package sqlite_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestReleaseGateEvaluator_RefusesLiveStore(t *testing.T) {
	t.Parallel()
	live, err := application.DefaultLiveStorePath()
	if err != nil {
		t.Fatalf("DefaultLiveStorePath() error = %v", err)
	}
	_, err = sqlite.NewReleaseGateEvaluator(sqlite.NewDatabase(live, nil)).
		Evaluate(context.Background(), time.Time{})
	if err == nil || !strings.Contains(err.Error(), "refusing the default live store") {
		t.Fatalf("Evaluate(live) error = %v, want live-store refusal", err)
	}
}

func TestReleaseGateEvaluator_PassingFixturePassesGates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "pass.db")
	fx := newReleaseGateFixture(t, dbPath)
	fx.savePrompt(ctx, "p1", "sess", uniqueBody("pass", 2<<20))
	fx.insertSession("sess", "claude")
	fx.refine(ctx, "sess", "p1", "We needed a fixture so the remaining #1620 rows can trip a release automatically.", false)

	report, err := sqlite.NewReleaseGateEvaluator(sqlite.NewDatabase(dbPath, onDiskSQLiteMigrations(t))).
		Evaluate(ctx, time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if report.SchemaVersion != application.ReleaseGateSchemaVersion {
		t.Fatalf("schema = %q", report.SchemaVersion)
	}
	if !report.Passed {
		t.Fatalf("passed = false, gates = %+v", report.Gates)
	}
	byID := gateByID(report.Gates)
	if byID["emission_amplification"].Status != application.ReleaseGateStatusPass {
		t.Fatalf("emission = %+v", byID["emission_amplification"])
	}
	if byID["whole_store_amplification"].Status != application.ReleaseGateStatusPass {
		t.Fatalf("whole_store = %+v", byID["whole_store_amplification"])
	}
	if byID["recent_index_amplification"].Status != application.ReleaseGateStatusSkip {
		t.Fatalf("recent_index = %+v, want skip on a fixture without a measured generation", byID["recent_index_amplification"])
	}
	if byID["body_duplicate_share"].Status != application.ReleaseGateStatusPass {
		t.Fatalf("duplicate = %+v", byID["body_duplicate_share"])
	}
	if byID["refinement_coverage"].Status != application.ReleaseGateStatusPass {
		t.Fatalf("refinement = %+v", byID["refinement_coverage"])
	}
	if byID["wake_injection"].Status != application.ReleaseGateStatusPass {
		t.Fatalf("wake = %+v", byID["wake_injection"])
	}
	if len(report.Measurements) != 5 {
		t.Fatalf("measurements = %d, want 5", len(report.Measurements))
	}
	for _, m := range report.Measurements {
		if m.Corpus != application.ReleaseGateMeasurementCorpus {
			t.Fatalf("measurement %s corpus = %q", m.ID, m.Corpus)
		}
	}
}

