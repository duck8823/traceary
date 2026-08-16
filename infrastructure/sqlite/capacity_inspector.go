package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"sort"
	"strings"
	"time"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
)

// CapacityInspector reads only SQLite metadata and aggregate lengths.
type CapacityInspector struct {
	db          *Database
	dbstatQuery func(context.Context, *sql.DB, int64) ([]apptypes.CapacityObject, error)
}

// NewCapacityInspector creates a metadata-only store capacity inspector.
func NewCapacityInspector(db *Database) *CapacityInspector {
	return &CapacityInspector{db: db, dbstatQuery: inspectDBStat}
}

func newCapacityInspectorWithDBStatQuery(db *Database, query func(context.Context, *sql.DB, int64) ([]apptypes.CapacityObject, error)) *CapacityInspector {
	return &CapacityInspector{db: db, dbstatQuery: query}
}

// InspectCapacity returns a metadata-only capacity snapshot.
func (i *CapacityInspector) InspectCapacity(ctx context.Context) (_ apptypes.CapacityReport, err error) {
	db, err := i.db.openReadOnly(ctx)
	if err != nil {
		return apptypes.CapacityReport{}, xerrors.Errorf("failed to open store for capacity inspection: %w", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil && err == nil {
			err = xerrors.Errorf("failed to close capacity inspection: %w", closeErr)
		}
	}()
	report := apptypes.CapacityReport{SchemaVersion: "traceary.capacity/v1"}
	if err := db.QueryRowContext(ctx, "PRAGMA page_size").Scan(&report.PageSizeBytes); err != nil {
		return report, xerrors.Errorf("failed to read page size: %w", err)
	}
	if err := db.QueryRowContext(ctx, "PRAGMA page_count").Scan(&report.PageCount); err != nil {
		return report, xerrors.Errorf("failed to read page count: %w", err)
	}
	if err := db.QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&report.FreePages); err != nil {
		return report, xerrors.Errorf("failed to read free pages: %w", err)
	}
	report.DatabaseBytes = report.PageSizeBytes * report.PageCount
	report.FreeBytes = report.PageSizeBytes * report.FreePages
	if info, statErr := os.Stat(i.db.Path() + "-wal"); statErr == nil {
		report.WALBytes = info.Size()
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return report, xerrors.Errorf("failed to stat WAL sidecar: %w", statErr)
	}

	started := time.Now()
	if cached, ok := loadMatchingDBStatCache(i.db.Path()); ok {
		report.Evidence = apptypes.CapacityEvidence{Status: "cached", Method: "dbstat", Reason: "dbstat cache hit for unchanged store file; inspected " + time.Since(started).String()}
		report.Objects = cached.Objects
	} else {
		budgetCtx, cancel := context.WithTimeout(ctx, dbstatInspectBudget)
		objects, statErr := i.dbstatQuery(budgetCtx, db, report.PageSizeBytes)
		deadlineExceeded := errors.Is(budgetCtx.Err(), context.DeadlineExceeded)
		cancel()
		elapsed := time.Since(started)
		if statErr != nil {
			if deadlineExceeded {
				report.Evidence = apptypes.CapacityEvidence{Status: "bounded", Method: "dbstat", Reason: "dbstat wall budget exceeded after " + elapsed.String()}
			} else if !isDBStatUnavailable(statErr) {
				return report, xerrors.Errorf("failed to inspect dbstat allocation: %w", statErr)
			} else {
				report.Evidence = apptypes.CapacityEvidence{Status: "unavailable", Method: "pragma", Reason: "dbstat unavailable; object attribution omitted"}
			}
		} else {
			report.Evidence = apptypes.CapacityEvidence{Status: "complete", Method: "dbstat", Reason: "inspected " + elapsed.String()}
			report.Objects = objects
			storeDBStatCache(i.db.Path(), objects)
		}
	}
	payloads, payloadEvidence, payloadErr := inspectPayloadClasses(ctx, db)
	if payloadErr != nil {
		return report, payloadErr
	}
	report.PayloadClasses = payloads
	report.PayloadEvidence = payloadEvidence
	return report, nil
}

func isDBStatUnavailable(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "no such table: dbstat") || strings.Contains(message, "no such module: dbstat")
}

// SQLite driver errors are internal adapter details and are contextualized by the caller.
//
//nolint:wrapcheck
func inspectDBStat(ctx context.Context, db *sql.DB, pageSize int64) (_ []apptypes.CapacityObject, err error) {
	rows, err := db.QueryContext(ctx, `SELECT d.name, CASE WHEN m.type = 'index' THEN 'index' ELSE 'table' END, count(*) FROM dbstat d LEFT JOIN sqlite_schema m ON m.name=d.name GROUP BY d.name, 2 ORDER BY d.name`)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()
	var result []apptypes.CapacityObject
	for rows.Next() {
		var item apptypes.CapacityObject
		if err := rows.Scan(&item.Name, &item.Kind, &item.Pages); err != nil {
			return nil, err
		}
		item.Bytes = item.Pages * pageSize
		result = append(result, item)
	}
	return result, rows.Err()
}

func inspectPayloadClasses(ctx context.Context, db *sql.DB) (_ []apptypes.CapacityPayloadClass, evidence apptypes.CapacityEvidence, err error) {
	var exists int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE type='table' AND name='event_metadata_projection'`).Scan(&exists); err != nil {
		return nil, evidence, xerrors.Errorf("failed to inspect event metadata schema: %w", err)
	}
	if exists == 0 {
		return []apptypes.CapacityPayloadClass{}, apptypes.CapacityEvidence{Status: "unavailable", Method: "event_metadata_projection", Reason: "metadata projection unavailable"}, nil
	}
	rows, err := db.QueryContext(ctx, `SELECT CASE WHEN body_stored_bytes < 1024 THEN 'lt_1_kib' WHEN body_stored_bytes < 65536 THEN '1_to_64_kib' WHEN body_stored_bytes < 1048576 THEN '64_kib_to_1_mib' ELSE 'gte_1_mib' END, count(*), coalesce(sum(body_stored_bytes),0) FROM event_metadata_projection GROUP BY 1`)
	if err != nil {
		return nil, evidence, xerrors.Errorf("failed to aggregate payload classes: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()
	result := make([]apptypes.CapacityPayloadClass, 0, 4)
	for rows.Next() {
		var item apptypes.CapacityPayloadClass
		if err := rows.Scan(&item.Name, &item.Rows, &item.Bytes); err != nil {
			return nil, evidence, xerrors.Errorf("failed to scan payload-class aggregate: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, evidence, xerrors.Errorf("failed to iterate payload-class aggregates: %w", err)
	}
	sort.Slice(result, func(a, b int) bool { return strings.Compare(result[a].Name, result[b].Name) < 0 })
	var eventCount, projectionCount int64
	if err := db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM events), (SELECT count(*) FROM event_metadata_projection)`).Scan(&eventCount, &projectionCount); err != nil {
		return nil, evidence, xerrors.Errorf("failed to inspect payload metadata coverage: %w", err)
	}
	evidence = apptypes.CapacityEvidence{Status: "complete", Method: "event_metadata_projection"}
	if projectionCount < eventCount {
		evidence.Status = "partial"
		evidence.Reason = "event metadata projection backfill incomplete"
	}
	return result, evidence, nil
}
