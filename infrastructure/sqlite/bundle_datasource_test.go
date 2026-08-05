package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestBundleDatasource_ImportEventReceivesArchiveSequence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "traceary.db")
	database := sqlite.NewDatabase(path, onDiskSQLiteMigrations(t))
	if err := sqlite.NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	tx, err := sqlite.NewBundleDatasource(database, sqlite.NewEventDatasource(database)).BeginBundleImport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	event := model.EventOf(types.EventID("bundle-sequenced"), types.EventKindNote, types.Client("cli"), types.Agent("codex"), types.SessionID("s"), types.Workspace("w"), "body", time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC))
	if imported, importErr := tx.ImportEvent(ctx, event, usecase.BundleConflictSkip); importErr != nil || !imported {
		_ = tx.Rollback(ctx)
		t.Fatalf("ImportEvent() = %v/%v", imported, importErr)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	var sequence int64
	if err = raw.QueryRow(`SELECT sequence FROM archive_event_sequences WHERE event_id='bundle-sequenced'`).Scan(&sequence); err != nil || sequence != 1 {
		t.Fatalf("bundle archive sequence = %d, err=%v", sequence, err)
	}
}

func TestBundleDatasource_ImportSessionRejectsConflictingTerminalReplace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := sqlite.NewDatabase(filepath.Join(t.TempDir(), "traceary.db"), onDiskSQLiteMigrations(t))
	if err := sqlite.NewStoreManagementDatasource(db).Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	bundles := sqlite.NewBundleDatasource(db, sqlite.NewEventDatasource(db))
	agent, _ := types.AgentFrom("codex")
	startedAt := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	newTerminal := func(reason types.TerminalReason, summary string) *model.Session {
		session, err := model.NewSessionWithRuntimeMode(types.SessionID("bundle-terminal"), startedAt, types.Client("hook"), agent, types.Workspace("workspace"), types.RuntimeModeOneShot)
		if err != nil {
			t.Fatalf("NewSessionWithRuntimeMode() error = %v", err)
		}
		if _, err := session.Terminate(startedAt.Add(time.Minute), reason, summary); err != nil {
			t.Fatalf("Terminate() error = %v", err)
		}
		return session
	}

	firstTx, err := bundles.BeginBundleImport(ctx)
	if err != nil {
		t.Fatalf("BeginBundleImport(first) error = %v", err)
	}
	if imported, err := firstTx.ImportSession(ctx, newTerminal(types.TerminalReasonSuccess, "first"), usecase.BundleConflictReplace, usecase.BundleMissingParentReject); err != nil || !imported {
		t.Fatalf("ImportSession(first) = %v/%v", imported, err)
	}
	if err := firstTx.Commit(ctx); err != nil {
		t.Fatalf("Commit(first) error = %v", err)
	}
	idempotentTx, err := bundles.BeginBundleImport(ctx)
	if err != nil {
		t.Fatalf("BeginBundleImport(idempotent) error = %v", err)
	}
	if imported, err := idempotentTx.ImportSession(ctx, newTerminal(types.TerminalReasonSuccess, "redelivery"), usecase.BundleConflictReplace, usecase.BundleMissingParentReject); err != nil || !imported {
		_ = idempotentTx.Rollback(ctx)
		t.Fatalf("ImportSession(idempotent) = %v/%v", imported, err)
	}
	if err := idempotentTx.Commit(ctx); err != nil {
		t.Fatalf("Commit(idempotent) error = %v", err)
	}

	modeConflict, err := model.NewSessionWithRuntimeMode(types.SessionID("bundle-terminal"), startedAt, types.Client("hook"), agent, types.Workspace("workspace"), types.RuntimeModeInteractive)
	if err != nil {
		t.Fatalf("NewSessionWithRuntimeMode(mode conflict) error = %v", err)
	}
	if _, err := modeConflict.Terminate(startedAt.Add(time.Minute), types.TerminalReasonSuccess, "mode conflict"); err != nil {
		t.Fatalf("Terminate(mode conflict) error = %v", err)
	}
	modeConflictTx, err := bundles.BeginBundleImport(ctx)
	if err != nil {
		t.Fatalf("BeginBundleImport(mode conflict) error = %v", err)
	}
	_, err = modeConflictTx.ImportSession(ctx, modeConflict, usecase.BundleConflictReplace, usecase.BundleMissingParentReject)
	if err == nil || !errors.Is(err, model.ErrConflictingTerminalState) {
		_ = modeConflictTx.Rollback(ctx)
		t.Fatalf("ImportSession(mode conflict) error = %v, want ErrConflictingTerminalState", err)
	}
	if !strings.Contains(err.Error(), `mode="one_shot"`) || !strings.Contains(err.Error(), `mode="interactive"`) {
		_ = modeConflictTx.Rollback(ctx)
		t.Fatalf("ImportSession(mode conflict) error lacks diagnostic modes: %v", err)
	}
	if err := modeConflictTx.Rollback(ctx); err != nil {
		t.Fatalf("Rollback(mode conflict) error = %v", err)
	}

	conflictTx, err := bundles.BeginBundleImport(ctx)
	if err != nil {
		t.Fatalf("BeginBundleImport(conflict) error = %v", err)
	}
	_, err = conflictTx.ImportSession(ctx, newTerminal(types.TerminalReasonFailure, "conflict"), usecase.BundleConflictReplace, usecase.BundleMissingParentReject)
	if err == nil || !errors.Is(err, model.ErrConflictingTerminalState) {
		_ = conflictTx.Rollback(ctx)
		t.Fatalf("ImportSession(conflict) error = %v, want ErrConflictingTerminalState", err)
	}
	if !strings.Contains(err.Error(), `"success"`) || !strings.Contains(err.Error(), `"failure"`) {
		_ = conflictTx.Rollback(ctx)
		t.Fatalf("ImportSession(conflict) error lacks diagnostic reasons: %v", err)
	}
	if err := conflictTx.Rollback(ctx); err != nil {
		t.Fatalf("Rollback(conflict) error = %v", err)
	}

	stored, err := sqlite.NewSessionDatasource(db).FindByID(ctx, types.SessionID("bundle-terminal"))
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	got, _ := stored.Value()
	if reason, ok := got.TerminalReason().Value(); !ok || reason != types.TerminalReasonSuccess || got.Summary() != "first" {
		t.Fatalf("stored terminal state = %q/%v summary=%q", reason, ok, got.Summary())
	}
}

