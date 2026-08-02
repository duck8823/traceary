package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain"
)

func TestSQLiteCompactionBuilderBuildAndVerifyPair(t *testing.T) {
	ctx := context.Background()
	source := filepath.Join(t.TempDir(), "source.db")
	candidate := filepath.Join(filepath.Dir(source), "candidate.db")
	db, err := sql.Open("sqlite", directSQLiteRWDSNCreate(source))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE sample(id TEXT PRIMARY KEY, body BLOB); INSERT INTO sample VALUES('a',x'0001'),('b','text')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	builder := SQLiteCompactionBuilder{}
	before, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := builder.Build(ctx, source, candidate); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("Build mutated source bytes")
	}
	if err := builder.VerifyPair(ctx, source, candidate); err != nil {
		t.Fatal(err)
	}
	candidateDB, err := sql.Open("sqlite", directSQLiteRWDSN(candidate))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := candidateDB.Exec(`UPDATE sample SET body='changed' WHERE id='a'`); err != nil {
		t.Fatal(err)
	}
	if err := candidateDB.Close(); err != nil {
		t.Fatal(err)
	}
	if err := builder.VerifyPair(ctx, source, candidate); err == nil {
		t.Fatal("VerifyPair accepted logical mismatch")
	}
}

func TestStoreCompactionSmallAllocatedShapeE2E(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	source := filepath.Join(dir, "source.db")
	db, err := sql.Open("sqlite", directSQLiteRWDSNCreate(source))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE sample(id INTEGER PRIMARY KEY, body BLOB); INSERT INTO sample(body) VALUES(zeroblob(1048576)),('queryable')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	original, err := inspectRegularFile(source)
	if err != nil {
		t.Fatal(err)
	}
	planningJournal := &CompactionFileJournal{Dir: filepath.Join(dir, "planning-journal")}
	planner := usecase.NewStoreCompactionUsecase(source, planningJournal, SQLiteCompactionBuilder{}, StoreReplacementFiles{}, StoreLeaseCoordinator{})
	run, err := planner.Plan(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	if run.Resources.RequiredBytes == 0 || !run.Resources.ExchangeCapability {
		t.Fatalf("invalid resource plan: %+v", run.Resources)
	}
	if !run.Resources.LeaseCapability {
		t.Fatal("plan did not record available cross-process lease")
	}
	journal := &CompactionFileJournal{Dir: filepath.Join(dir, "apply-journal")}
	if err := journal.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	service := usecase.NewStoreCompactionUsecase(source, journal, SQLiteCompactionBuilder{}, StoreReplacementFiles{}, StoreLeaseCoordinator{})
	run, err = service.Apply(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Phase != domain.CompactionCommitted {
		t.Fatalf("phase=%s", run.Phase)
	}
	compacted, err := inspectRegularFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if compacted.Inode == original.Inode {
		t.Fatal("source inode did not exchange")
	}
	read, err := sql.Open("sqlite", sqliteReadOnlyDSN(source))
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := read.QueryRow(`SELECT count(*) FROM sample`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	_ = read.Close()
	if count != 2 {
		t.Fatalf("count=%d", count)
	}
	run, err = service.Rollback(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Phase != domain.CompactionRolledBack {
		t.Fatalf("rollback phase=%s", run.Phase)
	}
	restored, err := inspectRegularFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Inode != original.Inode {
		t.Fatalf("original inode not restored: %d != %d", restored.Inode, original.Inode)
	}
}

func TestStoreCompactionResumeReplacesRunOwnedNearCapacityPartialCandidate(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	source := filepath.Join(dir, "source.db")
	db, err := sql.Open("sqlite", directSQLiteRWDSNCreate(source))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE payloads(id INTEGER PRIMARY KEY, body BLOB); INSERT INTO payloads(body) VALUES(zeroblob(16777216)); CREATE TABLE probe(v TEXT); INSERT INTO probe VALUES('intact')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	plannerJournal := &CompactionFileJournal{Dir: filepath.Join(dir, "planner")}
	planner := usecase.NewStoreCompactionUsecase(source, plannerJournal, SQLiteCompactionBuilder{}, StoreReplacementFiles{}, StoreLeaseCoordinator{})
	run, err := planner.Plan(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	run.Resources.LeaseCapability = true
	journal := &CompactionFileJournal{Dir: filepath.Join(dir, "resume")}
	if err := journal.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	run, err = run.Advance(domain.CompactionCopyIntent, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.Append(ctx, run); err != nil {
		t.Fatal(err)
	}
	files := StoreReplacementFiles{}
	prepared, err := files.PrepareCandidate(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	run.PreparedCandidateIdentity = prepared
	run.PreparedAttempt++
	run, err = run.Advance(domain.CompactionCandidatePrepared, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.Append(ctx, run); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	partialSize := len(data) * 9 / 10
	file, err := os.OpenFile(run.CandidatePath, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(data[:partialSize]); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	t.Logf("source=%d partial=%d", len(data), partialSize)
	fresh := usecase.NewStoreCompactionUsecase(source, journal, SQLiteCompactionBuilder{}, StoreReplacementFiles{}, StoreLeaseCoordinator{})
	got, err := fresh.Resume(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Phase != domain.CompactionCommitted {
		t.Fatalf("phase=%s", got.Phase)
	}
	read, err := sql.Open("sqlite", sqliteReadOnlyDSN(source))
	if err != nil {
		t.Fatal(err)
	}
	var value string
	if err := read.QueryRow(`SELECT v FROM probe`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	_ = read.Close()
	if value != "intact" {
		t.Fatalf("value=%q", value)
	}
}

func TestStoreCompactionResumePreservesUnknownValidCandidate(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	source := filepath.Join(dir, "source.db")
	db, _ := sql.Open("sqlite", directSQLiteRWDSNCreate(source))
	_, _ = db.Exec(`CREATE TABLE expected(v TEXT); INSERT INTO expected VALUES('source')`)
	_ = db.Close()
	plannerJournal := &CompactionFileJournal{Dir: filepath.Join(dir, "planner")}
	planner := usecase.NewStoreCompactionUsecase(source, plannerJournal, SQLiteCompactionBuilder{}, StoreReplacementFiles{}, StoreLeaseCoordinator{})
	run, err := planner.Plan(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	run.Resources.LeaseCapability = true
	journal := &CompactionFileJournal{Dir: filepath.Join(dir, "resume")}
	if err := journal.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	run, _ = run.Advance(domain.CompactionCopyIntent, time.Now())
	if err := journal.Append(ctx, run); err != nil {
		t.Fatal(err)
	}
	other, _ := sql.Open("sqlite", directSQLiteRWDSNCreate(run.CandidatePath))
	_, _ = other.Exec(`CREATE TABLE unrelated(v TEXT); INSERT INTO unrelated VALUES('preserve')`)
	_ = other.Close()
	before, err := os.ReadFile(run.CandidatePath)
	if err != nil {
		t.Fatal(err)
	}
	fresh := usecase.NewStoreCompactionUsecase(source, journal, SQLiteCompactionBuilder{}, StoreReplacementFiles{}, StoreLeaseCoordinator{})
	if _, err := fresh.Resume(ctx, run.ID); err == nil {
		t.Fatal("Resume accepted unknown valid candidate")
	}
	after, err := os.ReadFile(run.CandidatePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("unknown candidate was mutated")
	}
	loaded, err := journal.Load(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Phase != domain.CompactionCopyIntent {
		t.Fatalf("journal advanced to %s", loaded.Phase)
	}
}

func TestStoreCompactionResumeDoesNotAdoptCrashGapEmptyCandidate(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	source := filepath.Join(dir, "source.db")
	db, _ := sql.Open("sqlite", directSQLiteRWDSNCreate(source))
	_, _ = db.Exec(`CREATE TABLE expected(v TEXT)`)
	_ = db.Close()
	planning := &CompactionFileJournal{Dir: filepath.Join(dir, "planning")}
	planner := usecase.NewStoreCompactionUsecase(source, planning, SQLiteCompactionBuilder{}, StoreReplacementFiles{}, StoreLeaseCoordinator{})
	run, err := planner.Plan(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	run.Resources.LeaseCapability = true
	journal := &CompactionFileJournal{Dir: filepath.Join(dir, "journal")}
	if err := journal.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	run, _ = run.Advance(domain.CompactionCopyIntent, time.Now())
	if err := journal.Append(ctx, run); err != nil {
		t.Fatal(err)
	}
	files := StoreReplacementFiles{}
	identity, err := files.PrepareCandidate(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Size != 0 {
		t.Fatal("prepared candidate is not empty")
	}
	fresh := usecase.NewStoreCompactionUsecase(source, journal, SQLiteCompactionBuilder{}, files, StoreLeaseCoordinator{})
	if _, err := fresh.Resume(ctx, run.ID); err == nil {
		t.Fatal("fresh Resume adopted unjournaled crash-gap candidate")
	}
	after, err := inspectRegularFile(run.CandidatePath)
	if err != nil {
		t.Fatal(err)
	}
	if !after.SameInode(identity) {
		t.Fatal("unknown candidate was replaced")
	}
	loaded, err := journal.Load(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Phase != domain.CompactionCopyIntent || loaded.PreparedCandidateIdentity != (domain.StoreFileIdentity{}) {
		t.Fatal("journal advanced across crash gap")
	}
}

func TestStoreCompactionResumeRejectsReplacedPreparedCandidateInode(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	source := filepath.Join(dir, "source.db")
	db, _ := sql.Open("sqlite", directSQLiteRWDSNCreate(source))
	_, _ = db.Exec(`CREATE TABLE expected(v TEXT)`)
	_ = db.Close()
	run, journal := prepareCompactionCandidateForResumeTest(ctx, t, source, dir)
	replacementPath := run.CandidatePath + ".replacement"
	other, _ := sql.Open("sqlite", directSQLiteRWDSNCreate(replacementPath))
	_, _ = other.Exec(`CREATE TABLE unrelated(v TEXT)`)
	_ = other.Close()
	replacement, err := inspectRegularFile(replacementPath)
	if err != nil {
		t.Fatal(err)
	}
	if replacement.SameInode(run.PreparedCandidateIdentity) {
		t.Fatal("replacement was not allocated as a distinct inode")
	}
	if err := os.Rename(replacementPath, run.CandidatePath); err != nil {
		t.Fatal(err)
	}
	fresh := usecase.NewStoreCompactionUsecase(source, journal, SQLiteCompactionBuilder{}, StoreReplacementFiles{}, StoreLeaseCoordinator{})
	if _, err := fresh.Resume(ctx, run.ID); err == nil {
		t.Fatal("Resume accepted a replacement inode")
	}
	after, err := inspectRegularFile(run.CandidatePath)
	if err != nil {
		t.Fatal(err)
	}
	if !after.SameInode(replacement) {
		t.Fatal("replacement candidate was mutated")
	}
}

func TestStoreCompactionResumeRebuildsValidIncompletePreparedCandidate(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	source := filepath.Join(dir, "source.db")
	db, _ := sql.Open("sqlite", directSQLiteRWDSNCreate(source))
	_, _ = db.Exec(`CREATE TABLE expected(v TEXT); INSERT INTO expected VALUES('source')`)
	_ = db.Close()
	run, journal := prepareCompactionCandidateForResumeTest(ctx, t, source, dir)
	other, err := sql.Open("sqlite", directSQLiteRWDSNCreate(run.CandidatePath))
	if err != nil {
		t.Fatal(err)
	}
	_, err = other.Exec(`CREATE TABLE unrelated(v TEXT); INSERT INTO unrelated VALUES('valid but incomplete')`)
	_ = other.Close()
	if err != nil {
		t.Fatal(err)
	}
	observed, err := inspectRegularFile(run.CandidatePath)
	if err != nil {
		t.Fatal(err)
	}
	if !observed.SameInode(run.PreparedCandidateIdentity) {
		t.Fatal("SQLite replaced the prepared inode")
	}
	fresh := usecase.NewStoreCompactionUsecase(source, journal, SQLiteCompactionBuilder{}, StoreReplacementFiles{}, StoreLeaseCoordinator{})
	got, err := fresh.Resume(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Phase != domain.CompactionCommitted {
		t.Fatalf("phase=%s", got.Phase)
	}
}

func TestStoreCompactionApplyRejectsSameContentReplacementAfterVerification(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	source := filepath.Join(dir, "source.db")
	db, _ := sql.Open("sqlite", directSQLiteRWDSNCreate(source))
	_, _ = db.Exec(`CREATE TABLE expected(v TEXT); INSERT INTO expected VALUES('source')`)
	_ = db.Close()
	journal := &CompactionFileJournal{Dir: filepath.Join(dir, "journal")}
	planner := usecase.NewStoreCompactionUsecase(source, journal, SQLiteCompactionBuilder{}, StoreReplacementFiles{}, StoreLeaseCoordinator{})
	run, err := planner.Plan(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	run.Resources.LeaseCapability = true
	// Recreate the test journal with the test-only lease capability.
	journal = &CompactionFileJournal{Dir: filepath.Join(dir, "apply")}
	if err := journal.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	builder := replacingAfterVerifyBuilder{SQLiteCompactionBuilder: SQLiteCompactionBuilder{}}
	service := usecase.NewStoreCompactionUsecase(source, journal, builder, StoreReplacementFiles{}, StoreLeaseCoordinator{})
	if _, err := service.Apply(ctx, run.ID); err == nil {
		t.Fatal("Apply accepted a same-content replacement inode")
	}
	loaded, err := journal.Load(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Phase != domain.CompactionScrubInProgress || loaded.Candidate != (domain.StoreFileIdentity{}) {
		t.Fatalf("journal advanced past fence: phase=%s candidate=%+v", loaded.Phase, loaded.Candidate)
	}
	observed, err := inspectRegularFile(run.CandidatePath)
	if err != nil {
		t.Fatal(err)
	}
	if observed.SameInode(loaded.PreparedCandidateIdentity) {
		t.Fatal("test did not replace the prepared inode")
	}
}

func TestStoreCompactionExclusiveBoundaryRejectsLateHardLinkBeforeObservation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		phase domain.CompactionPhase
		call  func(application.StoreCompactionUsecase, context.Context, string) (domain.CompactionRun, error)
	}{
		{name: "resume swap intent", phase: domain.CompactionSwapIntent, call: func(u application.StoreCompactionUsecase, ctx context.Context, id string) (domain.CompactionRun, error) {
			return u.Resume(ctx, id)
		}},
		{name: "rollback committed", phase: domain.CompactionCommitted, call: func(u application.StoreCompactionUsecase, ctx context.Context, id string) (domain.CompactionRun, error) {
			return u.Rollback(ctx, id)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			realDir := filepath.Join(root, "real")
			if err := os.Mkdir(realDir, 0o700); err != nil {
				t.Fatal(err)
			}
			aliasDir := filepath.Join(root, "alias")
			if err := os.Symlink(realDir, aliasDir); err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(aliasDir, "store.db")
			candidate := source + ".candidate"
			rollback := source + ".rollback"
			for path, body := range map[string]string{source: "source", candidate: "candidate", rollback: "rollback"} {
				if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			run := domain.CompactionRun{ID: "0123456789abcdef0123456789abcdef", SourcePath: source, CandidatePath: candidate, RollbackPath: rollback, Phase: tc.phase}
			journal := &observationTrackingJournal{run: run}
			service := usecase.NewStoreCompactionUsecase(source, journal, SQLiteCompactionBuilder{}, StoreReplacementFiles{}, StoreLeaseCoordinator{})
			hardlink := filepath.Join(realDir, "late-hardlink.db")
			if err := os.Link(filepath.Join(realDir, "store.db"), hardlink); err != nil {
				t.Fatal(err)
			}
			beforeCandidate, _ := os.ReadFile(candidate)
			beforeRollback, _ := os.ReadFile(rollback)
			if _, err := tc.call(service, ctx, run.ID); err == nil {
				t.Fatal("operation accepted late hard link")
			}
			if journal.loads != 0 || journal.appends != 0 {
				t.Fatal("journal was observed or advanced before exclusive fence")
			}
			afterCandidate, _ := os.ReadFile(candidate)
			afterRollback, _ := os.ReadFile(rollback)
			if string(afterCandidate) != string(beforeCandidate) || string(afterRollback) != string(beforeRollback) {
				t.Fatal("compaction artifacts changed")
			}
			if err := os.Remove(hardlink); err != nil {
				t.Fatal(err)
			}
			release, err := (StoreLeaseCoordinator{}).AcquireExclusive(ctx, source)
			if err != nil {
				t.Fatalf("failed acquisition retained OS lease: %v", err)
			}
			release()
		})
	}
}

type observationTrackingJournal struct {
	run            domain.CompactionRun
	loads, appends int
}

func (j *observationTrackingJournal) Create(context.Context, domain.CompactionRun) error { return nil }
func (j *observationTrackingJournal) Load(context.Context, string) (domain.CompactionRun, error) {
	j.loads++
	return j.run, nil
}
func (j *observationTrackingJournal) Append(_ context.Context, run domain.CompactionRun) error {
	j.appends++
	j.run = run
	return nil
}

type replacingAfterVerifyBuilder struct{ SQLiteCompactionBuilder }

func (b replacingAfterVerifyBuilder) VerifyPair(ctx context.Context, source, candidate string) error {
	if err := b.SQLiteCompactionBuilder.VerifyPair(ctx, source, candidate); err != nil {
		return err
	}
	contents, err := os.ReadFile(candidate)
	if err != nil {
		return fmt.Errorf("read verified candidate: %w", err)
	}
	replacement := candidate + ".replacement"
	if err := os.WriteFile(replacement, contents, 0o600); err != nil {
		return fmt.Errorf("replace verified candidate: %w", err)
	}
	originalID, err := inspectRegularFile(candidate)
	if err != nil {
		return fmt.Errorf("inspect verified candidate: %w", err)
	}
	replacementID, err := inspectRegularFile(replacement)
	if err != nil {
		return fmt.Errorf("inspect replacement candidate: %w", err)
	}
	if replacementID.SameInode(originalID) {
		return errors.New("replacement candidate reused original inode")
	}
	if err := os.Rename(replacement, candidate); err != nil {
		return fmt.Errorf("rename replacement candidate: %w", err)
	}
	return nil
}

func prepareCompactionCandidateForResumeTest(ctx context.Context, t *testing.T, source, dir string) (domain.CompactionRun, *CompactionFileJournal) {
	t.Helper()
	planning := &CompactionFileJournal{Dir: filepath.Join(dir, "planning")}
	planner := usecase.NewStoreCompactionUsecase(source, planning, SQLiteCompactionBuilder{}, StoreReplacementFiles{}, StoreLeaseCoordinator{})
	run, err := planner.Plan(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	run.Resources.LeaseCapability = true
	journal := &CompactionFileJournal{Dir: filepath.Join(dir, "journal")}
	if err := journal.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	run, _ = run.Advance(domain.CompactionCopyIntent, time.Now())
	if err := journal.Append(ctx, run); err != nil {
		t.Fatal(err)
	}
	identity, err := (StoreReplacementFiles{}).PrepareCandidate(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	run.PreparedCandidateIdentity = identity
	run.PreparedAttempt++
	run, _ = run.Advance(domain.CompactionCandidatePrepared, time.Now())
	if err := journal.Append(ctx, run); err != nil {
		t.Fatal(err)
	}
	return run, journal
}

func directSQLiteRWDSNCreate(path string) string { return "file:" + path }