func TestReleaseGateEvaluator_EachGateCanMiss(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)

	t.Run("emission amplification", func(t *testing.T) {
		t.Parallel()
		dbPath := filepath.Join(t.TempDir(), "emission.db")
		fx := newReleaseGateFixture(t, dbPath)
		fx.savePrompt(ctx, "p1", "sess", "canonical")
		fx.saveTranscript(ctx, "t1", "sess", "extra-1")
		fx.saveTranscript(ctx, "t2", "sess", "extra-2")
		fx.saveTranscript(ctx, "t3", "sess", "extra-3")
		fx.insertSession("sess", "cli")
		assertGateMiss(t, dbPath, now, "emission_amplification")
	})

	t.Run("whole-store amplification", func(t *testing.T) {
		t.Parallel()
		dbPath := filepath.Join(t.TempDir(), "amp.db")
		fx := newReleaseGateFixture(t, dbPath)
		fx.savePrompt(ctx, "p1", "sess", "tiny")
		fx.insertSession("sess", "cli")
		assertGateMiss(t, dbPath, now, "whole_store_amplification")
	})

	t.Run("body duplicate share", func(t *testing.T) {
		t.Parallel()
		dbPath := filepath.Join(t.TempDir(), "dup.db")
		fx := newReleaseGateFixture(t, dbPath)
		body := strings.Repeat("D", 1024)
		for i := 0; i < 20; i++ {
			fx.savePrompt(ctx, fmt.Sprintf("p-%02d", i), "sess", body)
		}
		fx.insertSession("sess", "cli")
		assertGateMiss(t, dbPath, now, "body_duplicate_share")
	})

	t.Run("refinement coverage", func(t *testing.T) {
		t.Parallel()
		dbPath := filepath.Join(t.TempDir(), "refine.db")
		fx := newReleaseGateFixture(t, dbPath)
		fx.savePrompt(ctx, "p1", "sess", uniqueBody("refine", int(application.DefaultFoldThresholdBytes)+64))
		fx.insertSession("sess", "cli")
		assertGateMiss(t, dbPath, now, "refinement_coverage")
	})

	t.Run("wake injection", func(t *testing.T) {
		t.Parallel()
		dbPath := filepath.Join(t.TempDir(), "wake.db")
		fx := newReleaseGateFixture(t, dbPath)
		fx.savePrompt(ctx, "p1", "sess", uniqueBody("wake", int(application.DefaultFoldThresholdBytes)+64))
		fx.insertSession("sess", "claude")
		fx.refine(ctx, "sess", "p1", strings.Repeat("W", 9000), false)
		assertGateMiss(t, dbPath, now, "wake_injection")
	})

	t.Run("recent index amplification", func(t *testing.T) {
		t.Parallel()
		dbPath := filepath.Join(t.TempDir(), "recent.db")
		fx := newReleaseGateFixture(t, dbPath)
		fx.savePrompt(ctx, "p1", "sess", "canonical")
		fx.insertSession("sess", "cli")
		conn := openCallSiteDB(t, dbPath)
		if _, err := conn.Exec(`UPDATE search_projection_state
SET state='complete', recent_amplification_ppm=5000000, capacity_evidence_status='measured'`); err != nil {
			t.Fatalf("seed measured recent-index amplification: %v", err)
		}
		assertGateMiss(t, dbPath, now, "recent_index_amplification")
	})
}

func TestReleaseGateEvaluator_UnavailableRecentIndexIsSkipped(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "recent-skip.db")
	fx := newReleaseGateFixture(t, dbPath)
	fx.savePrompt(ctx, "p1", "sess", uniqueBody("skip", 2<<20))
	fx.insertSession("sess", "claude")
	fx.refine(ctx, "sess", "p1", "We needed a fixture so an unavailable fallback amplification cannot pass as measured.", false)
	conn := openCallSiteDB(t, dbPath)
	if _, err := conn.Exec(`UPDATE search_projection_state
SET state='complete', recent_amplification_ppm=2160000, capacity_evidence_status='unavailable'`); err != nil {
		t.Fatalf("seed unavailable recent-index amplification: %v", err)
	}
	report, err := sqlite.NewReleaseGateEvaluator(sqlite.NewDatabase(dbPath, onDiskSQLiteMigrations(t))).
		Evaluate(ctx, time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	got := gateByID(report.Gates)["recent_index_amplification"]
	if got.Status != application.ReleaseGateStatusSkip {
		t.Fatalf("recent_index = %+v, want skip when evidence is unavailable", got)
	}
}

func TestReleaseGateEvaluator_MeasurementsDoNotFail(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "measure.db")
	fx := newReleaseGateFixture(t, dbPath)
	fx.savePrompt(ctx, "p1", "sess", uniqueBody("measure", 2<<20))
	fx.insertSession("sess", "claude")
	fx.refine(ctx, "sess", "p1", "We needed a fixture so measurements stay published even when they exceed the old byte rows.", false)

	report, err := sqlite.NewReleaseGateEvaluator(sqlite.NewDatabase(dbPath, onDiskSQLiteMigrations(t))).
		Evaluate(ctx, time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if !report.Passed {
		t.Fatalf("large prompt bodies must not fail the release via measurements; gates = %+v", report.Gates)
	}
	found := false
	for _, m := range report.Measurements {
		if m.ID == "prompt_record_per_turn" && m.ObservedBytesPerUnit > 12*1024 {
			found = true
		}
	}
	if !found {
		t.Fatalf("want prompt_record_per_turn above the published 12 KiB illustration, got %+v", report.Measurements)
	}
}