func TestBundleDatasource_CommandAuditBeforeEventFailsFK(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	db := sqlite.NewDatabase(dbPath, onDiskSQLiteMigrations(t))
	store := sqlite.NewStoreManagementDatasource(db)
	if err := store.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	eventStore := sqlite.NewEventDatasource(db)
	sut := sqlite.NewBundleDatasource(db, eventStore)
	tx, err := sut.BeginBundleImport(context.Background())
	if err != nil {
		t.Fatalf("BeginBundleImport() error = %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	eventID, err := types.EventIDFrom("event-1")
	if err != nil {
		t.Fatalf("EventIDFrom() error = %v", err)
	}
	audit, err := model.NewCommandAudit(eventID, "go test ./...", "", "", false, false)
	if err != nil {
		t.Fatalf("NewCommandAudit() error = %v", err)
	}

	_, err = tx.ImportCommandAudit(context.Background(), audit, usecase.BundleConflictSkip)
	if err == nil {
		t.Fatalf("ImportCommandAudit() succeeded before event import, want FK error")
	}
	if !strings.Contains(err.Error(), "event not found") && !strings.Contains(err.Error(), "FOREIGN KEY") {
		t.Fatalf("ImportCommandAudit() error = %v, want FK/event-not-found error", err)
	}
}

func TestBundleDatasource_ImportSessionBackfillsMissingParent(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	db := sqlite.NewDatabase(dbPath, onDiskSQLiteMigrations(t))
	store := sqlite.NewStoreManagementDatasource(db)
	if err := store.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	eventStore := sqlite.NewEventDatasource(db)
	sut := sqlite.NewBundleDatasource(db, eventStore)
	tx, err := sut.BeginBundleImport(context.Background())
	if err != nil {
		t.Fatalf("BeginBundleImport() error = %v", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.Background())
		}
	}()

	agent, err := types.AgentFrom("codex")
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	child := model.SessionOf(
		types.SessionID("child-session"),
		time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC),
		types.None[time.Time](),
		types.Client("cli"),
		agent,
		types.Workspace("ws"),
		"child",
		"",
		types.SessionID("missing-parent"),
	)
	imported, err := tx.ImportSession(context.Background(), child, usecase.BundleConflictSkip, usecase.BundleMissingParentBackfill)
	if err != nil {
		t.Fatalf("ImportSession() error = %v", err)
	}
	if !imported {
		t.Fatalf("ImportSession() imported = false, want true")
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	committed = true

	sessionStore := sqlite.NewSessionDatasource(db)
	stub, err := sessionStore.FindByID(context.Background(), types.SessionID("missing-parent"))
	if err != nil {
		t.Fatalf("FindByID(parent) error = %v", err)
	}
	parent, ok := stub.Value()
	if !ok {
		t.Fatalf("backfilled parent not found")
	}
	if got := parent.Label(); got != "traceary:bundle-backfilled-parent" {
		t.Fatalf("parent label = %q, want marker", got)
	}
}

func TestBundleDatasource_UsageObservationsPreserveSnapshotChainAndRejectReplace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := sqlite.NewDatabase(filepath.Join(t.TempDir(), "traceary.db"), onDiskSQLiteMigrations(t))
	if err := sqlite.NewStoreManagementDatasource(db).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	bundles := sqlite.NewBundleDatasource(db, sqlite.NewEventDatasource(db))
	source, err := types.UsageSourceOf("codex", "headless_stream", "0.31.0", "openai", "model-1")
	if err != nil {
		t.Fatal(err)
	}
	ts := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	makeSnapshot := func(id string, revision int64, predecessor string, inputTokens int64) *model.UsageObservation {
		observationID, err := types.UsageObservationIDFrom(id)
		if err != nil {
			t.Fatal(err)
		}
		supersedes := types.None[types.UsageObservationID]()
		if predecessor != "" {
			value, err := types.UsageObservationIDFrom(predecessor)
			if err != nil {
				t.Fatal(err)
			}
			supersedes = types.Some(value)
		}
		descriptor, err := model.NewUsageSnapshotDescriptor(
			observationID, types.SessionID("session-1"), source, "codex:session-1", revision, supersedes, ts.Add(time.Duration(revision)*time.Minute),
		)
		if err != nil {
			t.Fatal(err)
		}
		input, err := types.KnownUsageValue(inputTokens)
		if err != nil {
			t.Fatal(err)
		}
		unavailable := types.UnavailableUsageValue()
		counters, err := types.UsageCountersOf(input, unavailable, unavailable, unavailable, unavailable, input)
		if err != nil {
			t.Fatal(err)
		}
		observation, err := model.NewFinalizedUsageObservation(
			descriptor, counters, types.UnavailableUsageCost(), types.UsageTerminalSuccess,
			descriptor.ObservedAt().Add(time.Second),
		)
		if err != nil {
			t.Fatal(err)
		}
		return observation
	}
	root := makeSnapshot("usage-root", 1, "", 10)
	successor := makeSnapshot("usage-successor", 2, "usage-root", 20)
	tx, err := bundles.BeginBundleImport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, observation := range []*model.UsageObservation{root, successor} {
		imported, err := tx.ImportUsageObservation(ctx, observation, usecase.BundleConflictSkip)
		if err != nil || !imported {
			_ = tx.Rollback(ctx)
			t.Fatalf("ImportUsageObservation(%s) = %t/%v", observation.Descriptor().ObservationID(), imported, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	listed, err := bundles.ListBundleUsageObservations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 || listed[0].Descriptor().ObservationID().String() != "usage-root" || listed[1].Descriptor().ObservationID().String() != "usage-successor" {
		t.Fatalf("listed snapshot order = %#v", listed)
	}

	replayTx, err := bundles.BeginBundleImport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if imported, err := replayTx.ImportUsageObservation(ctx, successor, usecase.BundleConflictSkip); err != nil || imported {
		_ = replayTx.Rollback(ctx)
		t.Fatalf("exact replay = %t/%v, want skipped", imported, err)
	}
	if err := replayTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	conflictTx, err := bundles.BeginBundleImport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = conflictTx.ImportUsageObservation(ctx, makeSnapshot("usage-successor", 2, "usage-root", 99), usecase.BundleConflictReplace)
	if err == nil || !errors.Is(err, model.ErrConflictingUsageObservation) {
		_ = conflictTx.Rollback(ctx)
		t.Fatalf("conflicting replacement error = %v, want ErrConflictingUsageObservation", err)
	}
	if err := conflictTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	missingTx, err := bundles.BeginBundleImport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	missingID, _ := types.UsageObservationIDFrom("usage-missing-predecessor")
	missingPredecessorID, _ := types.UsageObservationIDFrom("not-in-bundle-or-store")
	missingDescriptor, err := model.NewUsageSnapshotDescriptor(
		missingID, types.SessionID("session-1"), source, "missing-series", 3,
		types.Some(missingPredecessorID), ts.Add(3*time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	missing, err := model.NewFinalizedUsageObservation(
		missingDescriptor, root.Counters(), types.UnavailableUsageCost(), types.UsageTerminalSuccess,
		missingDescriptor.ObservedAt().Add(time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	if imported, err := missingTx.ImportUsageObservation(ctx, missing, usecase.BundleConflictSkip); err == nil || imported {
		_ = missingTx.Rollback(ctx)
		t.Fatalf("missing predecessor import = %t/%v, want fail closed", imported, err)
	}
	if err := missingTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	missingWithHeadTx, err := bundles.BeginBundleImport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	missingWithHead := makeSnapshot("usage-missing-predecessor-with-head", 3, "absent-from-existing-series", 30)
	if imported, err := missingWithHeadTx.ImportUsageObservation(ctx, missingWithHead, usecase.BundleConflictSkip); err == nil || imported {
		_ = missingWithHeadTx.Rollback(ctx)
		t.Fatalf("missing predecessor with destination head import = %t/%v, want fail closed", imported, err)
	}
	if err := missingWithHeadTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	sameIDMissingTx, err := bundles.BeginBundleImport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sameIDMissing := makeSnapshot("usage-successor", 2, "absent-same-id-predecessor", 20)
	if imported, err := sameIDMissingTx.ImportUsageObservation(ctx, sameIDMissing, usecase.BundleConflictSkip); err == nil || imported {
		_ = sameIDMissingTx.Rollback(ctx)
		t.Fatalf("same-ID missing predecessor import = %t/%v, want fail closed", imported, err)
	}
	if err := sameIDMissingTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestBundleDatasource_LaterTableFailureRollsBackImportedUsageObservation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := sqlite.NewDatabase(filepath.Join(t.TempDir(), "traceary.db"), onDiskSQLiteMigrations(t))
	if err := sqlite.NewStoreManagementDatasource(db).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	bundles := sqlite.NewBundleDatasource(db, sqlite.NewEventDatasource(db))
	tx, err := bundles.BeginBundleImport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	observation := sqliteFinalizedUsage(t, sqliteUsageDescriptor(t, "usage-rolled-back"), 10)
	if imported, err := tx.ImportUsageObservation(ctx, observation, usecase.BundleConflictSkip); err != nil || !imported {
		_ = tx.Rollback(ctx)
		t.Fatalf("ImportUsageObservation() = %t/%v", imported, err)
	}
	eventID, err := types.EventIDFrom("missing-event")
	if err != nil {
		t.Fatal(err)
	}
	audit, err := model.NewCommandAudit(eventID, "go test ./...", "", "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if imported, err := tx.ImportCommandAudit(ctx, audit, usecase.BundleConflictSkip); err == nil || imported {
		_ = tx.Rollback(ctx)
		t.Fatalf("ImportCommandAudit() = %t/%v, want failure", imported, err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	stored, err := sqlite.NewUsageObservationDatasource(db).FindByID(ctx, observation.Descriptor().ObservationID())
	if err != nil {
		t.Fatal(err)
	}
	if _, present := stored.Value(); present {
		t.Fatal("usage observation survived rolled-back bundle transaction")
	}
}

func TestBundleDatasource_UsageObservationImportPreservesExclusivityClaims(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := sqlite.NewDatabase(filepath.Join(t.TempDir(), "traceary.db"), onDiskSQLiteMigrations(t))
	if err := sqlite.NewStoreManagementDatasource(db).Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	bundles := sqlite.NewBundleDatasource(db, sqlite.NewEventDatasource(db))
	key, err := types.UsageExclusivityKeyFrom("codex:headless_stream:thread-1:1")
	if err != nil {
		t.Fatal(err)
	}
	otherKey, err := types.UsageExclusivityKeyFrom("codex:headless_stream:thread-1:2")
	if err != nil {
		t.Fatal(err)
	}
	exclusive := func(id string, claimKey types.UsageExclusivityKey, accounting types.UsageAccounting) *model.UsageObservation {
		t.Helper()
		descriptor := sqliteUsageDescriptor(t, id)
		if descriptor.Accounting() != accounting {
			descriptor, err = model.NewUsageObservationDescriptor(
				descriptor.ObservationID(), descriptor.SessionID(), descriptor.Source(),
				descriptor.Scope(), accounting, descriptor.ObservedAt(),
			)
			if err != nil {
				t.Fatal(err)
			}
		}
		descriptor, err = descriptor.WithExclusivityKey(claimKey)
		if err != nil {
			t.Fatal(err)
		}
		return sqliteFinalizedUsage(t, descriptor, 10)
	}
	winner := exclusive("usage-winner", key, types.UsageAccountingAdditive)
	alternative := exclusive("usage-alternative", key, types.UsageAccountingExcluded)

	tx, err := bundles.BeginBundleImport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, observation := range []*model.UsageObservation{winner, alternative} {
		imported, err := tx.ImportUsageObservation(ctx, observation, usecase.BundleConflictSkip)
		if err != nil || !imported {
			_ = tx.Rollback(ctx)
			t.Fatalf("ImportUsageObservation(%s) = %t/%v", observation.Descriptor().ObservationID(), imported, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	replayTx, err := bundles.BeginBundleImport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if imported, err := replayTx.ImportUsageObservation(ctx, winner, usecase.BundleConflictSkip); err != nil || imported {
		_ = replayTx.Rollback(ctx)
		t.Fatalf("exact replay = %t/%v, want skipped", imported, err)
	}
	if err := replayTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	// A different additive observation claiming the same key must never
	// become a second owner. Skip keeps the destination claim; error and
	// replace fail closed.
	doubleWinner := exclusive("usage-double-winner", key, types.UsageAccountingAdditive)
	skipTx, err := bundles.BeginBundleImport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if imported, err := skipTx.ImportUsageObservation(ctx, doubleWinner, usecase.BundleConflictSkip); err != nil || imported {
		_ = skipTx.Rollback(ctx)
		t.Fatalf("double-claim under skip = %t/%v, want skipped", imported, err)
	}
	if err := skipTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for _, policy := range []usecase.BundleConflictPolicy{usecase.BundleConflictError, usecase.BundleConflictReplace} {
		policyTx, err := bundles.BeginBundleImport(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, err = policyTx.ImportUsageObservation(ctx, doubleWinner, policy)
		if err == nil || !errors.Is(err, model.ErrConflictingUsageObservation) {
			_ = policyTx.Rollback(ctx)
			t.Fatalf("double-claim under %s error = %v, want ErrConflictingUsageObservation", policy, err)
		}
		if err := policyTx.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
	}
	storedDoubleWinner, err := sqlite.NewUsageObservationDatasource(db).FindByID(ctx, doubleWinner.Descriptor().ObservationID())
	if err != nil {
		t.Fatal(err)
	}
	if _, present := storedDoubleWinner.Value(); present {
		t.Fatal("double-claim observation reached the store")
	}

	// Re-owning the stored winner under a different key conflicts on
	// identity metadata: skip retains the original claim, error fails closed.
	rekeyed := exclusive("usage-winner", otherKey, types.UsageAccountingAdditive)
	rekeySkipTx, err := bundles.BeginBundleImport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if imported, err := rekeySkipTx.ImportUsageObservation(ctx, rekeyed, usecase.BundleConflictSkip); err != nil || imported {
		_ = rekeySkipTx.Rollback(ctx)
		t.Fatalf("re-keyed winner under skip = %t/%v, want skipped", imported, err)
	}
	if err := rekeySkipTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	rekeyErrorTx, err := bundles.BeginBundleImport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = rekeyErrorTx.ImportUsageObservation(ctx, rekeyed, usecase.BundleConflictError)
	if err == nil || !errors.Is(err, model.ErrConflictingUsageObservation) {
		_ = rekeyErrorTx.Rollback(ctx)
		t.Fatalf("re-keyed winner under error = %v, want ErrConflictingUsageObservation", err)
	}
	if err := rekeyErrorTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	listed, err := bundles.ListBundleUsageObservations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 {
		t.Fatalf("listed %d usage observations, want winner and excluded alternative", len(listed))
	}
	for _, observation := range listed {
		claimKey, present := observation.Descriptor().ExclusivityKey().Value()
		if !present || claimKey != key {
			t.Fatalf("observation %s exclusivity key = %q/%t, want %q", observation.Descriptor().ObservationID(), claimKey, present, key)
		}
	}
}
