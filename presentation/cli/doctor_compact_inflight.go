package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain"
)

const compactJournalDirName = ".traceary-compaction"

type compactInFlightJournal struct {
	ID        string                 `json:"id"`
	Phase     domain.CompactionPhase `json:"phase"`
	UpdatedAt time.Time              `json:"updated_at"`
}

func inspectCompactInFlightJournals(dbPath string, now time.Time) doctorCheck {
	return inspectCompactInFlightJournalsWithFix(dbPath, now, nil)
}

func inspectCompactInFlightJournalsWithFix(dbPath string, now time.Time, fix func(context.Context, bool) (doctorFixResult, error)) doctorCheck {
	const name = "compact-in-flight"
	if strings.TrimSpace(dbPath) == "" {
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusSkip,
			Message: Localize("store path is empty, so compaction journals were not inspected", "store path が空のため compaction journal は検査しません"),
		}
	}
	journals, err := listCompactInFlightJournals(filepath.Join(filepath.Dir(dbPath), compactJournalDirName))
	if err != nil {
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusWarn,
			Message: localizef("failed to inspect compaction journals: %v", "compaction journal を検査できません: %v", err),
		}
	}
	if len(journals) == 0 {
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusPass,
			Message: Localize("no in-flight compaction journal is present", "進行中の compaction journal はありません"),
		}
	}
	parts := make([]string, 0, len(journals))
	prePub := 0
	for _, journal := range journals {
		age := now.Sub(journal.UpdatedAt).Truncate(time.Second)
		if age < 0 {
			age = 0
		}
		parts = append(parts, localizef(
			"id=%s phase=%s age=%s",
			"id=%s phase=%s age=%s",
			journal.ID,
			journal.Phase,
			age,
		))
		if journal.Phase.IsPrePublication() {
			prePub++
		}
	}
	check := doctorCheck{
		Name:   name,
		Status: doctorStatusWarn,
		Message: localizef(
			"in-flight compaction journal(s): %s",
			"進行中の compaction journal: %s",
			strings.Join(parts, "; "),
		),
		Hint: Localize(
			"Pre-publication phases (planned / copy_intent / copy_retry_intent / candidate_prepared) can be abandoned by `traceary doctor --fix` or the next `store compact` when the source identity has moved. swap_intent and later keep today's resume/rollback semantics.",
			"publication 前の phase（planned / copy_intent / copy_retry_intent / candidate_prepared）は、source identity が動いていれば `traceary doctor --fix` または次の `store compact` が abandon します。swap_intent 以降は従来の resume/rollback です。",
		),
	}
	if prePub > 0 && fix != nil {
		check.FixCommand = "traceary doctor --fix"
		check.AutoFixAvailable = true
		check.StructuredFixFunc = fix
		check.FixFunc = func(ctx context.Context, dryRun bool) (string, error) {
			result, err := fix(ctx, dryRun)
			return result.Action, err
		}
	}
	return check
}

func (c *RootCLI) inspectCompactInFlight(dbPath string, now time.Time) doctorCheck {
	var fix func(context.Context, bool) (doctorFixResult, error)
	if c != nil && c.storeCompactionFactory != nil {
		fix = func(ctx context.Context, dryRun bool) (doctorFixResult, error) {
			if dryRun {
				return doctorFixResult{Action: Localize(
					"would abandon stale pre-publication compaction journals whose source identity has moved",
					"source identity が動いた publication 前 compaction journal を abandon します",
				)}, nil
			}
			_, usecase, err := c.compactionFor(dbPath)
			if err != nil {
				return doctorFixResult{}, err
			}
			abandoned, err := usecase.AbandonStalePrePublication(ctx, dbPath)
			if err != nil {
				return doctorFixResult{}, xerrors.Errorf("failed to abandon stale compaction journal: %w", err)
			}
			if abandoned.ID == "" {
				return doctorFixResult{
					Action:  Localize("no stale pre-publication compaction journal was abandoned", "abandon した publication 前 compaction journal はありません"),
					Metrics: map[string]int{"abandoned": 0},
				}, nil
			}
			return doctorFixResult{
				Action: localizef(
					"abandoned compact run id=%s phase=%s",
					"compaction run を abandon しました id=%s phase=%s",
					abandoned.ID,
					abandoned.Phase,
				),
				Metrics: map[string]int{"abandoned": 1},
			}, nil
		}
	}
	return inspectCompactInFlightJournalsWithFix(dbPath, now, fix)
}

func listCompactInFlightJournals(dir string) ([]compactInFlightJournal, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, xerrors.Errorf("failed to read compaction journal directory: %w", err)
	}
	var out []compactInFlightJournal
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		journal, ok, loadErr := loadCompactJournalSnapshot(filepath.Join(dir, entry.Name()))
		if loadErr != nil {
			return nil, loadErr
		}
		if !ok || journal.Phase.IsTerminal() {
			continue
		}
		out = append(out, journal)
	}
	return out, nil
}

func loadCompactJournalSnapshot(path string) (compactInFlightJournal, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return compactInFlightJournal{}, false, nil
		}
		return compactInFlightJournal{}, false, xerrors.Errorf("failed to open compaction journal: %w", err)
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var last []byte
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		last = append(last[:0], line...)
	}
	if err := scanner.Err(); err != nil {
		return compactInFlightJournal{}, false, xerrors.Errorf("failed to read compaction journal: %w", err)
	}
	if len(last) == 0 {
		return compactInFlightJournal{}, false, nil
	}
	var journal compactInFlightJournal
	if err := json.Unmarshal(last, &journal); err != nil {
		return compactInFlightJournal{}, false, xerrors.Errorf("failed to decode compaction journal: %w", err)
	}
	if journal.ID == "" || journal.Phase == "" {
		return compactInFlightJournal{}, false, nil
	}
	return journal, true, nil
}
