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
	hookGrokTranscriptJobSchemaVersion      = 1
	hookGrokTranscriptTerminalSchemaVersion = 1
	hookGrokTranscriptRetryCount            = 20
	hookGrokTranscriptRetryInterval         = 100 * time.Millisecond
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

type hookGrokTranscriptTerminal struct {
	SchemaVersion int       `json:"schema_version"`
	Disposition   string    `json:"disposition"`
	OccurredAt    time.Time `json:"occurred_at"`
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
	terminals, terminalUnreadable, terminalErr := scanHookGrokTranscriptTerminals()
	if terminalErr != nil {
		return doctorCheck{Name: name, Status: doctorStatusFail, Message: localizef("failed to inspect Grok transcript terminal dispositions: %v", "Grok transcript の終端 disposition 検査に失敗しました: %v", terminalErr)}
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
	terminalCounts := map[string]int{}
	for _, terminal := range terminals {
		terminalCounts[terminal.Disposition]++
	}
	partial := terminalCounts["unavailable"] + terminalCounts["malformed"] + terminalCounts["cancelled"]
	// A recorded marker is a successful idempotency receipt, not an operator
	// warning. Only outstanding work, partial coverage, or unreadable state
	// requires attention from doctor.
	if len(jobs) == 0 && len(unreadable) == 0 && partial == 0 && terminalUnreadable == 0 {
		if terminalCounts["recorded"] == 0 {
			return doctorCheck{Name: name, Status: doctorStatusPass, Message: Localize("no pending Grok transcript jobs found", "未処理の Grok transcript job はありません")}
		}
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusPass,
			Message: localizef("no pending Grok transcript jobs found; %d final-turn transcript(s) already recorded", "未処理の Grok transcript job はありません。final-turn transcript は %d 件記録済みです", terminalCounts["recorded"]),
		}
	}
	return doctorCheck{
		Name:   name,
		Status: doctorStatusWarn,
		Message: localizef(
			"found %d pending Grok transcript job(s), %d previously failed job(s), %d unreadable job(s), and %d partial final-turn disposition(s) (%d unavailable, %d malformed, %d cancelled; %d unreadable disposition marker(s)); oldest age %s",
			"未処理の Grok transcript job が %d 件、以前失敗した job が %d 件、読めない job が %d 件、partial final-turn disposition が %d 件（unavailable %d 件、malformed %d 件、cancelled %d 件、読めない disposition marker %d 件）あります。最古の経過時間は %s です",
			len(jobs), failed, len(unreadable), partial, terminalCounts["unavailable"], terminalCounts["malformed"], terminalCounts["cancelled"], terminalUnreadable, oldestAge.Round(time.Second),
		),
		Hint: Localize("a terminal final-turn disposition is partial coverage and has no retry job; enable TRACEARY_HOOK_DEBUG for the next turn. Remove a pending job only after confirming it is stale", "終端 final-turn disposition は partial coverage であり retry job はありません。次の turn で TRACEARY_HOOK_DEBUG を有効にしてください。未処理 job は不要と確認してからだけ削除してください"),
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
	jobPath, shouldLaunch, err := enqueueHookGrokTranscript(payload, dbPath, time.Now().UTC())
	if err != nil {
		return err
	}
	if !shouldLaunch {
		return nil
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

func hookGrokTranscriptTerminalDir() (string, error) {
	stateDir, err := resolveHookStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(stateDir, "grok-transcript-terminal"), nil
}

// enqueueHookGrokTranscript creates work only when this delivery has neither a
// durable job nor a terminal receipt. The boolean is false for either
// idempotent case, so callers do not launch another detached worker for a
// redelivered Stop.
func enqueueHookGrokTranscript(payload []byte, dbPath string, requestedAt time.Time) (string, bool, error) {
	sessionID := strings.TrimSpace(hookPayloadString(payload, "session_id", ""))
	transcriptPath := strings.TrimSpace(hookPayloadString(payload, "transcript_path", ""))
	if sessionID == "" || transcriptPath == "" {
		return "", false, xerrors.Errorf("Grok transcript job requires session_id and transcript_path")
	}
	dir, err := hookGrokTranscriptQueueDir()
	if err != nil {
		return "", false, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", false, xerrors.Errorf("failed to create Grok transcript queue: %w", err)
	}
	key := strings.Join([]string{
		strings.TrimSpace(dbPath),
		sessionID,
		strings.TrimSpace(hookPayloadString(payload, "prompt_id", "")),
		transcriptPath,
	}, "\x00")
	digest := sha256.Sum256([]byte(key))
	jobPath := filepath.Join(dir, hex.EncodeToString(digest[:])+".json")
	jobLock := flock.New(jobPath + ".lock")
	if err := jobLock.Lock(); err != nil {
		return "", false, xerrors.Errorf("failed to lock Grok transcript enqueue: %w", err)
	}
	defer func() { _ = jobLock.Unlock() }()

	terminalPath, err := hookGrokTranscriptTerminalPath(jobPath)
	if err != nil {
		return "", false, err
	}
	if _, readErr := readHookGrokTranscriptTerminal(terminalPath); readErr == nil {
		return jobPath, false, nil
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return "", false, readErr
	}

	job := hookGrokTranscriptJob{
		SchemaVersion: hookGrokTranscriptJobSchemaVersion,
		Payload:       string(payload),
		DBPath:        strings.TrimSpace(dbPath),
		RequestedAt:   requestedAt,
	}
	if _, readErr := readHookGrokTranscriptJob(jobPath); readErr == nil {
		return jobPath, false, nil
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return "", false, readErr
	}
	if err := writeHookGrokTranscriptJob(jobPath, job); err != nil {
		return "", false, err
	}
	return jobPath, true, nil
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
		blocks, disposition := inspectGrokTranscript(payload)
		if disposition == grokTranscriptReady {
			if _, err := c.runHookTranscript(ctx, bytes.NewReader(payload), grokHookClient, job.DBPath); err != nil {
				return c.failHookGrokTranscriptJob(resolvedJobPath, job, err)
			}
			_ = blocks // body ownership remains in runHookTranscript; this branch is state-only.
			return finalizeHookGrokTranscriptJob(resolvedJobPath, "recorded")
		}
		if disposition == grokTranscriptUnavailable || disposition == grokTranscriptMalformed {
			return finalizeHookGrokTranscriptJob(resolvedJobPath, string(disposition))
		}
		select {
		case <-ctx.Done():
			return finalizeHookGrokTranscriptJob(resolvedJobPath, "cancelled")
		case <-time.After(hookGrokTranscriptRetryInterval):
		}
	}
	return finalizeHookGrokTranscriptJob(resolvedJobPath, "unavailable")
}

func finalizeHookGrokTranscriptJob(jobPath, disposition string) error {
	if err := writeHookGrokTranscriptTerminal(jobPath, disposition, time.Now().UTC()); err != nil {
		return err
	}
	if err := os.Remove(jobPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return xerrors.Errorf("failed to clear terminal Grok transcript job: %w", err)
	}
	return nil
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
	if job.SchemaVersion != hookGrokTranscriptJobSchemaVersion || strings.TrimSpace(job.Payload) == "" || job.RequestedAt.IsZero() || job.Attempts < 0 {
		return hookGrokTranscriptJob{}, xerrors.Errorf("Grok transcript job has an unsupported shape")
	}
	return job, nil
}

func scanHookGrokTranscriptTerminals() ([]hookGrokTranscriptTerminal, int, error) {
	dir, err := hookGrokTranscriptTerminalDir()
	if err != nil {
		return nil, 0, err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, xerrors.Errorf("failed to read Grok transcript terminal dispositions: %w", err)
	}
	terminals := []hookGrokTranscriptTerminal{}
	unreadable := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		terminal, readErr := readHookGrokTranscriptTerminal(filepath.Join(dir, entry.Name()))
		if readErr != nil {
			unreadable++
			continue
		}
		terminals = append(terminals, terminal)
	}
	return terminals, unreadable, nil
}

func readHookGrokTranscriptTerminal(path string) (hookGrokTranscriptTerminal, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return hookGrokTranscriptTerminal{}, xerrors.Errorf("failed to read Grok transcript terminal disposition: %w", err)
	}
	var terminal hookGrokTranscriptTerminal
	if err := json.Unmarshal(data, &terminal); err != nil {
		return hookGrokTranscriptTerminal{}, xerrors.Errorf("failed to decode Grok transcript terminal disposition: %w", err)
	}
	if terminal.SchemaVersion != hookGrokTranscriptTerminalSchemaVersion || terminal.OccurredAt.IsZero() || !validHookGrokTranscriptTerminalDisposition(terminal.Disposition) {
		return hookGrokTranscriptTerminal{}, xerrors.Errorf("Grok transcript terminal disposition has an unsupported shape")
	}
	return terminal, nil
}

