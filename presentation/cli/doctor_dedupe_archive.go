package cli

import (
	"context"
	"time"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
)

type doctorDedupeArchiveRun struct {
	RunID            string `json:"run_id"`
	Internal         bool   `json:"internal"`
	Rows             int    `json:"rows"`
	Bytes            int64  `json:"bytes"`
	OldestArchivedAt string `json:"oldest_archived_at"`
	PlannedRelease   string `json:"planned_release"`
}

func (c *RootCLI) inspectDedupeArchiveRuns(ctx context.Context) doctorCheck {
	if c.storeManagement == nil {
		return doctorCheck{
			Name:    "dedupe-archive-runs",
			Status:  doctorStatusSkip,
			Message: Localize("store management usecase is not configured", "store management usecase が設定されていません"),
		}
	}
	runs, err := c.storeManagement.ListContentEventDedupeRuns(ctx)
	return dedupeArchiveRunsDoctorCheck(runs, err, time.Now().UTC())
}

func dedupeArchiveRunsDoctorCheck(runs []apptypes.ContentEventDedupeRun, err error, now time.Time) doctorCheck {
	const name = "dedupe-archive-runs"
	if err != nil {
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusWarn,
			Message: localizef("failed to inventory dedupe archive runs: %v", "dedupe archive run の一覧に失敗しました: %v", err),
		}
	}
	inventory := make([]doctorDedupeArchiveRun, 0, len(runs))
	var internalRuns, internalRows int
	var internalBytes int64
	for _, run := range runs {
		item := doctorDedupeArchiveRun{
			RunID:            run.RunID,
			Internal:         run.Internal,
			Rows:             run.QuarantinedRows,
			Bytes:            run.BodyBytes,
			OldestArchivedAt: run.OldestArchivedAt,
			PlannedRelease:   plannedDedupeArchiveRelease(run, now),
		}
		inventory = append(inventory, item)
		if run.Internal {
			internalRuns++
			internalRows += run.QuarantinedRows
			internalBytes += run.BodyBytes
		}
	}
	check := doctorCheck{Name: name, DedupeArchiveRuns: inventory}
	if internalRows > 0 {
		check.Status = doctorStatusWarn
		check.Message = localizef(
			"%d compact-internal quarantine run(s) hold %d rows (~%s); replica/external store compact will drop them",
			"%d 件の compact 内部 quarantine run が %d 行（~%s）を保持しています。replica/external の store compact が破棄します",
			internalRuns, internalRows, formatCompactBytes(uint64(max(internalBytes, 0))),
		)
		check.Hint = "traceary store compact"
		return check
	}
	check.Status = doctorStatusPass
	if len(runs) == 0 {
		check.Message = Localize("no dedupe quarantine runs", "dedupe quarantine run はありません")
		return check
	}
	check.Message = localizef(
		"%d quarantine run(s) held, none created by compact",
		"%d 件の quarantine run があります。compact が作ったものはありません",
		len(runs),
	)
	return check
}

func plannedDedupeArchiveRelease(run apptypes.ContentEventDedupeRun, _ time.Time) string {
	if run.Internal {
		return "next replica/external store compact"
	}
	if run.OldestArchivedAt == "" {
		return "retained (archived_at is not a parseable timestamp)"
	}
	oldest, err := time.Parse(time.RFC3339Nano, run.OldestArchivedAt)
	if err != nil {
		return "retained (archived_at is not a parseable timestamp)"
	}
	return oldest.Add(application.DedupeArchiveRetention).UTC().Format(time.RFC3339)
}
