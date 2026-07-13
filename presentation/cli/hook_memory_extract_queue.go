package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

const (
	hookMemoryExtractJobSchemaVersion = 1
	hookMemoryExtractMaxRunsPerWorker = 2
	hookMemoryExtractErrorLimit       = 1024
)

type hookMemoryExtractRequest struct {
	SessionID      types.SessionID
	Workspace      types.Workspace
	DBPath         string
	SourceBoundary string
}

type hookMemoryExtractJob struct {
	SchemaVersion  int             `json:"schema_version"`
	SessionID      types.SessionID `json:"session_id"`
	Workspace      types.Workspace `json:"workspace"`
	DBPath         string          `json:"db_path"`
	SourceBoundary string          `json:"source_boundary"`
	RequestedAt    time.Time       `json:"requested_at"`
	Attempts       int             `json:"attempts,omitempty"`
	LastAttemptAt  *time.Time      `json:"last_attempt_at,omitempty"`
	LastError      string          `json:"last_error,omitempty"`
	Path           string          `json:"-"`
}

func (c *RootCLI) newHookMemoryExtractWorkerCommand() *cobra.Command {
	var jobPath string
	cmd := &cobra.Command{
		Use:    "memory-extract-worker",
		Short:  "Process one durable hook memory-extraction job",
		Hidden: true,
		Args:   noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runHookMemoryExtractWorker(cmd.Context(), jobPath)
		},
	}
	cmd.Flags().StringVar(&jobPath, "job", "", "durable extraction job path")
	_ = cmd.MarkFlagRequired("job")
	return cmd
}

func (c *RootCLI) scheduleHookMemoryExtract(request hookMemoryExtractRequest) {
	if c.memory == nil {
		return
	}
	jobPath, err := enqueueHookMemoryExtract(request, time.Now().UTC())
	if err != nil {
		slog.Debug("hook memory extraction enqueue failed", "session_id", request.SessionID, "source_boundary", request.SourceBoundary, "error", err)
		return
	}
	if err := c.launchHookMemoryExtractWorker(jobPath); err != nil {
		// The durable job remains pending. A later hook invocation can launch
		// another worker, and doctor surfaces the backlog in the meantime.
		slog.Debug("hook memory extraction worker launch failed", "job", jobPath, "error", err)
	}
}

func (c *RootCLI) launchHookMemoryExtractWorker(jobPath string) error {
	launcher := c.hookMemoryExtractLauncher
	if launcher == nil {
		launcher = launchDetachedHookMemoryExtractWorker
	}
	return launcher(jobPath)
}

func hookMemoryExtractQueueDir() (string, error) {
	stateDir, err := resolveHookStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(stateDir, "memory-extract"), nil
}

func hookMemoryExtractJobPath(request hookMemoryExtractRequest) (string, error) {
	dir, err := hookMemoryExtractQueueDir()
	if err != nil {
		return "", err
	}
	key := strings.Join([]string{
		strings.TrimSpace(request.DBPath),
		strings.TrimSpace(request.SessionID.String()),
		strings.TrimSpace(request.Workspace.String()),
	}, "\x00")
	digest := sha256.Sum256([]byte(key))
	return filepath.Join(dir, hex.EncodeToString(digest[:])+".json"), nil
}

