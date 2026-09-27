package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/sqlite"
)

func supervisorFixture(t *testing.T) (*sqlite.Database, *sqlite.SessionDatasource, *sqlite.EventDatasource) {
	t.Helper()
	db := sqlite.NewDatabase(filepath.Join(t.TempDir(), "traceary.db"), onDiskSQLiteMigrations(t))
	if err := sqlite.NewStoreManagementDatasource(db).Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	return db, sqlite.NewSessionDatasource(db), sqlite.NewEventDatasource(db)
}
func supervisorSession(t *testing.T, sid types.SessionID, at time.Time) *model.Session {
	t.Helper()
	s, err := model.NewSessionWithRuntimeMode(sid, at, "cli", "codex", "workspace", types.RuntimeModeOneShot)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func ownershipBoundary(s *model.Session, id string, kind types.EventKind, at time.Time) *model.Event {
	return model.EventOf(types.EventID(id), kind, s.Client(), s.Agent(), s.SessionID(), s.Workspace(), id, at)
}
func TestSupervisorStoredModeGuardAndLaterLogs(t *testing.T) {
	_, sessions, events := supervisorFixture(t)
	ctx := context.Background()
	at := time.Now().Add(-time.Hour)
	s := supervisorSession(t, "owned", at)
	if err := sessions.SaveBoundary(ctx, s, ownershipBoundary(s, "start", types.EventKindSessionStarted, at)); err != nil {
		t.Fatal(err)
	}
	before, err := sessions.FindByID(ctx, s.SessionID())
	if err != nil {
		t.Fatal(err)
	}
	forged := model.NewSession(s.SessionID(), at, "cli", "codex", "workspace")
	if err := forged.End(at.Add(time.Minute), "forged"); err != nil {
		t.Fatal(err)
	}
	if err := sessions.SaveBoundary(ctx, forged, ownershipBoundary(forged, "ordinary", types.EventKindSessionEnded, at.Add(time.Minute))); !errors.Is(err, model.ErrSupervisorOwnedSession) {
		t.Fatalf("forged mode write = %v", err)
	}
	after, err := sessions.FindByID(ctx, s.SessionID())
	if err != nil {
		t.Fatal(err)
	}
	beforeRow, _ := before.Value()
	afterRow, _ := after.Value()
	if !reflect.DeepEqual(beforeRow, afterRow) {
		t.Fatal("refused writer reservation changed session fields")
	}
	priorEvents, err := events.ListRecent(ctx, 10, 0, "", "", "", s.SessionID(), "", false, time.Time{}, time.Time{}, "")
	if err != nil || len(priorEvents) != 1 {
		t.Fatalf("refused write changed events: %d/%v", len(priorEvents), err)
	}
	if _, err := s.FinalizeOneShot(at.Add(2*time.Minute), types.TerminalReasonFailure, ""); err != nil {
		t.Fatal(err)
	}
	final := ownershipBoundary(s, "final", types.EventKindSessionEnded, at.Add(2*time.Minute))
	final.SetSourceHook("session_end")
	final.SetRawWorkspace("workspace")
	evidence, err := model.NewHookDeliveryEvidence(final, "native-final-delivery", "workspace")
	if err != nil {
		t.Fatal(err)
	}
	final.SetDeliveryEvidence(evidence)

	if err := sessions.SaveOneShotBoundary(ctx, s, final); err != nil {
		t.Fatal(err)
	}
	contradictory := supervisorSession(t, "owned", at)
	if _, err := contradictory.FinalizeOneShot(at.Add(4*time.Minute), types.TerminalReasonSuccess, ""); err != nil {
		t.Fatal(err)
	}
	if err := sessions.SaveOneShotBoundary(ctx, contradictory, ownershipBoundary(contradictory, "final", types.EventKindSessionEnded, at.Add(4*time.Minute))); !errors.Is(err, model.ErrConflictingTerminalState) {
		t.Fatalf("duplicate event disguised contradictory result: %v", err)
	}
	// A new physical event ID with the same native delivery/fingerprints
	// exercises receipt idempotency, not merely an event-ID collision.
	replay := model.EventOfWithSourceHook("final-replay", final.Kind(), final.Client(), final.Agent(), final.SessionID(), final.Workspace(), final.Body(), final.CreatedAt(), final.SourceHook())
	replay.SetRawWorkspace("workspace")
	replayEvidence, err := model.NewHookDeliveryEvidence(replay, "native-final-delivery", "workspace")
	if err != nil {
		t.Fatal(err)
	}
	replay.SetDeliveryEvidence(replayEvidence)
	if err := sessions.SaveBoundary(ctx, s, replay); !errors.Is(err, model.ErrSupervisorOwnedSession) {
		t.Fatalf("ordinary stable redelivery = %v", err)
	}
	// The same receipt actually short-circuits on the authorized port, proving
	// ownership validation, rather than missing dedup evidence, refused End.
	if err := sessions.SaveOneShotBoundary(ctx, s, replay); err != nil {
		t.Fatal(err)
	}
	if replay.PersistInserted() || replay.EventID() != final.EventID() {
		t.Fatal("stable receipt did not preserve the original boundary")
	}

	later := ownershipBoundary(s, "later-log", types.EventKindCommandExecuted, at.Add(3*time.Minute))
	if err := events.Save(ctx, later); err != nil {
		t.Fatalf("later log refused: %v", err)
	}
	stored, err := sessions.FindByID(ctx, s.SessionID())
	if err != nil {
		t.Fatal(err)
	}
	got, _ := stored.Value()
	reason, _ := got.TerminalReason().Value()
	ended, _ := got.EndedAt().Value()
	if reason != types.TerminalReasonFailure || !ended.Equal(at.Add(2*time.Minute)) {
		t.Fatal("supervisor result changed")
	}
	boundaries, err := events.ListRecent(ctx, 10, 0, types.EventKindSessionEnded, "", "", s.SessionID(), "", false, time.Time{}, time.Time{}, "")
	if err != nil || len(boundaries) != 1 {
		t.Fatalf("boundary count = %d/%v", len(boundaries), err)
	}
}
func TestSupervisorGCAndParentCascadeCannotFinalizeOneShot(t *testing.T) {
	db, sessions, _ := supervisorFixture(t)
	ctx := context.Background()
	at := time.Now().Add(-48 * time.Hour)
	parent := model.NewSession("parent", at, "cli", "codex", "workspace")
	if err := sessions.SaveBoundary(ctx, parent, ownershipBoundary(parent, "parent-start", types.EventKindSessionStarted, at)); err != nil {
		t.Fatal(err)
	}
	child, err := model.NewSessionWithRuntimeModeAndParent("child", at, "cli", "codex", "workspace", types.RuntimeModeOneShot, "parent")
	if err != nil {
		t.Fatal(err)
	}
	if err := sessions.SaveBoundary(ctx, child, ownershipBoundary(child, "child-start", types.EventKindSessionStarted, at)); err != nil {
		t.Fatal(err)
	}
	sut := usecase.NewSessionUsecase(nil, sessions, nil, nil)
	if _, err := sut.End(ctx, "cli", "codex", "parent", "workspace", ""); err != nil {
		t.Fatal(err)
	}
	for _, dry := range []bool{true, false} {
		count, err := sqlite.NewStoreManagementDatasource(db).CloseStaleSessions(ctx, time.Hour, dry, nil)
		if err != nil || count != 0 {
			t.Fatalf("GC %v = %d/%v", dry, count, err)
		}
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := sqlite.NewStoreManagementDatasource(db).CloseStaleSessions(ctx, time.Hour, false, nil)
		errs <- err
	}()
	go func() {
		defer wg.Done()
		_, _, err := sut.FinalizeOneShot(ctx, "cli", "codex", "child", "workspace", types.TerminalReasonTimeout, "")
		errs <- err
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	stored, err := sessions.FindByID(ctx, "child")
	if err != nil {
		t.Fatal(err)
	}
	s, _ := stored.Value()
	reason, _ := s.TerminalReason().Value()
	if reason != types.TerminalReasonTimeout {
		t.Fatal("ordinary writer won")
	}
}
func TestSupervisorImportProtectsExistingBindingAndOutcome(t *testing.T) {
	for _, ended := range []bool{false, true} {
		t.Run(map[bool]string{false: "open", true: "ended"}[ended], func(t *testing.T) {
			db, sessions, events := supervisorFixture(t)
			ctx := context.Background()
			at := time.Now().Add(-time.Hour)
			parent := model.NewSession("binding-parent", at, "cli", "codex", "workspace")
			if err := sessions.SaveBoundary(ctx, parent, ownershipBoundary(parent, "binding-parent-start", types.EventKindSessionStarted, at)); err != nil {
				t.Fatal(err)
			}
			s := supervisorSession(t, "owned", at)
			if err := sessions.SaveBoundary(ctx, s, ownershipBoundary(s, "start", types.EventKindSessionStarted, at)); err != nil {
				t.Fatal(err)
			}
			if ended {
				if _, err := s.FinalizeOneShot(at.Add(time.Minute), types.TerminalReasonSuccess, ""); err != nil {
					t.Fatal(err)
				}
				if err := sessions.SaveOneShotBoundary(ctx, s, ownershipBoundary(s, "end", types.EventKindSessionEnded, at.Add(time.Minute))); err != nil {
					t.Fatal(err)
				}
			}
			snapshot := model.SessionSnapshot{SessionID: s.SessionID(), StartedAt: s.StartedAt(), EndedAt: s.EndedAt(), Client: s.Client(), Agent: s.Agent(), Workspace: s.Workspace(), RuntimeMode: s.RuntimeMode(), TerminalReason: s.TerminalReason()}
			mutations := map[string]func(*model.SessionSnapshot){
				"parent": func(s *model.SessionSnapshot) { s.ParentSessionID = "binding-parent" },
				"mode":   func(s *model.SessionSnapshot) { s.RuntimeMode = types.RuntimeModeInteractive },
				"client": func(s *model.SessionSnapshot) { s.Client = "hook" }, "agent": func(s *model.SessionSnapshot) { s.Agent = "claude" }, "workspace": func(s *model.SessionSnapshot) { s.Workspace = "other" },
				"started": func(s *model.SessionSnapshot) { s.StartedAt = s.StartedAt.Add(-time.Minute) },
				"spawn":   func(s *model.SessionSnapshot) { s.SpawnEventID = "spawn" }, "kind": func(s *model.SessionSnapshot) { s.SubagentKind = "worker" }, "order": func(s *model.SessionSnapshot) { s.SpawnOrder = types.Some(1) },
				"result": func(s *model.SessionSnapshot) {
					s.EndedAt = types.Some(at.Add(2 * time.Minute))
					s.TerminalReason = types.Some(types.TerminalReasonFailure)
				},
			}
			if ended {
				mutations["reopen"] = func(s *model.SessionSnapshot) {
					s.EndedAt = types.None[time.Time]()
					s.TerminalReason = types.None[types.TerminalReason]()
				}
			}
			for name, mutate := range mutations {
				t.Run(name, func(t *testing.T) {
					proposedState := snapshot
					mutate(&proposedState)
					proposed, err := model.SessionFromSnapshot(proposedState)
					if err != nil {
						t.Fatal(err)
					}
					tx, err := sqlite.NewBundleDatasource(db, events).BeginBundleImport(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer func() { _ = tx.Rollback(ctx) }()
					if _, err := tx.ImportSession(ctx, proposed, usecase.BundleConflictReplace, usecase.BundleMissingParentReject); !errors.Is(err, model.ErrConflictingTerminalState) {
						t.Fatalf("import override = %v", err)
					}
				})
			}
			exact, err := model.SessionFromSnapshot(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			exact.SetLabel("permitted label")
			tx, err := sqlite.NewBundleDatasource(db, events).BeginBundleImport(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tx.ImportSession(ctx, exact, usecase.BundleConflictReplace, usecase.BundleMissingParentReject); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestSupervisorAbsentLegacyRestoreDoesNotAuthenticateOldSuccess(t *testing.T) {
	db, sessions, events := supervisorFixture(t)
	ctx := context.Background()
	at := time.Now().Add(-time.Hour)
	legacy, err := model.SessionFromSnapshot(model.SessionSnapshot{SessionID: "legacy", StartedAt: at, EndedAt: types.Some(at.Add(time.Minute)), Client: "cli", Agent: "codex", Workspace: "workspace", RuntimeMode: types.RuntimeModeOneShot, TerminalReason: types.Some(types.TerminalReasonSuccess)})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := sqlite.NewBundleDatasource(db, events).BeginBundleImport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ImportSession(ctx, legacy, usecase.BundleConflictReplace, usecase.BundleMissingParentReject); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	sut := usecase.NewSessionUsecase(nil, sessions, nil, nil)
	if _, _, err := sut.FinalizeOneShot(ctx, "cli", "codex", "legacy", "workspace", types.TerminalReasonFailure, ""); !errors.Is(err, model.ErrConflictingTerminalState) {
		t.Fatalf("old success overwritten = %v", err)
	}
	stored, err := sessions.FindByID(ctx, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	s, _ := stored.Value()
	reason, _ := s.TerminalReason().Value()
	if reason != types.TerminalReasonSuccess {
		t.Fatal("recorded legacy reason changed")
	}
}

func TestSupervisorFinalizationPreservesHumanRefinement(t *testing.T) {
	db, sessions, events := supervisorFixture(t)
	ctx := context.Background()
	at := time.Now().Add(-time.Hour)
	s := supervisorSession(t, "refined", at)
	start := ownershipBoundary(s, "start", types.EventKindSessionStarted, at)
	if err := sessions.SaveBoundary(ctx, s, start); err != nil {
		t.Fatal(err)
	}
	repo := sqlite.NewSessionRefinementDatasource(db)
	refiner := usecase.NewSessionRefinementUsecase(sessions, repo, events, nil)
	if _, err := refiner.Refine(ctx, usecase.SessionRefineInput{SessionID: s.SessionID(), Summary: "human work", Keywords: "human", ProducedBy: "agent", CoversTo: start.EventID()}); err != nil {
		t.Fatal(err)
	}
	before, err := repo.FindBySessionID(ctx, s.SessionID())
	if err != nil {
		t.Fatal(err)
	}
	sut := usecase.NewSessionUsecase(nil, sessions, nil, nil, usecase.SessionUsecaseDependencies{Refinement: refiner})
	if _, _, err := sut.FinalizeOneShot(ctx, "cli", "codex", s.SessionID(), "workspace", types.TerminalReasonSuccess, "one-shot process finished: success"); err != nil {
		t.Fatal(err)
	}
	after, err := repo.FindBySessionID(ctx, s.SessionID())
	if err != nil {
		t.Fatal(err)
	}
	a, _ := before.Value()
	b, _ := after.Value()
	if a.Summary() != b.Summary() || a.Generation() != b.Generation() || a.CoversToEventID() != b.CoversToEventID() || a.CoversFromEventID() != b.CoversFromEventID() || a.ProducedBy() != b.ProducedBy() {
		t.Fatal("automatic finalization changed human refinement")
	}
}

func TestSupervisorImportRacesFinalizationWithoutOverwritingOutcome(t *testing.T) {
	db, sessions, events := supervisorFixture(t)
	ctx := context.Background()
	at := time.Now().Add(-time.Hour)
	s := supervisorSession(t, "race-import", at)
	if err := sessions.SaveBoundary(ctx, s, ownershipBoundary(s, "start", types.EventKindSessionStarted, at)); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	imports := make(chan error, 1)
	finals := make(chan error, 1)
	go func() {
		<-start
		tx, err := sqlite.NewBundleDatasource(db, events).BeginBundleImport(ctx)
		if err != nil {
			imports <- err
			return
		}
		defer func() { _ = tx.Rollback(ctx) }()
		_, err = tx.ImportSession(ctx, s, usecase.BundleConflictReplace, usecase.BundleMissingParentReject)
		if err == nil {
			err = tx.Commit(ctx)
		}
		imports <- err
	}()
	go func() {
		<-start
		sut := usecase.NewSessionUsecase(nil, sessions, nil, nil)
		_, _, err := sut.FinalizeOneShot(ctx, "cli", "codex", s.SessionID(), "workspace", types.TerminalReasonFailure, "")
		finals <- err
	}()
	close(start)
	if err := <-finals; err != nil {
		t.Fatal(err)
	}
	if err := <-imports; err != nil && !errors.Is(err, model.ErrConflictingTerminalState) {
		var coded interface{ Code() int }
		if !errors.As(err, &coded) || coded.Code()&0xff != 5 {
			t.Fatal(err)
		}
	}
	stored, err := sessions.FindByID(ctx, s.SessionID())
	if err != nil {
		t.Fatal(err)
	}
	got, _ := stored.Value()
	reason, _ := got.TerminalReason().Value()
	if reason != types.TerminalReasonFailure {
		t.Fatal("import overwrote finalization")
	}
}

func TestSupervisorFinalizationDoesNotCreateRefinement(t *testing.T) {
	db, sessions, events := supervisorFixture(t)
	ctx := context.Background()
	at := time.Now().Add(-time.Hour)
	s := supervisorSession(t, "no-refinement", at)
	if err := sessions.SaveBoundary(ctx, s, ownershipBoundary(s, "start", types.EventKindSessionStarted, at)); err != nil {
		t.Fatal(err)
	}
	repo := sqlite.NewSessionRefinementDatasource(db)
	refiner := usecase.NewSessionRefinementUsecase(sessions, repo, events, nil)
	sut := usecase.NewSessionUsecase(nil, sessions, nil, nil, usecase.SessionUsecaseDependencies{Refinement: refiner})
	if _, _, err := sut.FinalizeOneShot(ctx, "cli", "codex", s.SessionID(), "workspace", types.TerminalReasonSuccess, "one-shot process finished: success"); err != nil {
		t.Fatal(err)
	}
	row, err := repo.FindBySessionID(ctx, s.SessionID())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := row.Value(); ok {
		t.Fatal("automatic completion refinement created")
	}
}

func TestSupervisorImportCannotPromoteExistingOrdinarySID(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		t.Run(map[bool]string{false: "open", true: "terminal"}[terminal], func(t *testing.T) {
			db, sessions, events := supervisorFixture(t)
			ctx := context.Background()
			at := time.Now().Add(-time.Hour)
			ordinary := model.NewSession("ordinary", at, "cli", "codex", "workspace")
			if err := sessions.SaveBoundary(ctx, ordinary, ownershipBoundary(ordinary, "ordinary-start", types.EventKindSessionStarted, at)); err != nil {
				t.Fatal(err)
			}
			before, err := sessions.FindByID(ctx, ordinary.SessionID())
			if err != nil {
				t.Fatal(err)
			}
			proposed := supervisorSession(t, ordinary.SessionID(), at)
			if terminal {
				if _, err := proposed.FinalizeOneShot(at.Add(time.Minute), types.TerminalReasonSuccess, ""); err != nil {
					t.Fatal(err)
				}
			}
			tx, err := sqlite.NewBundleDatasource(db, events).BeginBundleImport(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			unrelated := supervisorSession(t, "rolled-back-legacy", at)
			if _, err := tx.ImportSession(ctx, unrelated, usecase.BundleConflictReplace, usecase.BundleMissingParentReject); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.ImportSession(ctx, proposed, usecase.BundleConflictReplace, usecase.BundleMissingParentReject); !errors.Is(err, model.ErrConflictingTerminalState) {
				t.Fatalf("ordinary SID promotion = %v", err)
			}
			if err := tx.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			after, err := sessions.FindByID(ctx, ordinary.SessionID())
			if err != nil {
				t.Fatal(err)
			}
			a, _ := before.Value()
			b, _ := after.Value()
			if !reflect.DeepEqual(a, b) {
				t.Fatal("import reassigned ordinary binding")
			}
			rolledBack, err := sessions.FindByID(ctx, "rolled-back-legacy")
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := rolledBack.Value(); ok {
				t.Fatal("failed import left preceding rows committed")
			}
		})
	}
}

func TestSupervisorBackfilledParentCannotBePromotedByLaterImport(t *testing.T) {
	db, sessions, events := supervisorFixture(t)
	ctx := context.Background()
	at := time.Now().Add(-time.Hour)
	child, err := model.NewSessionWithRuntimeModeAndParent("child", at.Add(time.Minute), "cli", "codex", "workspace", types.RuntimeModeInteractive, "parent")
	if err != nil {
		t.Fatal(err)
	}
	bundles := sqlite.NewBundleDatasource(db, events)
	first, err := bundles.BeginBundleImport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Rollback(ctx) }()
	if _, err := first.ImportSession(ctx, child, usecase.BundleConflictReplace, usecase.BundleMissingParentBackfill); err != nil {
		t.Fatal(err)
	}
	if err := first.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	before, err := sessions.FindByID(ctx, "parent")
	if err != nil {
		t.Fatal(err)
	}
	placeholder, ok := before.Value()
	if !ok || placeholder.RuntimeMode() != types.RuntimeModeInteractive || placeholder.Label() != "traceary:bundle-backfilled-parent" {
		t.Fatal("expected ordinary backfill placeholder")
	}
	realParent := supervisorSession(t, "parent", at)
	later, err := bundles.BeginBundleImport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = later.Rollback(ctx) }()
	if _, err := later.ImportSession(ctx, supervisorSession(t, "rollback-only", at), usecase.BundleConflictReplace, usecase.BundleMissingParentReject); err != nil {
		t.Fatal(err)
	}
	if _, err := later.ImportSession(ctx, realParent, usecase.BundleConflictReplace, usecase.BundleMissingParentReject); !errors.Is(err, model.ErrConflictingTerminalState) {
		t.Fatalf("backfilled parent promotion = %v", err)
	}
	if err := later.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := sessions.FindByID(ctx, "parent")
	if err != nil {
		t.Fatal(err)
	}
	preserved, _ := after.Value()
	if !reflect.DeepEqual(placeholder, preserved) {
		t.Fatal("placeholder label bypassed stored mode ownership")
	}
	restoredChild, err := sessions.FindByID(ctx, "child")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := restoredChild.Value(); !ok {
		t.Fatal("prior child import changed")
	}
	rejected, err := sessions.FindByID(ctx, "rollback-only")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rejected.Value(); ok {
		t.Fatal("later failed import partially committed")
	}
}

func TestSupervisorSameBundleChildBeforeOneShotAncestorFailsAtomically(t *testing.T) {
	source, sourceSessions, sourceEvents := supervisorFixture(t)
	ctx := context.Background()
	at := time.Now().Add(-time.Hour)
	root := model.NewSession("z-root", at, "cli", "codex", "workspace")
	if err := sourceSessions.SaveBoundary(ctx, root, ownershipBoundary(root, "root-start", types.EventKindSessionStarted, at)); err != nil {
		t.Fatal(err)
	}
	parent, err := model.NewSessionWithRuntimeModeAndParent("a-parent", at.Add(time.Minute), "cli", "codex", "workspace", types.RuntimeModeOneShot, root.SessionID())
	if err != nil {
		t.Fatal(err)
	}
	if err := sourceSessions.SaveBoundary(ctx, parent, ownershipBoundary(parent, "parent-start", types.EventKindSessionStarted, parent.StartedAt())); err != nil {
		t.Fatal(err)
	}
	child, err := model.NewSessionWithRuntimeModeAndParent("child", at.Add(2*time.Minute), "cli", "codex", "workspace", types.RuntimeModeInteractive, parent.SessionID())
	if err != nil {
		t.Fatal(err)
	}
	if err := sourceSessions.SaveBoundary(ctx, child, ownershipBoundary(child, "child-start", types.EventKindSessionStarted, child.StartedAt())); err != nil {
		t.Fatal(err)
	}
	// Root is first, but lexical parent IDs put child's a-parent reference
	// before the real a-parent row's z-root reference. This is intentionally
	// a regression for the current sort, not a topological sorting redesign.
	bundlePath := filepath.Join(t.TempDir(), "same-bundle.tbun")
	exporter := usecase.NewBundleUsecase(sourceEvents, sqlite.NewBundleDatasource(source, sourceEvents), nil)
	if err := exporter.Export(ctx, usecase.BundleExportOptions{OutPath: bundlePath, Passphrase: []byte("synthetic-test-passphrase")}); err != nil {
		t.Fatal(err)
	}
	target, targetSessions, targetEvents := supervisorFixture(t)
	importer := usecase.NewBundleUsecase(targetEvents, sqlite.NewBundleDatasource(target, targetEvents), nil)
	_, err = importer.Import(ctx, usecase.BundleImportOptions{InPath: bundlePath, Passphrase: []byte("synthetic-test-passphrase"), OnConflict: usecase.BundleConflictReplace, MissingParent: usecase.BundleMissingParentBackfill})
	if !errors.Is(err, model.ErrConflictingTerminalState) {
		t.Fatalf("same-bundle promotion = %v", err)
	}
	for _, id := range []types.SessionID{"z-root", "a-parent", "child"} {
		row, err := targetSessions.FindByID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := row.Value(); ok {
			t.Fatalf("failed bundle left session %s committed", id)
		}
	}
	rows, err := targetEvents.ListRecent(ctx, 10, 0, "", "", "", "", "", false, time.Time{}, time.Time{}, "")
	if err != nil || len(rows) != 0 {
		t.Fatalf("failed bundle left events committed: %d/%v", len(rows), err)
	}
	// The documented recovery sequence works without deleting or relabeling
	// any row: restore the actual ancestors in this isolated target first.
	ancestors, err := sqlite.NewBundleDatasource(target, targetEvents).BeginBundleImport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ancestors.Rollback(ctx) }()
	for _, session := range []*model.Session{root, parent} {
		if _, err := ancestors.ImportSession(ctx, session, usecase.BundleConflictReplace, usecase.BundleMissingParentReject); err != nil {
			t.Fatal(err)
		}
	}
	if err := ancestors.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := importer.Import(ctx, usecase.BundleImportOptions{InPath: bundlePath, Passphrase: []byte("synthetic-test-passphrase"), OnConflict: usecase.BundleConflictReplace, MissingParent: usecase.BundleMissingParentBackfill}); err != nil {
		t.Fatalf("ancestor-first recovery = %v", err)
	}
	for _, id := range []types.SessionID{"z-root", "a-parent", "child"} {
		row, err := targetSessions.FindByID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		restored, ok := row.Value()
		if !ok {
			t.Fatalf("recovery missing %s", id)
		}
		if id == "a-parent" && restored.RuntimeMode() != types.RuntimeModeOneShot {
			t.Fatal("recovery lost one-shot parent binding")
		}
	}

}
