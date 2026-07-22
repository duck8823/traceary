package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestRunLineageDatasourceRecordRestoresNamespacedFactsAndKnownZero(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := sqlite.NewDatabase(filepath.Join(t.TempDir(), "traceary.db"), onDiskSQLiteMigrations(t))
	if err := sqlite.NewStoreManagementDatasource(db).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	sut := sqlite.NewRunLineageDatasource(db)

	first := sqliteRunLineage(t, "codex", "same", types.None[types.RunIdentity](), types.Some[int64](0))
	second := sqliteRunLineage(t, "claude", "same", types.None[types.RunIdentity](), types.None[int64]())
	for _, lineage := range []*model.RunLineage{first, second} {
		transition, err := sut.Record(ctx, lineage)
		if err != nil || transition != model.RunLineageTransitionApplied {
			t.Fatalf("Record() = %q, %v", transition, err)
		}
		transition, err = sut.Record(ctx, lineage)
		if err != nil || transition != model.RunLineageTransitionAlreadyApplied {
			t.Fatalf("replay = %q, %v", transition, err)
		}
	}
	restored, err := sut.FindByIdentity(ctx, first.Identity())
	if err != nil {
		t.Fatal(err)
	}
	value, present := restored.Value()
	if !present {
		t.Fatal("lineage missing")
	}
	bytes, present := value.ToolOutputBytes().Value()
	if !present || bytes != 0 {
		t.Fatalf("tool output bytes = %d, present=%v", bytes, present)
	}
	packet, present := value.Packet().Value()
	if !present || packet.Bytes() != 0 {
		t.Fatalf("packet = %#v, present=%v", packet, present)
	}

	conflict := sqliteRunLineage(t, "codex", "same", types.None[types.RunIdentity](), types.Some[int64](1))
	if _, err := sut.Record(ctx, conflict); !errors.Is(err, model.ErrConflictingRunLineage) {
		t.Fatalf("conflict error = %v", err)
	}
}

func TestRunLineageDatasourceRejectsMissingParentAndPreservesParentTree(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := sqlite.NewDatabase(filepath.Join(t.TempDir(), "traceary.db"), onDiskSQLiteMigrations(t))
	if err := sqlite.NewStoreManagementDatasource(db).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	sut := sqlite.NewRunLineageDatasource(db)
	parentID, _ := types.RunIdentityFrom("codex", "parent")
	child := sqliteRunLineage(t, "codex", "child", types.Some(parentID), types.None[int64]())
	if _, err := sut.Record(ctx, child); !errors.Is(err, model.ErrInvalidRunLineage) {
		t.Fatalf("missing parent error = %v", err)
	}
	if _, err := sut.Record(ctx, sqliteRunLineage(t, "codex", "parent", types.None[types.RunIdentity](), types.None[int64]())); err != nil {
		t.Fatal(err)
	}
	if _, err := sut.Record(ctx, child); err != nil {
		t.Fatal(err)
	}
}

func TestRunLineageDatasourceSerializesConcurrentReplayAndConflict(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name                      string
		proposals                 func(*testing.T) []*model.RunLineage
		wantAlready, wantConflict int
	}{
		{name: "identical", proposals: func(t *testing.T) []*model.RunLineage {
			value := sqliteRunLineage(t, "codex", "same", types.None[types.RunIdentity](), types.Some[int64](0))
			return []*model.RunLineage{value, value}
		}, wantAlready: 1},
		{name: "different", proposals: func(t *testing.T) []*model.RunLineage {
			return []*model.RunLineage{sqliteRunLineage(t, "codex", "different", types.None[types.RunIdentity](), types.Some[int64](0)), sqliteRunLineage(t, "codex", "different", types.None[types.RunIdentity](), types.Some[int64](1))}
		}, wantConflict: 1},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			db := sqlite.NewDatabase(filepath.Join(t.TempDir(), "traceary.db"), onDiskSQLiteMigrations(t))
			if err := sqlite.NewStoreManagementDatasource(db).Initialize(ctx); err != nil {
				t.Fatal(err)
			}
			sut := sqlite.NewRunLineageDatasource(db)
			proposals := test.proposals(t)
			type result struct {
				transition model.RunLineageTransition
				err        error
			}
			start, results := make(chan struct{}), make(chan result, 2)
			for _, proposal := range proposals {
				go func(value *model.RunLineage) {
					<-start
					transition, err := sut.Record(ctx, value)
					results <- result{transition, err}
				}(proposal)
			}
			close(start)
			applied, already, conflicts := 0, 0, 0
			for range proposals {
				got := <-results
				switch {
				case got.err == nil && got.transition == model.RunLineageTransitionApplied:
					applied++
				case got.err == nil && got.transition == model.RunLineageTransitionAlreadyApplied:
					already++
				case errors.Is(got.err, model.ErrConflictingRunLineage):
					conflicts++
				default:
					t.Fatalf("result = %q, %v", got.transition, got.err)
				}
			}
			if applied != 1 || already != test.wantAlready || conflicts != test.wantConflict {
				t.Fatalf("applied/already/conflicts = %d/%d/%d", applied, already, conflicts)
			}
		})
	}
}