func enqueueHookMemoryExtract(request hookMemoryExtractRequest, requestedAt time.Time) (string, error) {
	if request.SessionID == "" {
		return "", xerrors.Errorf("memory extraction session ID is empty")
	}
	jobPath, err := hookMemoryExtractJobPath(request)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(jobPath), 0o700); err != nil {
		return "", xerrors.Errorf("failed to create memory extraction queue: %w", err)
	}
	jobLock := flock.New(jobPath + ".lock")
	locked, err := jobLock.TryLock()
	if err != nil {
		return "", xerrors.Errorf("failed to lock memory extraction job: %w", err)
	}
	if !locked {
		// The worker may already have read the current job. A separate rerun
		// marker preserves a boundary that arrives while extraction is active.
		if err := publishHookMemoryExtractRerun(jobPath+".rerun", requestedAt); err != nil {
			return "", xerrors.Errorf("failed to mark memory extraction rerun: %w", err)
		}
		// Cover the completion race where the worker removed the job between
		// our failed lock attempt and rerun publication. The worker also checks
		// the marker after removal; either side may recreate the same job, and
		// the atomic rename keeps the result readable.
		if _, statErr := os.Stat(jobPath); errors.Is(statErr, os.ErrNotExist) {
			job := hookMemoryExtractJob{
				SchemaVersion:  hookMemoryExtractJobSchemaVersion,
				SessionID:      request.SessionID,
				Workspace:      request.Workspace,
				DBPath:         strings.TrimSpace(request.DBPath),
				SourceBoundary: strings.TrimSpace(request.SourceBoundary),
				RequestedAt:    readHookMemoryExtractRerunTime(jobPath+".rerun", requestedAt),
			}
			if err := writeHookMemoryExtractJob(jobPath, job); err != nil {
				return "", err
			}
		} else if statErr != nil {
			return "", xerrors.Errorf("failed to inspect contended memory extraction job: %w", statErr)
		}
		return jobPath, nil
	}
	defer func() { _ = jobLock.Unlock() }()

	job := hookMemoryExtractJob{
		SchemaVersion:  hookMemoryExtractJobSchemaVersion,
		SessionID:      request.SessionID,
		Workspace:      request.Workspace,
		DBPath:         strings.TrimSpace(request.DBPath),
		SourceBoundary: strings.TrimSpace(request.SourceBoundary),
		RequestedAt:    requestedAt,
	}
	if existing, readErr := readHookMemoryExtractJob(jobPath); readErr == nil {
		if existing.RequestedAt.Before(job.RequestedAt) {
			job.RequestedAt = existing.RequestedAt
		}
		job.Attempts = existing.Attempts
		job.LastAttemptAt = existing.LastAttemptAt
		job.LastError = existing.LastError
	} else if !errors.Is(readErr, os.ErrNotExist) {
		corruptPath := fmt.Sprintf("%s.%s.corrupt.json", jobPath, requestedAt.Format("20060102T150405.000000000Z"))
		if renameErr := os.Rename(jobPath, corruptPath); renameErr != nil {
			return "", xerrors.Errorf("failed to quarantine unreadable memory extraction job: %w", renameErr)
		}
	}
	if err := writeHookMemoryExtractJob(jobPath, job); err != nil {
		return "", err
	}
	return jobPath, nil
}

