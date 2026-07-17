package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/presentation"
)

// Auto archive is fail-closed: only runs when retention.mode is archive_then_gc.
const (
	defaultArchiveAutoInterval = 168 * time.Hour
	archiveAutoTimeout         = 2 * time.Minute
	archiveAutoStatusSchema    = 1
)

// archiveAutoStatus is persisted next to the per-DB lease marker for doctor.
type archiveAutoStatus struct {
	SchemaVersion int    `json:"schema_version"`
	At            string `json:"at"`
	OK            bool   `json:"ok"`
	Rows          int    `json:"rows"`
	Deleted       int    `json:"deleted"`
	Path          string `json:"path,omitempty"`
	Error         string `json:"error,omitempty"`
	Mode          string `json:"mode"`
	Interval      string `json:"interval"`
	KeepDays      int    `json:"keep_days"`
	Target        string `json:"target"`
}

// runOpportunisticArchiveThenGC exports cold rows and deletes after verify when
// config.retention.mode is archive_then_gc. Detached from hook soft-deadline.
// Never stores passphrases; reads them only via passphrase_env when set.
func (c *RootCLI) runOpportunisticArchiveThenGC(ctx context.Context, dbPath string) {
	if c.storeManagement == nil || strings.TrimSpace(dbPath) == "" {
		return
	}
	cfg := presentation.LoadConfig().Retention
	if !strings.EqualFold(strings.TrimSpace(cfg.Mode), presentation.RetentionModeArchiveThenGC) {
		return
	}

	interval, err := parseArchiveAutoInterval(cfg.Interval)
	if err != nil {
		slog.Debug("archive auto: invalid interval", "error", err, "interval", cfg.Interval)
		_ = writeArchiveAutoStatus(dbPath, archiveAutoStatus{
			SchemaVersion: archiveAutoStatusSchema,
			At:            time.Now().UTC().Format(time.RFC3339Nano),
			OK:            false,
			Error:         "invalid interval: " + err.Error(),
			Mode:          cfg.Mode,
			Interval:      cfg.Interval,
		})
		return
	}

	markerPath, statusPath, err := archiveAutoPaths(dbPath)
	if err != nil {
		slog.Debug("archive auto: path resolution failed", "error", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(markerPath), 0o700); err != nil {
		slog.Debug("archive auto: state dir failed", "error", err)
		return
	}

	now := time.Now().UTC()
	if archiveAutoRecentlyCompleted(markerPath, now, interval) {
		return
	}

	lease := flock.New(markerPath + ".lock")
	locked, err := lease.TryLock()
	if err != nil || !locked {
		if err != nil {
			slog.Debug("archive auto: lease failed", "error", err)
		}
		return
	}
	defer func() {
		if err := lease.Unlock(); err != nil {
			slog.Debug("archive auto: lease unlock failed", "error", err)
		}
	}()
	if archiveAutoRecentlyCompleted(markerPath, now, interval) {
		return
	}

	keepDays := cfg.KeepDays
	if keepDays <= 0 {
		keepDays = defaultRetentionDays
	}
	targetName := strings.TrimSpace(cfg.Target)
	if targetName == "" {
		targetName = "all"
	}
	target, ok := apptypes.GarbageCollectionTargetFrom(targetName)
	if !ok {
		_ = writeArchiveAutoStatus(dbPath, archiveAutoStatus{
			SchemaVersion: archiveAutoStatusSchema,
			At:            now.Format(time.RFC3339Nano),
			OK:            false,
			Error:         "invalid target: " + targetName,
			Mode:          cfg.Mode,
			Interval:      interval.String(),
			KeepDays:      keepDays,
			Target:        targetName,
		})
		return
	}

	passphrase, err := readPassphraseEnv(cfg.PassphraseEnv)
	if err != nil {
		// Missing secret: fail closed, no delete (acceptance: missing secret).
		_ = writeArchiveAutoStatus(dbPath, archiveAutoStatus{
			SchemaVersion: archiveAutoStatusSchema,
			At:            now.Format(time.RFC3339Nano),
			OK:            false,
			Error:         err.Error(),
			Mode:          cfg.Mode,
			Interval:      interval.String(),
			KeepDays:      keepDays,
			Target:        targetName,
		})
		return
	}

	outDir, err := resolveArchiveAutoOutputDir(cfg.OutputDir)
	if err != nil {
		_ = writeArchiveAutoStatus(dbPath, archiveAutoStatus{
			SchemaVersion: archiveAutoStatusSchema,
			At:            now.Format(time.RFC3339Nano),
			OK:            false,
			Error:         err.Error(),
			Mode:          cfg.Mode,
			Interval:      interval.String(),
			KeepDays:      keepDays,
			Target:        targetName,
		})
		return
	}
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		_ = writeArchiveAutoStatus(dbPath, archiveAutoStatus{
			SchemaVersion: archiveAutoStatusSchema,
			At:            now.Format(time.RFC3339Nano),
			OK:            false,
			Error:         "output_dir: " + err.Error(),
			Mode:          cfg.Mode,
			Interval:      interval.String(),
			KeepDays:      keepDays,
			Target:        targetName,
		})
		return
	}
	outPath := filepath.Join(outDir, "traceary-archive-"+now.Format("20060102T150405Z")+".trcaryar")

	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), archiveAutoTimeout)
	defer cancel()

	cutoff := now.AddDate(0, 0, -keepDays)
	result, err := c.storeManagement.CreateStoreArchive(runCtx, apptypes.StoreArchiveCreateParams{
		OutputPath:        outPath,
		Before:            cutoff,
		KeepDays:          keepDays,
		Target:            target,
		DryRun:            false,
		DeleteAfterVerify: true,
		Passphrase:        passphrase,
		ToolVersion:       "",
		SourceDBPath:      dbPath,
	})
	status := archiveAutoStatus{
		SchemaVersion: archiveAutoStatusSchema,
		At:            now.Format(time.RFC3339Nano),
		Mode:          presentation.RetentionModeArchiveThenGC,
		Interval:      interval.String(),
		KeepDays:      keepDays,
		Target:        targetName,
		Rows:          result.TotalRows,
		Deleted:       result.DeletedCount,
		Path:          result.Path,
	}
	if err != nil {
		status.OK = false
		status.Error = err.Error()
		_ = writeArchiveAutoStatus(dbPath, status)
		slog.Debug("archive auto failed", "error", err)
		return
	}
	status.OK = true
	if err := os.WriteFile(markerPath, []byte(now.Format(time.RFC3339Nano)), 0o600); err != nil {
		status.OK = false
		status.Error = "marker write: " + err.Error()
		_ = writeArchiveAutoStatus(dbPath, status)
		return
	}
	_ = writeArchiveAutoStatus(dbPath, status)
	// statusPath used only via writeArchiveAutoStatus helper.
	_ = statusPath
	slog.Debug("archive auto completed", "rows", result.TotalRows, "deleted", result.DeletedCount, "path", result.Path)
}

