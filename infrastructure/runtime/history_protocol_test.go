package runtime

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
)

func historyProtocolFixture(t *testing.T) (app.RemoteHistoryRequest, app.HistoryResult) {
	t.Helper()
	p, err := domain.ProjectOf("/project", "/project/.git", "project")
	if err != nil {
		t.Fatal(err)
	}
	w, err := domain.WorktreeOf(p.ID(), "/project", "/project/.git", "/store", "original")
	if err != nil {
		t.Fatal(err)
	}
	source, err := domain.HistorySourceOf(w, "current")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	v, err := domain.HistorySelectionOf(base, base.Add(time.Hour), 2, domain.HistoryAnchor{})
	if err != nil {
		t.Fatal(err)
	}
	var rows []domain.HistoryEvent
	for _, id := range []string{"c", "b", "a"} {
		e, err := domain.HistoryEventOf(domain.HistoryEventState{ID: id, Kind: "note", Workspace: w.Path(), CreatedAt: base})
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, e)
	}
	page, err := domain.HistoryPageOf(source, v, rows)
	if err != nil {
		t.Fatal(err)
	}
	r := app.RemoteHistoryRequest{Epoch: strings.Repeat("a", 32), Request: app.HistoryRequest{Worktree: w.ID(), Selection: v}}
	return r, app.HistoryResult{Epoch: r.Epoch, Page: page, Hint: app.HistoryHintState{Revision: 9007199254740993}}
}
func TestHistoryProtocolRoundTripAndCanonicalCounters(t *testing.T) {
	r, result := historyProtocolFixture(t)
	record := recordHistoryResult(r, result)
	data, err := json.Marshal(runtimeResponse{Version: 1, ID: "test", Code: "ok", History: record})
	if err != nil {
		t.Fatal(err)
	}
	var reply runtimeResponse
	if err = decodeRuntimeJSON(data, &reply); err != nil {
		t.Fatal(err)
	}
	got, err := reply.History.result()
	if err != nil || got.Hint != result.Hint || got.Page.Next() != result.Page.Next() || got.Page.Source() != result.Page.Source() {
		t.Fatal("history roundtrip lost source/page/counter", err)
	}
	record.Hint.Revision = "01"
	if _, err = record.result(); err == nil {
		t.Fatal("noncanonical hint generation accepted")
	}
	record = recordHistoryResult(r, result)
	record.Page.Next.ID = "wrong"
	if _, err = record.result(); err == nil {
		t.Fatal("wrong retained-row continuation accepted")
	}
	record = recordHistoryResult(r, result)
	record.Request.Selection.From = "2026-11-04T00:00:00+00:00"
	if _, err = record.result(); err == nil {
		t.Fatal("noncanonical UTC time accepted")
	}
}
