//go:build darwin || linux

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/application"
	app "github.com/duck8823/traceary/application/runtime"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	domain "github.com/duck8823/traceary/domain/runtime"
	"github.com/duck8823/traceary/domain/types"
	infra "github.com/duck8823/traceary/infrastructure/runtime"
	"github.com/duck8823/traceary/infrastructure/sqlite"
	schema "github.com/duck8823/traceary/schema/sqlite"
)

func newHistorySpoolReplayRoot(t *testing.T, path string) *RootCLI {
	t.Helper()
	migrations, err := schema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	db := sqlite.NewDatabase(path, migrations)
	events := sqlite.NewEventDatasource(db)
	// Real production storage/usecases; no repository stub or alternative
	// drainer. Initialize runs inside replay, not before the parent lock test.
	return NewRootCLI(WithStoreManagement(usecase.NewStoreManagementUsecase(sqlite.NewStoreManagementDatasource(db))), WithEvent(usecase.NewEventUsecase(events, events)), WithDatabasePathSetter(db.SetPath))
}

// The separate finite process prevents this test from borrowing the parent's
// legitimate same-process exclusive connection. It runs no daemon or AI.
func TestOwnedHistorySpoolReplayProcessHelper(t *testing.T) {
	if os.Getenv("TRACEARY_TEST_HISTORY_SPOOL") != "1" {
		t.Skip("subprocess-only fixture")
	}
	root := newHistorySpoolReplayRoot(t, os.Getenv("TRACEARY_TEST_HISTORY_STORE"))
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	replayed, failed := root.drainHookSpoolRecords(ctx, 5)
	if err := json.NewEncoder(os.Stdout).Encode(map[string]int{"replayed": replayed, "failed": failed}); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryFilesystemSpoolReplaysAfterRealCompactionWithoutDuplicatingCapture(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(root, "repo")
	if err = os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "--quiet"}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "--quiet", "-m", "fixture"}} {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "GIT_") {
				cmd.Env = append(cmd.Env, entry)
			}
		}
		cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
		if out, e := cmd.CombinedOutput(); e != nil {
			t.Fatalf("generated Git fixture: %v %.1000s", e, out)
		}
	}
	home := filepath.Join(root, "home")
	state := filepath.Join(root, "hook-state")
	if err = os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv(hookStateDirEnvKey, state)
	t.Setenv("TRACEARY_WORKSPACE", repo)
	all, err := schema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "store.db")
	database := sqlite.NewDatabase(path, all)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	if err = sqlite.NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	events := sqlite.NewEventDatasource(database)
	base := time.Now().UTC().Add(-time.Minute)
	seed := model.EventOf("baseline-note", types.EventKindNote, "cli", "fixture", "spool-session", types.Workspace(repo), "synthetic baseline", base)
	if err = events.Save(ctx, seed); err != nil {
		t.Fatal(err)
	}
	metadata, err := infra.NewRegistryStore(filepath.Join(root, "runtime"), infra.MaxRegistryBytes)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := app.NewRegistry(metadata, infra.RegistryResolver{})
	if err != nil {
		t.Fatal(err)
	}
	project, err := registry.RegisterProject(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	worktree, err := registry.RegisterWorktree(ctx, project.ID(), repo, path)
	if err != nil {
		t.Fatal(err)
	}
	sources, err := app.NewRegisteredHistorySources(metadata, infra.RegistryResolver{})
	if err != nil {
		t.Fatal(err)
	}
	history, err := app.NewHistory(sources, infra.SQLiteHistoryReader{})
	if err != nil {
		t.Fatal(err)
	}
	view, err := domain.HistorySelectionOf(base.Add(-time.Hour), base.Add(time.Hour), 1, domain.HistoryAnchor{})
	if err != nil {
		t.Fatal(err)
	}
	request := app.HistoryRequest{Worktree: worktree.ID(), Selection: view}
	walk := func() []string {
		t.Helper()
		r := request
		ids := []string{}
		for pages := 0; pages < 8; pages++ {
			p, e := history.Page(ctx, r)
			if e != nil {
				t.Fatal(e)
			}
			for _, event := range p.Events() {
				ids = append(ids, event.Snapshot().ID)
			}
			if !p.More() {
				return ids
			}
			r.ExpectedWitness = p.Source().Witness()
			r.Selection, e = domain.HistorySelectionOf(view.From(), view.To(), 1, p.Next())
			if e != nil {
				t.Fatal(e)
			}
		}
		t.Fatal("bounded fixture history did not complete")
		return nil
	}
	if got := walk(); !reflect.DeepEqual(got, []string{"baseline-note"}) {
		t.Fatal("unexpected baseline history")
	}
	payload, err := json.Marshal(map[string]string{"prompt": "synthetic spooled prompt", "session_id": "spool-session", "cwd": repo})
	if err != nil {
		t.Fatal(err)
	}
	record := hookSpoolRecord{SchemaVersion: hookSpoolSchemaVersion, Command: "prompt", Client: "claude", DBPath: path, Payload: string(payload), CreatedAt: time.Now().UTC().Add(-time.Minute), ReceiptID: "history-maintenance-spool-001", ResolvedWorkspace: &repo}
	pending, err := persistHookSpoolRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(pending)
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	drain := func() (int, int) {
		t.Helper()
		childCtx, stop := context.WithTimeout(ctx, 4*time.Second)
		defer stop()
		child := exec.CommandContext(childCtx, binary, "-test.run=^TestOwnedHistorySpoolReplayProcessHelper$")
		// Use TestMain's explicit fixture-sharing keys rather than weakening
		// its default isolation or assuming production env survives re-exec.
		child.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, testHookStateDirEnvKey + "=" + state, testDBPathEnvKey + "=" + path, "TRACEARY_WORKSPACE=" + repo, "TRACEARY_TEST_HISTORY_SPOOL=1", "TRACEARY_TEST_HISTORY_STORE=" + path, "GORACE=atexit_sleep_ms=0"}
		out, e := child.CombinedOutput()
		if e != nil {
			t.Fatalf("finite production spool helper: %v %.1000s", e, out)
		}
		document, ok := bytes.CutSuffix(bytes.TrimSpace(out), []byte("\nPASS"))
		if !ok {
			t.Fatalf("spool helper stdout mismatch: %.1000s", out)
		}
		var result struct {
			Replayed int `json:"replayed"`
			Failed   int `json:"failed"`
		}
		if e = json.Unmarshal(document, &result); e != nil {
			t.Fatal(e)
		}
		return result.Replayed, result.Failed
	}
	release, err := (sqlite.StoreLeaseCoordinator{}).AcquireExclusive(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err = history.Page(ctx, request); !errors.Is(err, app.ErrRuntimeBusy) {
		t.Fatal("history borrowed actual exclusive lease", err)
	}
	if replayed, failed := drain(); replayed != 0 || failed != 1 {
		t.Fatal("separate-process capture ignored real maintenance", replayed, failed)
	}
	// Production CAS requeue deliberately renames to a new retry-tail path.
	// Receipt identity, not a stale pathname, is the durable delivery identity.
	queued, err := listHookSpoolRecordPaths()
	if err != nil || len(queued) != 1 {
		t.Fatal("failed real drain lost or duplicated queue ownership", err)
	}
	pending = queued[0]
	requeued, err := os.ReadFile(pending)
	if err != nil {
		t.Fatal("failed real drain lost queued record", err)
	}
	var retained hookSpoolRecord
	if err = json.Unmarshal(requeued, &retained); err != nil {
		t.Fatal(err)
	}
	if retained.ReceiptID != record.ReceiptID || retained.Payload != record.Payload || retained.AttemptCount != 1 || retained.ResolvedWorkspace == nil || *retained.ResolvedWorkspace != repo {
		t.Fatal("actual claim/requeue lost receipt/acquired workspace")
	}
	if !strings.Contains(retained.LastError, (&apptypes.StoreMaintenancePendingError{}).Error()) {
		t.Fatal("requeue was not caused by the actual exclusive maintenance boundary", retained.LastError)
	}
	release()
	if got := walk(); !reflect.DeepEqual(got, []string{"baseline-note"}) {
		t.Fatal("failed cross-process replay wrote a premature capture", got)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	compactor := usecase.NewStoreCompactionUsecase(path, &sqlite.CompactionFileJournal{Dir: filepath.Join(root, "compaction-journal")}, &sqlite.SQLiteCompactionBuilder{}, sqlite.StoreReplacementFiles{CallerHoldsExclusiveLease: true}, sqlite.StoreLeaseCoordinator{})
	compacted, err := compactor.Compact(ctx, application.CompactInput{Source: path})
	if err != nil || compacted.CompactStrategy != application.CompactStrategyReplica {
		t.Fatal("actual compaction failed", err)
	}
	after, err := os.Stat(path)
	if err != nil || os.SameFile(before, after) {
		t.Fatal("no actual store exchange", err)
	}
	if replayed, failed := drain(); replayed != 1 || failed != 0 {
		t.Fatal("production filesystem replay failed after compaction", replayed, failed)
	}
	if _, err = os.Stat(pending); !os.IsNotExist(err) {
		t.Fatal("successful production drain retained pending record")
	}
	first := walk()
	if len(first) != 2 || first[1] != "baseline-note" {
		t.Fatal("filesystem replay did not add exactly one prompt", first)
	}
	// Simulate lost file acknowledgement with identical ORIGINAL durable bytes,
	// not a reconstructed event or new receipt/native input invocation.
	if err = os.WriteFile(pending, original, 0600); err != nil {
		t.Fatal(err)
	}
	if replayed, failed := drain(); replayed != 1 || failed != 0 {
		t.Fatal("exact spool redelivery failed", replayed, failed)
	}
	if got := walk(); !reflect.DeepEqual(got, first) {
		t.Fatal("filesystem replay duplicated or rebound canonical capture", got, first)
	}
	paths, err := listHookSpoolRecordPaths()
	if err != nil || len(paths) != 0 {
		t.Fatal("pending/claimed spool residue remained", err)
	}
	entries, err := os.ReadDir(filepath.Dir(pending))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), hookSpoolClaimMarker) {
			t.Fatal("owned drain left a claim")
		}
	}
	rows, err := events.ListRecentMetadata(ctx, apptypes.NewEventListCriteriaBuilder(64).Workspace(types.Workspace(repo)).From(view.From()).To(view.To()).Build())
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.EventID().String())
	}
	if !reflect.DeepEqual(first, ids) {
		t.Fatal("filesystem replay runtime/CLI membership diverged")
	}
}
