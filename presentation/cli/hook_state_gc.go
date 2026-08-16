package cli

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"golang.org/x/xerrors"
)

const (
	hookStatePIDAgeFloor      = 24 * time.Hour
	hookStateResidueRetention = 14 * 24 * time.Hour
	hookStateGCHookBudget     = 20
	hookStateGCDoctorBudget   = 200
	hookStateResidueCheckName = "hook-state-residue"
)

var hookPerPIDStateName = regexp.MustCompile(`^(claude|codex|antigravity|grok|kimi|gemini)-(\d+)(-repo)?$`)

type hookStateResidueStats struct {
	PerPID      int
	StalePerPID int
	Diagnostics int
	Ended       int
}

func inspectHookStateResidueMetadata(now time.Time) doctorCheck {
	stats, err := inspectHookStateResidueStats(now)
	if err != nil {
		return doctorCheck{
			Name:    hookStateResidueCheckName,
			Status:  doctorStatusFail,
			Message: localizef("failed to inspect hook state residue: %v", "hook state 残渣の検査に失敗しました: %v", err),
		}
	}
	if stats.PerPID == 0 && stats.Diagnostics == 0 && stats.Ended == 0 {
		return doctorCheck{
			Name:    hookStateResidueCheckName,
			Status:  doctorStatusPass,
			Message: Localize("no leftover hook state, diagnostics, or ended markers", "hook state / diagnostics / ended marker の残渣はありません"),
		}
	}
	status := doctorStatusPass
	if stats.StalePerPID > 0 || stats.Diagnostics > 0 || stats.Ended > 0 {
		status = doctorStatusWarn
	}
	check := doctorCheck{
		Name:   hookStateResidueCheckName,
		Status: status,
		Message: localizef(
			"hook state residue: per_pid=%d stale_pid=%d diagnostics=%d ended=%d",
			"hook state 残渣: per_pid=%d stale_pid=%d diagnostics=%d ended=%d",
			stats.PerPID, stats.StalePerPID, stats.Diagnostics, stats.Ended,
		),
		Hint: Localize(
			"killed host processes leave per-PID state files; SessionEnd diagnostics and ended markers accumulate. Later hooks prune a bounded batch. Run `traceary doctor --fix` to catch up. Does not touch spool/ or spool/dead/.",
			"異常終了した host は per-PID state を残します。SessionEnd diagnostics と ended marker も蓄積します。後続 hook が bounded batch で掃除し、`traceary doctor --fix` で一括できます。spool/ と spool/dead/ は対象外です。",
		),
	}
	if status == doctorStatusWarn {
		check.FixCommand = "traceary doctor --fix"
		check.AutoFixAvailable = true
		check.FixFunc = func(_ context.Context, dryRun bool) (string, error) {
			result, err := gcHookStateResidues(now, hookStateGCDoctorBudget, dryRun)
			if err != nil {
				return "", err
			}
			return localizef(
				"pruned hook state residue: removed=%d remaining_stale=%d",
				"hook state 残渣を prune しました: removed=%d remaining_stale=%d",
				result.Removed, result.Remaining,
			), nil
		}
		check.StructuredFixFunc = func(_ context.Context, dryRun bool) (doctorFixResult, error) {
			result, err := gcHookStateResidues(now, hookStateGCDoctorBudget, dryRun)
			if err != nil {
				return doctorFixResult{}, err
			}
			return doctorFixResult{
				Action: localizef(
					"pruned hook state residue: removed=%d remaining_stale=%d",
					"hook state 残渣を prune しました: removed=%d remaining_stale=%d",
					result.Removed, result.Remaining,
				),
				Metrics: map[string]int{"removed": result.Removed, "remaining": result.Remaining},
			}, nil
		}
	}
	return check
}

type hookStateGCResult struct {
	Removed   int
	Remaining int
}

func inspectHookStateResidueStats(now time.Time) (hookStateResidueStats, error) {
	var stats hookStateResidueStats
	candidates, err := listHookStateResidueCandidates(now)
	if err != nil {
		return stats, err
	}
	for _, candidate := range candidates {
		switch candidate.kind {
		case hookResiduePerPID:
			stats.PerPID++
			if candidate.stale {
				stats.StalePerPID++
			}
		case hookResidueDiagnostics:
			stats.Diagnostics++
		case hookResidueEnded:
			stats.Ended++
		}
	}
	return stats, nil
}

type hookResidueKind int

const (
	hookResiduePerPID hookResidueKind = iota
	hookResidueDiagnostics
	hookResidueEnded
)

type hookResidueCandidate struct {
	path  string
	kind  hookResidueKind
	stale bool
}

func listHookStateResidueCandidates(now time.Time) ([]hookResidueCandidate, error) {
	stateDir, err := resolveHookStateDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(stateDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, xerrors.Errorf("failed to read hook state directory: %w", err)
	}
	var candidates []hookResidueCandidate
	for _, entry := range entries {
		name := entry.Name()
		path := filepath.Join(stateDir, name)
		if entry.IsDir() {
			switch name {
			case "diagnostics":
				aged, listErr := listAgedFiles(path, now, hookStateResidueRetention, hookResidueDiagnostics)
				if listErr != nil {
					return nil, listErr
				}
				candidates = append(candidates, aged...)
			case "ended":
				aged, listErr := listAgedFiles(path, now, hookStateResidueRetention, hookResidueEnded)
				if listErr != nil {
					return nil, listErr
				}
				candidates = append(candidates, aged...)
			}
			continue
		}
		matches := hookPerPIDStateName.FindStringSubmatch(name)
		if matches == nil {
			continue
		}
		pid, convErr := strconv.Atoi(matches[2])
		if convErr != nil {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return nil, xerrors.Errorf("failed to inspect hook state file: %w", infoErr)
		}
		stale := !hookProcessAlive(pid) && now.Sub(info.ModTime()) >= hookStatePIDAgeFloor
		candidates = append(candidates, hookResidueCandidate{path: path, kind: hookResiduePerPID, stale: stale})
	}
	return candidates, nil
}

func listAgedFiles(dir string, now time.Time, retention time.Duration, kind hookResidueKind) ([]hookResidueCandidate, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, xerrors.Errorf("failed to read %s: %w", dir, err)
	}
	var out []hookResidueCandidate
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return nil, xerrors.Errorf("failed to inspect %s: %w", entry.Name(), infoErr)
		}
		if now.Sub(info.ModTime()) < retention {
			continue
		}
		out = append(out, hookResidueCandidate{path: filepath.Join(dir, entry.Name()), kind: kind, stale: true})
	}
	return out, nil
}

func gcHookStateResidues(now time.Time, budget int, dryRun bool) (hookStateGCResult, error) {
	if budget < 1 {
		budget = hookStateGCHookBudget
	}
	candidates, err := listHookStateResidueCandidates(now)
	if err != nil {
		return hookStateGCResult{}, err
	}
	var result hookStateGCResult
	for _, candidate := range candidates {
		if !candidate.stale {
			continue
		}
		if result.Removed >= budget {
			result.Remaining++
			continue
		}
		if dryRun {
			result.Removed++
			continue
		}
		if removeErr := os.Remove(candidate.path); removeErr != nil && !os.IsNotExist(removeErr) {
			return result, xerrors.Errorf("failed to remove hook state residue: %w", removeErr)
		}
		result.Removed++
	}
	return result, nil
}

func maybeGCHookStateResidues() {
	_, _ = gcHookStateResidues(time.Now().UTC(), hookStateGCHookBudget, false)
}