func validHookGrokTranscriptTerminalDisposition(disposition string) bool {
	switch disposition {
	case "recorded", "unavailable", "malformed", "cancelled":
		return true
	default:
		return false
	}
}

func writeHookGrokTranscriptTerminal(jobPath, disposition string, occurredAt time.Time) error {
	if !validHookGrokTranscriptTerminalDisposition(disposition) || occurredAt.IsZero() {
		return xerrors.Errorf("Grok transcript terminal disposition is invalid")
	}
	dir, err := hookGrokTranscriptTerminalDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return xerrors.Errorf("failed to create Grok transcript terminal disposition directory: %w", err)
	}
	terminalPath, err := hookGrokTranscriptTerminalPath(jobPath)
	if err != nil {
		return err
	}
	terminal := hookGrokTranscriptTerminal{
		SchemaVersion: hookGrokTranscriptTerminalSchemaVersion,
		Disposition:   disposition,
		OccurredAt:    occurredAt.UTC(),
	}
	encoded, err := json.MarshalIndent(terminal, "", "  ")
	if err != nil {
		return xerrors.Errorf("failed to encode Grok transcript terminal disposition: %w", err)
	}
	encoded = append(encoded, '\n')
	temporaryFile, err := os.CreateTemp(dir, "."+filepath.Base(terminalPath)+".*.tmp")
	if err != nil {
		return xerrors.Errorf("failed to create Grok transcript terminal disposition temporary file: %w", err)
	}
	temporaryPath := temporaryFile.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporaryFile.Chmod(0o600); err != nil {
		_ = temporaryFile.Close()
		return xerrors.Errorf("failed to protect Grok transcript terminal disposition temporary file: %w", err)
	}
	if _, err := temporaryFile.Write(encoded); err != nil {
		_ = temporaryFile.Close()
		return xerrors.Errorf("failed to write Grok transcript terminal disposition: %w", err)
	}
	if err := temporaryFile.Close(); err != nil {
		return xerrors.Errorf("failed to close Grok transcript terminal disposition: %w", err)
	}
	if err := os.Rename(temporaryPath, terminalPath); err != nil {
		return xerrors.Errorf("failed to publish Grok transcript terminal disposition: %w", err)
	}
	return nil
}

func hookGrokTranscriptTerminalPath(jobPath string) (string, error) {
	name := filepath.Base(jobPath)
	if len(name) != 69 || !strings.HasSuffix(name, ".json") {
		return "", xerrors.Errorf("Grok transcript terminal disposition job name is invalid")
	}
	dir, err := hookGrokTranscriptTerminalDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
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
