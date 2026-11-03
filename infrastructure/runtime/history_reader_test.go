//go:build darwin || linux

package runtime_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	app "github.com/duck8823/traceary/application/runtime"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	domain "github.com/duck8823/traceary/domain/runtime"
	"github.com/duck8823/traceary/domain/types"
	infra "github.com/duck8823/traceary/infrastructure/runtime"
	db "github.com/duck8823/traceary/infrastructure/sqlite"
	schema "github.com/duck8823/traceary/schema/sqlite"
)

type historySQLiteFixture struct {
	history    *app.History
	sources    *app.RegisteredHistorySources
	events     *db.EventDatasource
	request    app.HistoryRequest
	path, repo string
	base       time.Time
}

func historySQLite(t *testing.T) historySQLiteFixture {
	t.Helper()
	root, repo, path := fixture(t)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	migrations, err := schema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	database := db.NewDatabase(path, migrations)
	if err = db.NewStoreManagementDatasource(database).Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	registry, metadata := service(t, root)
	p, err := registry.RegisterProject(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	w, err := registry.RegisterWorktree(context.Background(), p.ID(), repo, path)
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
	base := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	view, err := domain.HistorySelectionOf(base, base.Add(time.Hour), 2, domain.HistoryAnchor{})
	if err != nil {
		t.Fatal(err)
	}
	return historySQLiteFixture{history, sources, db.NewEventDatasource(database), app.HistoryRequest{Worktree: w.ID(), Selection: view}, path, repo, base}
}
func (f historySQLiteFixture) save(t *testing.T, id string, at time.Time, workspace string) {
	t.Helper()
	e := model.EventOf(types.EventID(id), types.EventKindNote, "cli", "fixture", "session", types.Workspace(workspace), strings.Repeat("body must never be hydrated ", 1000), at)
	if err := f.events.Save(context.Background(), e); err != nil {
		t.Fatal(err)
	}
}
func (f historySQLiteFixture) walk(t *testing.T) []string {
	t.Helper()
	request := f.request
	var result []string
	for pages := 0; pages < 100; pages++ {
		p, err := f.history.Page(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range p.Events() {
			result = append(result, event.Snapshot().ID)
		}
		if !p.More() {
			return result
		}
		request.ExpectedWitness = p.Source().Witness()
		request.Selection, err = domain.HistorySelectionOf(request.Selection.From(), request.Selection.To(), request.Selection.Limit(), p.Next())
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("history walk did not terminate")
	return nil
}
func TestHistorySQLiteRescanRecoversDeepLateAndTiedRowsWithNoHints(t *testing.T) {
	f := historySQLite(t)
	for i, id := range []string{"a", "b", "c", "d", "e", "f"} {
		f.save(t, id, f.base.Add(time.Duration(i+1)*time.Minute), f.repo)
	}
	f.save(t, "foreign", f.base.Add(time.Second), "/foreign-worktree")
	first, err := f.history.Page(context.Background(), f.request)
	if err != nil || !first.More() {
		t.Fatal(err)
	}
	if previous := f.walk(t); len(previous) != 6 {
		t.Fatal("initial full requested view not recovered", previous)
	}
	// Earlier than the first page AND earlier than a prior walk's final timestamp.
	f.save(t, "late", f.base.Add(30*time.Second), f.repo)
	f.save(t, "b-tie", f.base.Add(2*time.Minute), f.repo)
	got := f.walk(t)
	legacy, err := f.events.ListRecentMetadata(context.Background(), apptypes.NewEventListCriteriaBuilder(100).Workspace(types.Workspace(f.repo)).From(f.base).To(f.base.Add(time.Hour)).Build())
	if err != nil {
		t.Fatal(err)
	}
	want := make([]string, len(legacy))
	for i, e := range legacy {
		want[i] = e.EventID().String()
	}
	if !reflect.DeepEqual(got, want) || len(got) != 8 {
		t.Fatal("rescan lost committed late/tied rows or changed CLI membership", got, want)
	}
}
func TestHistorySQLiteReadsCommittedRowsOnlyAndDrainsForMaintenance(t *testing.T) {
	f := historySQLite(t)
	ctx := context.Background()
	raw, err := sql.Open("sqlite", f.path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	tx, err := raw.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `INSERT INTO events(id,kind,client,agent,session_id,workspace,body,created_at,source_hook) VALUES(?,?,?,?,?,?,?,?,?)`, "pending", "note", "cli", "fixture", "session", f.repo, "uncommitted body", f.base.Format(time.RFC3339Nano), "")
	if err != nil {
		t.Fatal(err)
	}
	if got := f.walk(t); len(got) != 0 {
		t.Fatal("uncommitted event appeared", got)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := f.walk(t); !reflect.DeepEqual(got, []string{"pending"}) {
		t.Fatal("commit without hint not recovered", got)
	}
	maintenance, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	release, err := (db.StoreLeaseCoordinator{}).AcquireExclusive(maintenance, f.path)
	if err != nil {
		t.Fatal("history kept a read lease", err)
	}
	if _, err = f.history.Page(ctx, f.request); !errors.Is(err, app.ErrRuntimeBusy) {
		release()
		t.Fatal("maintenance not reported busy", err)
	}
	release()
	if got := f.walk(t); len(got) != 1 {
		t.Fatal("reader failed after maintenance", got)
	}
}
func TestHistorySQLiteRejectsMalformedRowWithoutSkippingAndSurvivesStoreReplacement(t *testing.T) {
	f := historySQLite(t)
	ctx := context.Background()
	f.save(t, "a", f.base, f.repo)
	p, err := f.history.Page(ctx, f.request)
	if err != nil {
		t.Fatal(err)
	}
	old := p.Source()
	raw, err := sql.Open("sqlite", f.path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = raw.ExecContext(ctx, `UPDATE events SET source_hook=? WHERE id='a'`, strings.Repeat("x", 257)); err != nil {
		t.Fatal(err)
	}
	if _, err = f.history.Page(ctx, f.request); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal("oversized row hidden", err)
	}
	if _, err = raw.ExecContext(ctx, `UPDATE events SET source_hook='' WHERE id='a'`); err != nil {
		t.Fatal(err)
	}
	if _, err = raw.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
	if err = raw.Close(); err != nil {
		t.Fatal(err)
	}
	release, err := (db.StoreLeaseCoordinator{}).AcquireExclusive(ctx, f.path)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(f.path)
	if err != nil {
		release()
		t.Fatal(err)
	}
	if err = os.Rename(f.path, f.path+".before-replacement"); err != nil {
		release()
		t.Fatal(err)
	}
	if err = os.WriteFile(f.path, content, 0600); err != nil {
		release()
		t.Fatal(err)
	}
	release()
	r := f.request
	r.ExpectedWitness = old.Witness()
	if _, err = f.history.Page(ctx, r); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("stale source continuation accepted", err)
	}
	p, err = f.history.Page(ctx, f.request)
	if err != nil || !p.Source().StoreIdentityChanged() || len(p.Events()) != 1 {
		t.Fatal("fresh post-replacement resnapshot failed", err)
	}
	ids := []string{p.Events()[0].Snapshot().ID}
	sort.Strings(ids)
	if ids[0] != "a" {
		t.Fatal(ids)
	}
}