func TestUsageObservationDatasourcePersistsRunAttributionAndRejectsSessionMismatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := sqlite.NewDatabase(filepath.Join(t.TempDir(), "traceary.db"), onDiskSQLiteMigrations(t))
	if err := sqlite.NewStoreManagementDatasource(db).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	runRepo := sqlite.NewRunLineageDatasource(db)
	identity, _ := types.RunIdentityFrom("codex", "run-usage")
	sessionID, _ := types.SessionIDFrom("session-usage")
	lineage, err := model.RunLineageOf(identity, types.None[types.RunIdentity](), types.Some(sessionID), types.EmptyRunWorkAttribution(), types.None[types.PacketIdentity](), types.None[int64]())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runRepo.Record(ctx, lineage); err != nil {
		t.Fatal(err)
	}

	id, _ := types.UsageObservationIDFrom("usage-with-run")
	source, _ := types.UsageSourceOf("codex", "local", "1", "", "")
	descriptor, err := model.NewUsageObservationDescriptorWithRunIdentity(id, sessionID, source, types.UsageScopeRun, types.UsageAccountingAdditive, time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC), identity)
	if err != nil {
		t.Fatal(err)
	}
	observation, _ := model.NewPendingUsageObservation(descriptor)
	usageRepo := sqlite.NewUsageObservationDatasource(db)
	if _, err := usageRepo.Record(ctx, observation); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	restored, err := usageRepo.FindByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	value, _ := restored.Value()
	run, present := value.Descriptor().RunIdentity().Value()
	if !present || run != identity {
		t.Fatalf("run identity = %#v, present=%v", run, present)
	}

	otherSession, _ := types.SessionIDFrom("other-session")
	otherID, _ := types.UsageObservationIDFrom("usage-mismatch")
	mismatchDescriptor, _ := model.NewUsageObservationDescriptorWithRunIdentity(otherID, otherSession, source, types.UsageScopeCall, types.UsageAccountingAdditive, time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC), identity)
	mismatch, _ := model.NewPendingUsageObservation(mismatchDescriptor)
	if _, err := usageRepo.Record(ctx, mismatch); err == nil {
		t.Fatal("session mismatch accepted")
	}
}

func TestBundleDatasourceRejectsInvalidUTF8StoredRunIdentity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	database := sqlite.NewDatabase(dbPath, onDiskSQLiteMigrations(t))
	if err := sqlite.NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO run_lineages(host, run_id) VALUES ('codex', CAST(X'FF' AS TEXT))`); err != nil {
		t.Fatalf("insert invalid UTF-8 fixture: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlite.NewBundleDatasource(database, nil).ListBundleRunLineages(ctx); err == nil {
		t.Fatal("invalid stored UTF-8 restored")
	}
}

func TestRunLineageMigrationRejectsMalformedAndMutableRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	database := sqlite.NewDatabase(dbPath, onDiskSQLiteMigrations(t))
	if err := sqlite.NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	if _, err := raw.Exec(`INSERT INTO run_lineages(host, run_id) VALUES ('codex', 'root')`); err != nil {
		t.Fatal(err)
	}
	invalid := []string{
		`INSERT INTO run_lineages(host, run_id, parent_host, parent_run_id) VALUES ('codex', 'self', 'codex', 'self')`,
		`INSERT INTO run_lineages(host, run_id, parent_host) VALUES ('codex', 'half-parent', 'codex')`,
		`INSERT INTO run_lineages(host, run_id, packet_sha256, packet_bytes) VALUES ('codex', 'bad-hash', '` + strings.Repeat("A", 64) + `', 0)`,
		`INSERT INTO run_lineages(host, run_id, packet_sha256) VALUES ('codex', 'half-packet', '` + strings.Repeat("a", 64) + `')`,
		`INSERT INTO run_lineages(host, run_id, tool_output_bytes) VALUES ('codex', 'negative', -1)`,
		`INSERT INTO run_lineages(host, run_id, pull_request_number) VALUES ('codex', 'pr-no-repo', 1)`,
	}
	for _, statement := range invalid {
		if _, err := raw.Exec(statement); err == nil {
			t.Fatalf("malformed row accepted: %s", statement)
		}
	}
	if _, err := raw.Exec(`UPDATE run_lineages SET run_id = 'changed' WHERE host = 'codex' AND run_id = 'root'`); err == nil {
		t.Fatal("immutable lineage updated")
	}
	if _, err := raw.Exec(`DELETE FROM run_lineages WHERE host = 'codex' AND run_id = 'root'`); err == nil {
		t.Fatal("immutable lineage deleted")
	}
}

func sqliteRunLineage(t *testing.T, host, runID string, parent types.Optional[types.RunIdentity], toolBytes types.Optional[int64]) *model.RunLineage {
	t.Helper()
	identity, err := types.RunIdentityFrom(host, runID)
	if err != nil {
		t.Fatal(err)
	}
	packet, err := types.PacketIdentityFrom(strings.Repeat("a", 64), 0)
	if err != nil {
		t.Fatal(err)
	}
	lineage, err := model.RunLineageOf(identity, parent, types.None[types.SessionID](), types.EmptyRunWorkAttribution(), types.Some(packet), toolBytes)
	if err != nil {
		t.Fatal(err)
	}
	return lineage
}