func parseArchiveAutoInterval(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultArchiveAutoInterval, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, xerrors.Errorf("parse interval %q: %w", raw, err)
	}
	if d <= 0 {
		return 0, xerrors.Errorf("interval must be positive")
	}
	return d, nil
}

func resolveArchiveAutoOutputDir(configured string) (string, error) {
	configured = strings.TrimSpace(configured)
	if configured != "" {
		if strings.HasPrefix(configured, "~/") {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", xerrors.Errorf("home for output_dir: %w", err)
			}
			return filepath.Join(home, configured[2:]), nil
		}
		return configured, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", xerrors.Errorf("home for default archives: %w", err)
	}
	return filepath.Join(home, ".config", "traceary", "archives"), nil
}

func archiveAutoPaths(dbPath string) (markerPath, statusPath string, err error) {
	stateDir, err := resolveHookStateDir()
	if err != nil {
		return "", "", err
	}
	digest := sha256.Sum256([]byte(strings.TrimSpace(dbPath)))
	base := filepath.Join(stateDir, "archive-auto", hex.EncodeToString(digest[:]))
	return base + ".stamp", base + ".status.json", nil
}

func archiveAutoRecentlyCompleted(markerPath string, now time.Time, interval time.Duration) bool {
	info, err := os.Stat(markerPath)
	if err != nil {
		return false
	}
	return now.Sub(info.ModTime()) < interval
}

func writeArchiveAutoStatus(dbPath string, status archiveAutoStatus) error {
	_, statusPath, err := archiveAutoPaths(dbPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(statusPath), 0o700); err != nil {
		return xerrors.Errorf("create archive auto status dir: %w", err)
	}
	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return xerrors.Errorf("marshal archive auto status: %w", err)
	}
	data = append(data, '\n')
	tmp := statusPath + ".partial"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return xerrors.Errorf("write archive auto status partial: %w", err)
	}
	if err := os.Rename(tmp, statusPath); err != nil {
		return xerrors.Errorf("finalize archive auto status: %w", err)
	}
	return nil
}

func readArchiveAutoStatus(dbPath string) (archiveAutoStatus, error) {
	_, statusPath, err := archiveAutoPaths(dbPath)
	if err != nil {
		return archiveAutoStatus{}, err
	}
	data, err := os.ReadFile(statusPath)
	if err != nil {
		return archiveAutoStatus{}, xerrors.Errorf("read archive auto status: %w", err)
	}
	var status archiveAutoStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return archiveAutoStatus{}, xerrors.Errorf("parse archive auto status: %w", err)
	}
	return status, nil
}