func (c *RootCLI) runHookMemoryExtractWorker(ctx context.Context, jobPath string) error {
	if c.storeManagement == nil {
		return xerrors.Errorf("initialize store usecase is not configured")
	}
	if c.memory == nil {
		return xerrors.Errorf("memory usecase is not configured")
	}
	resolvedJobPath, err := filepath.Abs(strings.TrimSpace(jobPath))
	if err != nil {
		return xerrors.Errorf("failed to resolve memory extraction job path: %w", err)
	}
	queueDir, err := hookMemoryExtractQueueDir()
	if err != nil {
		return xerrors.Errorf("failed to create memory extraction rerun marker: %w", err)
	}
	insideQueue, err := filepath.Rel(queueDir, resolvedJobPath)
	if err != nil || insideQueue == "." || filepath.Dir(insideQueue) != "." || filepath.IsAbs(insideQueue) {
		return xerrors.Errorf("memory extraction job is outside the queue directory")
	}
	name := filepath.Base(insideQueue)
	if len(name) != 69 || !strings.HasSuffix(name, ".json") {
		return xerrors.Errorf("memory extraction job name is invalid")
	}
	if _, decodeErr := hex.DecodeString(strings.TrimSuffix(name, ".json")); decodeErr != nil {
		return xerrors.Errorf("memory extraction job name is invalid: %w", decodeErr)
	}
	info, statErr := os.Lstat(resolvedJobPath)
	if statErr == nil && !info.Mode().IsRegular() {
		return xerrors.Errorf("memory extraction job is not a regular file")
	}
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return xerrors.Errorf("failed to inspect memory extraction job: %w", statErr)
	}

	jobLock := flock.New(resolvedJobPath + ".lock")
	locked, err := jobLock.TryLock()
	if err != nil {
		return xerrors.Errorf("failed to lock memory extraction job: %w", err)
	}
	if !locked {
		return nil
	}
	lockHeld := true
	defer func() {
		if lockHeld {
			_ = jobLock.Unlock()
		}
	}()

	for run := 0; run < hookMemoryExtractMaxRunsPerWorker; run++ {
		job, readErr := readHookMemoryExtractJob(resolvedJobPath)
		if errors.Is(readErr, os.ErrNotExist) {
			return nil
		}
		if readErr != nil {
			return readErr
		}
		attemptedAt := time.Now().UTC()
		job.Attempts++
		job.LastAttemptAt = &attemptedAt
		job.LastError = ""
		if writeErr := writeHookMemoryExtractJob(resolvedJobPath, job); writeErr != nil {
			return writeErr
		}
		resolvedDBPath, resolveErr := resolveDBPath(job.DBPath)
		if resolveErr != nil {
			return c.failHookMemoryExtractJob(resolvedJobPath, job, resolveErr)
		}
		c.applyDatabasePath(resolvedDBPath)
		if initErr := c.storeManagement.Initialize(ctx); initErr != nil {
			return c.failHookMemoryExtractJob(resolvedJobPath, job, xerrors.Errorf("failed to initialize store: %w", initErr))
		}
		_, extractErr := c.memory.Extract(ctx, apptypes.NewMemoryExtractionCriteriaBuilder().
			SessionID(job.SessionID).
			Workspace(job.Workspace).
			Build())
		if extractErr != nil {
			return c.failHookMemoryExtractJob(resolvedJobPath, job, extractErr)
		}
		if _, rerunErr := os.Stat(resolvedJobPath + ".rerun"); rerunErr == nil {
			rerunRequestedAt := readHookMemoryExtractRerunTime(resolvedJobPath+".rerun", job.RequestedAt)
			_ = os.Remove(resolvedJobPath + ".rerun")
			if rerunRequestedAt.Before(job.RequestedAt) {
				job.RequestedAt = rerunRequestedAt
			}
			job.LastError = ""
			if writeErr := writeHookMemoryExtractJob(resolvedJobPath, job); writeErr != nil {
				return writeErr
			}
			if run+1 >= hookMemoryExtractMaxRunsPerWorker {
				if unlockErr := jobLock.Unlock(); unlockErr != nil {
					return xerrors.Errorf("failed to release memory extraction job before handoff: %w", unlockErr)
				}
				lockHeld = false
				if launchErr := c.launchHookMemoryExtractWorker(resolvedJobPath); launchErr != nil {
					return xerrors.Errorf("failed to hand off pending memory extraction job: %w", launchErr)
				}
				return nil
			}
			continue
		} else if !os.IsNotExist(rerunErr) {
			return xerrors.Errorf("failed to inspect memory extraction rerun marker: %w", rerunErr)
		}
		if c.hookMemoryBeforeJobRemoval != nil {
			c.hookMemoryBeforeJobRemoval()
		}
		if removeErr := os.Remove(resolvedJobPath); removeErr != nil && !os.IsNotExist(removeErr) {
			return xerrors.Errorf("failed to clear memory extraction job: %w", removeErr)
		}
		if _, rerunErr := os.Stat(resolvedJobPath + ".rerun"); rerunErr == nil {
			job.RequestedAt = readHookMemoryExtractRerunTime(resolvedJobPath+".rerun", job.RequestedAt)
			_ = os.Remove(resolvedJobPath + ".rerun")
			if writeErr := writeHookMemoryExtractJob(resolvedJobPath, job); writeErr != nil {
				return writeErr
			}
			if unlockErr := jobLock.Unlock(); unlockErr != nil {
				return xerrors.Errorf("failed to release memory extraction job after completion race: %w", unlockErr)
			}
			lockHeld = false
			if launchErr := c.launchHookMemoryExtractWorker(resolvedJobPath); launchErr != nil {
				return xerrors.Errorf("failed to hand off raced memory extraction job: %w", launchErr)
			}
			return nil
		} else if !os.IsNotExist(rerunErr) {
			return xerrors.Errorf("failed to inspect post-completion memory extraction rerun marker: %w", rerunErr)
		}
		if c.hookMemoryAfterFinalCheck != nil {
			c.hookMemoryAfterFinalCheck()
		}
		if unlockErr := jobLock.Unlock(); unlockErr != nil {
			return xerrors.Errorf("failed to release completed memory extraction job: %w", unlockErr)
		}
		lockHeld = false
		// Close the final lost-wakeup window: an enqueue that observed the
		// old lock after our post-remove check may have recreated the job and
		// launched a worker that exited before this unlock. Relaunch it here.
		if _, statErr := os.Stat(resolvedJobPath); statErr == nil {
			if launchErr := c.launchHookMemoryExtractWorker(resolvedJobPath); launchErr != nil {
				return xerrors.Errorf("failed to hand off post-completion memory extraction job: %w", launchErr)
			}
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return xerrors.Errorf("failed to inspect post-unlock memory extraction job: %w", statErr)
		}
		return nil
	}
	return nil
}

