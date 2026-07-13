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
