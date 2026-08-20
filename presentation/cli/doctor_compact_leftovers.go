package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/xerrors"
)

func compactInFlightIDSet(dbPath string) (map[string]struct{}, error) {
	journals, err := listCompactInFlightJournals(filepath.Join(filepath.Dir(dbPath), compactJournalDirName))
	if err != nil {
		return nil, err
	}
	ids := make(map[string]struct{}, len(journals))
	for _, journal := range journals {
		if journal.ID != "" {
			ids[journal.ID] = struct{}{}
		}
	}
	return ids, nil
}

func compactArtifactRunID(dbPath, path string) string {
	base := filepath.Base(dbPath)
	name := filepath.Base(path)
	for _, kind := range []string{".compact-", ".rollback-"} {
		prefix := base + kind
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		rest := strings.TrimPrefix(name, prefix)
		for _, suffix := range []string{".work-journal", "-journal", "-wal", "-shm"} {
			rest = strings.TrimSuffix(rest, suffix)
		}
		return rest
	}
	return ""
}

func listAbandonedCompactLeftovers(dbPath string) ([]string, error) {
	inflight, err := compactInFlightIDSet(dbPath)
	if err != nil {
		return nil, err
	}
	matches, err := filepath.Glob(dbPath + ".compact-*")
	if err != nil {
		return nil, xerrors.Errorf("failed to inspect compact work leftovers: %w", err)
	}
	workJournals, err := filepath.Glob(filepath.Join(filepath.Dir(dbPath), "*.work-journal"))
	if err != nil {
		return nil, xerrors.Errorf("failed to inspect compact work-journal leftovers: %w", err)
	}
	seen := map[string]struct{}{}
	var out []string
	for _, path := range append(append([]string{}, matches...), workJournals...) {
		if _, dup := seen[path]; dup {
			continue
		}
		seen[path] = struct{}{}
		info, statErr := os.Lstat(path)
		if statErr != nil || info == nil || !info.Mode().IsRegular() {
			continue
		}
		if !strings.Contains(filepath.Base(path), ".compact-") {
			continue
		}
		runID := compactArtifactRunID(dbPath, path)
		if runID == "" {
			continue
		}
		if _, live := inflight[runID]; live {
			continue
		}
		out = append(out, path)
	}
	return out, nil
}

func unlinkRegularCompactArtifacts(paths []string, dryRun bool) (removed int, err error) {
	for _, path := range paths {
		info, statErr := os.Lstat(path)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				continue
			}
			return removed, xerrors.Errorf("failed to inspect compact leftover %s: %w", path, statErr)
		}
		if info == nil || !info.Mode().IsRegular() {
			continue
		}
		if dryRun {
			removed++
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return removed, xerrors.Errorf("failed to remove compact leftover %s: %w", path, err)
		}
		removed++
	}
	return removed, nil
}

func fixAbandonedCompactLeftovers(ctx context.Context, dbPath string, dryRun bool) (doctorFixResult, error) {
	if err := ctx.Err(); err != nil {
		return doctorFixResult{}, xerrors.Errorf("compact leftover fix canceled: %w", err)
	}
	leftovers, err := listAbandonedCompactLeftovers(dbPath)
	if err != nil {
		return doctorFixResult{}, err
	}
	removed, err := unlinkRegularCompactArtifacts(leftovers, dryRun)
	if err != nil {
		return doctorFixResult{}, err
	}
	if dryRun {
		return doctorFixResult{
			Action: localizef(
				"would remove %d abandoned compact leftover file(s)",
				"abandoned compact leftover を %d 件削除します",
				removed,
			),
			Metrics: map[string]int{"removed": removed},
		}, nil
	}
	return doctorFixResult{
		Action: localizef(
			"removed %d abandoned compact leftover file(s)",
			"abandoned compact leftover を %d 件削除しました",
			removed,
		),
		Metrics: map[string]int{"removed": removed},
	}, nil
}

func doctorFixCommand(dbPath string) string {
	if strings.TrimSpace(dbPath) == "" {
		return "traceary doctor --fix"
	}
	return "traceary doctor --fix --db-path " + shellQuote(dbPath)
}