func (c *RootCLI) failHookMemoryExtractJob(path string, job hookMemoryExtractJob, cause error) error {
	job.LastError = truncateHookMemoryExtractError(cause.Error())
	if err := writeHookMemoryExtractJob(path, job); err != nil {
		return xerrors.Errorf("memory extraction failed (%v) and job metadata update failed: %w", cause, err)
	}
	return xerrors.Errorf("memory extraction failed: %w", cause)
}

func readHookMemoryExtractJob(path string) (hookMemoryExtractJob, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return hookMemoryExtractJob{}, xerrors.Errorf("failed to read memory extraction job: %w", err)
	}
	var job hookMemoryExtractJob
	if err := json.Unmarshal(data, &job); err != nil {
		return hookMemoryExtractJob{}, xerrors.Errorf("failed to decode memory extraction job: %w", err)
	}
	if job.SchemaVersion != hookMemoryExtractJobSchemaVersion || job.SessionID == "" {
		return hookMemoryExtractJob{}, xerrors.Errorf("unsupported or incomplete memory extraction job")
	}
	job.Path = path
	return job, nil
}

func writeHookMemoryExtractJob(path string, job hookMemoryExtractJob) error {
	encoded, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		return xerrors.Errorf("failed to encode memory extraction job: %w", err)
	}
	encoded = append(encoded, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".memory-extract-*.tmp")
	if err != nil {
		return xerrors.Errorf("failed to create memory extraction job: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return xerrors.Errorf("failed to protect memory extraction job: %w", err)
	}
	if _, err := tmp.Write(encoded); err != nil {
		_ = tmp.Close()
		return xerrors.Errorf("failed to write memory extraction job: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return xerrors.Errorf("failed to sync memory extraction job: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return xerrors.Errorf("failed to close memory extraction job: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return xerrors.Errorf("failed to publish memory extraction job: %w", err)
	}
	return nil
}

func publishHookMemoryExtractRerun(path string, requestedAt time.Time) error {
	return publishHookMemoryExtractRerunWithHook(path, requestedAt, nil)
}

func publishHookMemoryExtractRerunWithHook(path string, requestedAt time.Time, beforePublish func()) error {
	marker, err := os.CreateTemp(filepath.Dir(path), ".memory-extract-rerun-*.tmp")
	if err != nil {
		return xerrors.Errorf("failed to create memory extraction rerun marker: %w", err)
	}
	tmpPath := marker.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := marker.Chmod(0o600); err != nil {
		_ = marker.Close()
		return xerrors.Errorf("failed to protect memory extraction rerun marker: %w", err)
	}
	if _, err := marker.WriteString(requestedAt.Format(time.RFC3339Nano) + "\n"); err != nil {
		_ = marker.Close()
		_ = os.Remove(tmpPath)
		return xerrors.Errorf("failed to write memory extraction rerun marker: %w", err)
	}
	if err := marker.Sync(); err != nil {
		_ = marker.Close()
		_ = os.Remove(tmpPath)
		return xerrors.Errorf("failed to sync memory extraction rerun marker: %w", err)
	}
	if err := marker.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return xerrors.Errorf("failed to close memory extraction rerun marker: %w", err)
	}
	if beforePublish != nil {
		beforePublish()
	}
	if err := os.Link(tmpPath, path); errors.Is(err, os.ErrExist) {
		return nil
	} else if err != nil {
		return xerrors.Errorf("failed to publish memory extraction rerun marker: %w", err)
	}
	return nil
}

func readHookMemoryExtractRerunTime(path string, fallback time.Time) time.Time {
	data, err := os.ReadFile(path)
	if err != nil {
		return fallback
	}
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(data)))
	if err != nil {
		return fallback
	}
	if parsed.Before(fallback) {
		return parsed
	}
	return fallback
}