type releaseGateFixture struct {
	t      *testing.T
	path   string
	events *sqlite.EventDatasource
}

func newReleaseGateFixture(t *testing.T, dbPath string) *releaseGateFixture {
	t.Helper()
	events, store := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	return &releaseGateFixture{t: t, path: dbPath, events: events}
}

func (fx *releaseGateFixture) savePrompt(ctx context.Context, id, session, body string) {
	fx.t.Helper()
	fx.saveKind(ctx, id, session, types.EventKindPrompt, body)
}

func (fx *releaseGateFixture) saveTranscript(ctx context.Context, id, session, body string) {
	fx.t.Helper()
	fx.saveKind(ctx, id, session, types.EventKindTranscript, body)
}

func (fx *releaseGateFixture) saveKind(ctx context.Context, id, session string, kind types.EventKind, body string) {
	fx.t.Helper()
	event := model.EventOf(
		types.EventID(id),
		kind,
		types.Client("cli"),
		types.Agent("codex"),
		types.SessionID(session),
		types.Workspace("ws"),
		body,
		time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
	)
	if err := fx.events.Save(ctx, event); err != nil {
		fx.t.Fatalf("Save(%s) error = %v", id, err)
	}
}

func (fx *releaseGateFixture) insertSession(id, client string) {
	fx.t.Helper()
	conn := openCallSiteDB(fx.t, fx.path)
	if _, err := conn.Exec(`INSERT INTO sessions(session_id, started_at, ended_at, client, agent, workspace)
VALUES (?, '2026-08-01T00:00:00Z', '2026-08-02T00:00:00Z', ?, 'codex', 'ws')`, id, client); err != nil {
		fx.t.Fatalf("insert session %s: %v", id, err)
	}
}

func (fx *releaseGateFixture) refine(ctx context.Context, session, eventID, summary string, degraded bool) {
	fx.t.Helper()
	refinements := sqlite.NewSessionRefinementDatasource(sqlite.NewDatabase(fx.path, onDiskSQLiteMigrations(fx.t)))
	row, err := model.NewSessionRefinement(
		types.SessionID(session), 1,
		types.EventID(eventID), types.EventID(eventID),
		summary, "", "test",
		time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC),
		degraded,
	)
	if err != nil {
		fx.t.Fatalf("NewSessionRefinement(%s) error = %v", session, err)
	}
	if _, err := refinements.SaveIfAdvances(ctx, row, 0); err != nil {
		fx.t.Fatalf("SaveIfAdvances(%s) error = %v", session, err)
	}
}

func uniqueBody(label string, n int) string {
	if n < len(label)+8 {
		n = len(label) + 8
	}
	out := make([]byte, n)
	copy(out, label)
	for i := len(label); i < n; i++ {
		out[i] = byte((i*131 + len(label)*17 + int(label[i%len(label)])) % 251)
		if out[i] < 32 {
			out[i] += 32
		}
	}
	return string(out)
}

func gateByID(gates []apptypes.ReleaseGateResult) map[string]apptypes.ReleaseGateResult {
	out := make(map[string]apptypes.ReleaseGateResult, len(gates))
	for _, gate := range gates {
		out[gate.ID] = gate
	}
	return out
}

func assertGateMiss(t *testing.T, dbPath string, now time.Time, id string) {
	t.Helper()
	report, err := sqlite.NewReleaseGateEvaluator(sqlite.NewDatabase(dbPath, onDiskSQLiteMigrations(t))).
		Evaluate(context.Background(), now)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if report.Passed {
		t.Fatalf("passed = true, want miss on %s; gates = %+v", id, report.Gates)
	}
	got := gateByID(report.Gates)[id]
	if got.Status != application.ReleaseGateStatusMiss {
		t.Fatalf("%s status = %q (%+v), want miss", id, got.Status, got)
	}
}
