package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"github.com/spf13/cobra"
	"golang.org/x/xerrors"
)

const (
	hookGrokTranscriptJobSchemaVersion = 1
	hookGrokTranscriptRetryCount       = 20
	hookGrokTranscriptRetryInterval    = 100 * time.Millisecond
)

type hookGrokTranscriptJob struct {
	SchemaVersion int       `json:"schema_version"`
	Payload       string    `json:"payload"`
	DBPath        string    `json:"db_path,omitempty"`
	RequestedAt   time.Time `json:"requested_at"`
	Attempts      int       `json:"attempts,omitempty"`
	LastAttemptAt time.Time `json:"last_attempt_at,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
}

func scanHookGrokTranscriptJobs() ([]hookGrokTranscriptJob, []string, error) {
	dir, err := hookGrokTranscriptQueueDir()
	if err != nil {
		return nil, nil, err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, xerrors.Errorf("failed to read Grok transcript queue: %w", err)
	}
	jobs := []hookGrokTranscriptJob{}
	unreadable := []string{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		job, readErr := readHookGrokTranscriptJob(path)
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

func inspectHookGrokTranscriptDiagnostics(now time.Time) doctorCheck {
	const name = "hook-grok-transcript"
	jobs, unreadable, err := scanHookGrokTranscriptJobs()
	if err != nil {
		return doctorCheck{Name: name, Status: doctorStatusFail, Message: localizef("failed to inspect Grok transcript queue: %v", "Grok transcript queue の検査に失敗しました: %v", err)}
	}
	if len(jobs) == 0 && len(unreadable) == 0 {
		return doctorCheck{Name: name, Status: doctorStatusPass, Message: Localize("no pending Grok transcript jobs found", "未処理の Grok transcript job はありません")}
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
			"found %d pending Grok transcript job(s), %d previously failed job(s), and %d unreadable job(s); oldest age %s",
			"未処理の Grok transcript job が %d 件、以前失敗した job が %d 件、読めない job が %d 件あります。最古 age %s",
			len(jobs), failed, len(unreadable), oldestAge.Round(time.Second),
		),
		Hint: Localize("the final Grok transcript remained unavailable after Stop; enable TRACEARY_HOOK_DEBUG for the next turn and remove a pending job only after confirming it is stale", "Stop 後も最終 Grok transcript を取得できませんでした。次の turn で TRACEARY_HOOK_DEBUG を有効にし、未処理 job は stale と確認してから削除してください"),
	}
}

func (c *RootCLI) newHookGrokTranscriptWorkerCommand() *cobra.Command {
	var jobPath string
	cmd := &cobra.Command{
		Use:    "transcript-worker",
		Short:  "Process one durable Grok transcript job",
		Hidden: true,
		Args:   noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runHookGrokTranscriptWorker(cmd.Context(), jobPath)
		},
	}
	cmd.Flags().StringVar(&jobPath, "job", "", "durable Grok transcript job path")
	_ = cmd.MarkFlagRequired("job")
	return cmd
}

func (c *RootCLI) scheduleHookGrokTranscript(payload []byte, dbPath string) error {
	jobPath, err := enqueueHookGrokTranscript(payload, dbPath, time.Now().UTC())
	if err != nil {
		return err
	}
	launcher := c.hookGrokTranscriptLauncher
	if launcher == nil {
		launcher = launchDetachedHookGrokTranscriptWorker
	}
	if err := launcher(jobPath); err != nil {
		return xerrors.Errorf("failed to launch Grok transcript worker: %w", err)
	}
	return nil
}

func hookGrokTranscriptQueueDir() (string, error) {
	stateDir, err := resolveHookStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(stateDir, "grok-transcript"), nil
}

func enqueueHookGrokTranscript(payload []byte, dbPath string, requestedAt time.Time) (string, error) {
	sessionID := strings.TrimSpace(hookPayloadString(payload, "session_id", ""))
	transcriptPath := strings.TrimSpace(hookPayloadString(payload, "transcript_path", ""))
	if sessionID == "" || transcriptPath == "" {
		return "", xerrors.Errorf("Grok transcript job requires session_id and transcript_path")
	}
	dir, err := hookGrokTranscriptQueueDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", xerrors.Errorf("failed to create Grok transcript queue: %w", err)
	}
	key := strings.Join([]string{
		strings.TrimSpace(dbPath),
		sessionID,
		strings.TrimSpace(hookPayloadString(payload, "prompt_id", "")),
		transcriptPath,
	}, "\x00")
	digest := sha256.Sum256([]byte(key))
	jobPath := filepath.Join(dir, hex.EncodeToString(digest[:])+".json")
	job := hookGrokTranscriptJob{
		SchemaVersion: hookGrokTranscriptJobSchemaVersion,
		Payload:       string(payload),
		DBPath:        strings.TrimSpace(dbPath),
		RequestedAt:   requestedAt,
	}
	if existing, readErr := readHookGrokTranscriptJob(jobPath); readErr == nil {
		job.Attempts = existing.Attempts
		job.LastAttemptAt = existing.LastAttemptAt
		job.LastError = existing.LastError
		if existing.RequestedAt.Before(job.RequestedAt) {
			job.RequestedAt = existing.RequestedAt
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return "", readErr
	}
	if err := writeHookGrokTranscriptJob(jobPath, job); err != nil {
		return "", err
	}
	return jobPath, nil
}

func (c *RootCLI) runHookGrokTranscriptWorker(ctx context.Context, jobPath string) error {
	if c.storeManagement == nil || c.event == nil {
		return xerrors.Errorf("Grok transcript worker usecases are not configured")
	}
	resolvedJobPath, err := validateHookGrokTranscriptJobPath(jobPath)
	if err != nil {
		return err
	}

	jobLock := flock.New(resolvedJobPath + ".lock")
	locked, err := jobLock.TryLock()
	if err != nil {
		return xerrors.Errorf("failed to lock Grok transcript job: %w", err)
	}
	if !locked {
		return nil
	}
	defer func() { _ = jobLock.Unlock() }()

	job, err := readHookGrokTranscriptJob(resolvedJobPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	payload := []byte(job.Payload)
	for attempt := 0; attempt < hookGrokTranscriptRetryCount; attempt++ {
		if _, ready := extractGrokTranscript(payload); ready {
			if err := c.runHookTranscript(ctx, bytes.NewReader(payload), grokHookClient, job.DBPath); err != nil {
				return c.failHookGrokTranscriptJob(resolvedJobPath, job, err)
			}
			if err := os.Remove(resolvedJobPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return xerrors.Errorf("failed to clear completed Grok transcript job: %w", err)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return c.failHookGrokTranscriptJob(resolvedJobPath, job, ctx.Err())
		case <-time.After(hookGrokTranscriptRetryInterval):
		}
	}
	return c.failHookGrokTranscriptJob(resolvedJobPath, job, xerrors.Errorf("Grok transcript was not ready after %s", hookGrokTranscriptRetryCount*hookGrokTranscriptRetryInterval))
}

func validateHookGrokTranscriptJobPath(jobPath string) (string, error) {
	resolvedJobPath, err := filepath.Abs(strings.TrimSpace(jobPath))
	if err != nil {
		return "", xerrors.Errorf("failed to resolve Grok transcript job path: %w", err)
	}
	queueDir, err := hookGrokTranscriptQueueDir()
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(queueDir, resolvedJobPath)
	if err != nil || relative == "." || filepath.Dir(relative) != "." || filepath.IsAbs(relative) {
		return "", xerrors.Errorf("Grok transcript job is outside the queue directory")
	}
	name := filepath.Base(relative)
	if len(name) != 69 || !strings.HasSuffix(name, ".json") {
		return "", xerrors.Errorf("Grok transcript job name is invalid")
	}
	if _, err := hex.DecodeString(strings.TrimSuffix(name, ".json")); err != nil {
		return "", xerrors.Errorf("Grok transcript job name is invalid: %w", err)
	}
	info, err := os.Lstat(resolvedJobPath)
	if err == nil && !info.Mode().IsRegular() {
		return "", xerrors.Errorf("Grok transcript job is not a regular file")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", xerrors.Errorf("failed to inspect Grok transcript job: %w", err)
	}
	return resolvedJobPath, nil
}

func readHookGrokTranscriptJob(path string) (hookGrokTranscriptJob, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return hookGrokTranscriptJob{}, xerrors.Errorf("failed to read Grok transcript job: %w", err)
	}
	var job hookGrokTranscriptJob
	if err := json.Unmarshal(data, &job); err != nil {
		return hookGrokTranscriptJob{}, xerrors.Errorf("failed to decode Grok transcript job: %w", err)
	}
	if job.SchemaVersion != hookGrokTranscriptJobSchemaVersion || strings.TrimSpace(job.Payload) == "" {
		return hookGrokTranscriptJob{}, xerrors.Errorf("Grok transcript job has an unsupported shape")
	}
	return job, nil
}

func writeHookGrokTranscriptJob(path string, job hookGrokTranscriptJob) error {
	encoded, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		return xerrors.Errorf("failed to encode Grok transcript job: %w", err)
	}
	encoded = append(encoded, '\n')
	temporaryFile, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return xerrors.Errorf("failed to create Grok transcript job temporary file: %w", err)
	}
	temporaryPath := temporaryFile.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporaryFile.Chmod(0o600); err != nil {
		_ = temporaryFile.Close()
		return xerrors.Errorf("failed to protect Grok transcript job temporary file: %w", err)
	}
	if _, err := temporaryFile.Write(encoded); err != nil {
		_ = temporaryFile.Close()
		return xerrors.Errorf("failed to write Grok transcript job: %w", err)
	}
	if err := temporaryFile.Close(); err != nil {
		return xerrors.Errorf("failed to close Grok transcript job: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return xerrors.Errorf("failed to publish Grok transcript job: %w", err)
	}
	return nil
}

func (c *RootCLI) failHookGrokTranscriptJob(path string, job hookGrokTranscriptJob, cause error) error {
	job.Attempts++
	job.LastAttemptAt = time.Now().UTC()
	job.LastError = "transcript unavailable"
	if err := writeHookGrokTranscriptJob(path, job); err != nil {
		return errors.Join(cause, err)
	}
	return xerrors.Errorf("Grok transcript job remains pending: %w", cause)
}

func launchDetachedHookGrokTranscriptWorker(jobPath string) error {
	executable, err := os.Executable()
	if err != nil {
		return xerrors.Errorf("failed to resolve traceary executable: %w", err)
	}
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return xerrors.Errorf("failed to open null device: %w", err)
	}
	defer func() { _ = devNull.Close() }()
	cmd := exec.Command(executable, "hook", "grok", "transcript-worker", "--job", jobPath)
	cmd.Stdin = devNull
	cmd.Stdout = devNull
	cmd.Stderr = devNull
	cmd.Env = append(os.Environ(), hookAuditSuppressionEnvKey+"=1")
	configureDetachedHookProcess(cmd)
	if err := cmd.Start(); err != nil {
		return xerrors.Errorf("failed to start Grok transcript worker: %w", err)
	}
	if err := cmd.Process.Release(); err != nil {
		return xerrors.Errorf("failed to release Grok transcript worker: %w", err)
	}
	return nil
}