func launchDetachedHookMemoryExtractWorker(jobPath string) error {
	executable, err := os.Executable()
	if err != nil {
		return xerrors.Errorf("failed to resolve traceary executable: %w", err)
	}
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return xerrors.Errorf("failed to open null device: %w", err)
	}
	defer func() { _ = devNull.Close() }()
	cmd := exec.Command(executable, "hook", "memory-extract-worker", "--job", jobPath)
	cmd.Stdin = devNull
	cmd.Stdout = devNull
	cmd.Stderr = devNull
	cmd.Env = append(os.Environ(), hookAuditSuppressionEnvKey+"=1")
	configureDetachedHookProcess(cmd)
	if err := cmd.Start(); err != nil {
		return xerrors.Errorf("failed to start memory extraction worker: %w", err)
	}
	if err := cmd.Process.Release(); err != nil {
		return xerrors.Errorf("failed to release memory extraction worker: %w", err)
	}
	return nil
}

func scanHookMemoryExtractJobs() ([]hookMemoryExtractJob, []string, error) {
	dir, err := hookMemoryExtractQueueDir()
	if err != nil {
		return nil, nil, err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, xerrors.Errorf("failed to read memory extraction queue: %w", err)
	}
	jobs := []hookMemoryExtractJob{}
	unreadable := []string{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		job, readErr := readHookMemoryExtractJob(path)
		if readErr != nil {
			unreadable = append(unreadable, path)
			continue
		}
		jobs = append(jobs, job)
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].RequestedAt.Before(jobs[j].RequestedAt) })
	sort.Strings(unreadable)
	return jobs, unreadable, nil
}

func inspectHookMemoryExtractDiagnostics(now time.Time) doctorCheck {
	const name = "hook-memory-extract"
	jobs, unreadable, err := scanHookMemoryExtractJobs()
	if err != nil {
		return doctorCheck{Name: name, Status: doctorStatusFail, Message: localizef("failed to inspect memory extraction queue: %v", "memory extraction queue の検査に失敗しました: %v", err)}
	}
	if len(jobs) == 0 && len(unreadable) == 0 {
		return doctorCheck{Name: name, Status: doctorStatusPass, Message: Localize("no pending hook memory extraction jobs found", "未処理の hook memory extraction job はありません")}
	}
	failed := 0
	oldestAge := time.Duration(0)
	if len(jobs) > 0 {
		oldestAge = now.Sub(jobs[0].RequestedAt)
		if oldestAge < 0 {
			oldestAge = 0
		}
	}
	for _, job := range jobs {
		if job.Attempts > 0 {
			failed++
		}
	}
	return doctorCheck{
		Name:   name,
		Status: doctorStatusWarn,
		Message: localizef(
			"found %d pending memory extraction job(s), %d previously failed job(s), and %d unreadable job(s); oldest age %s",
			"未処理の memory extraction job が %d 件、以前失敗した job が %d 件、読めない job が %d 件あります。最古 age %s",
			len(jobs), failed, len(unreadable), oldestAge.Round(time.Second),
		),
		Hint: Localize("a later hook retries pending jobs automatically; run doctor again after the next hook and inspect debug logs if failures remain", "次の hook が未処理 job を自動再試行します。次の hook 後に doctor を再実行し、失敗が残る場合は debug log を確認してください"),
	}
}

func truncateHookMemoryExtractError(message string) string {
	message = strings.TrimSpace(message)
	if len(message) <= hookMemoryExtractErrorLimit {
		return message
	}
	return fmt.Sprintf("%s...", message[:hookMemoryExtractErrorLimit-3])
}
