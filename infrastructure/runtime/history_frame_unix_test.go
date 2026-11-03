//go:build darwin || linux

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
)

// Frame fixture emulates the exact bounded descending keyset reader; no SQL/native effects.
type historyFrameFixture struct {
	source domain.HistorySource
	rows   []domain.HistoryEvent
}

func (f historyFrameFixture) ResolveHistorySource(context.Context, domain.WorktreeID) (domain.HistorySource, error) {
	return f.source, nil
}
func (f historyFrameFixture) RevalidateHistorySource(context.Context, domain.HistorySource) error {
	return nil
}
func (f historyFrameFixture) ReadHistoryPage(_ context.Context, _ domain.HistorySource, v domain.HistorySelection) ([]domain.HistoryEvent, error) {
	rows := []domain.HistoryEvent{}
	for _, e := range f.rows {
		if !v.Before().IsZero() && e.Snapshot().ID >= v.Before().ID {
			continue
		}
		rows = append(rows, e)
		if len(rows) == v.Limit()+1 {
			break
		}
	}
	return rows, nil
}
func historyFrameServer(t *testing.T, pathBytes int) (*daemonServer, app.RemoteHistoryRequest) {
	t.Helper()
	path := "/" + strings.Repeat("<", pathBytes)
	p, err := domain.ProjectOf(path, path+"/.git", "project")
	if err != nil {
		t.Fatal(err)
	}
	w, err := domain.WorktreeOf(p.ID(), path, path+"/.git", path+"/store", "original")
	if err != nil {
		t.Fatal(err)
	}
	source, err := domain.HistorySourceOf(w, "current")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	v, err := domain.HistorySelectionOf(base, base.Add(time.Hour), 64, domain.HistoryAnchor{})
	if err != nil {
		t.Fatal(err)
	}
	f := historyFrameFixture{source: source}
	for i := 64; i >= 0; i-- {
		e, err := domain.HistoryEventOf(domain.HistoryEventState{ID: fmt.Sprintf("%03d", i), Kind: "note", Workspace: path, CreatedAt: base})
		if err != nil {
			t.Fatal(err)
		}
		f.rows = append(f.rows, e)
	}
	history, err := app.NewHistory(f, f)
	if err != nil {
		t.Fatal(err)
	}
	s := &daemonServer{ctx: context.Background(), epoch: strings.Repeat("a", 32), history: history, daemon: &LocalDaemon{}}
	return s, app.RemoteHistoryRequest{Epoch: s.epoch, Request: app.HistoryRequest{Worktree: w.ID(), Selection: v}}
}
func TestHistoryFrameAdaptivePrefixRecoversEveryTrimmedRow(t *testing.T) {
	s, request := historyFrameServer(t, 600)
	seen := map[string]bool{}
	frames := 0
	for range 100 {
		r := recordHistoryRequest(request)
		reply := runtimeResponse{Version: 1, ID: "frame"}
		s.historyReply(&reply, &r)
		if reply.Code != "ok" || reply.History == nil {
			t.Fatal("history frame not returned", reply.Code)
		}
		data, err := json.Marshal(reply)
		if err != nil || len(data) > MaxRuntimeFrameBytes {
			t.Fatal("oversize frame", err, len(data))
		}
		result, err := reply.History.result()
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range result.Page.Events() {
			id := event.Snapshot().ID
			if seen[id] {
				t.Fatal("replayed trimmed row", id)
			}
			seen[id] = true
		}
		frames++
		if !result.Page.More() {
			break
		}
		request.Request.ExpectedWitness = result.Page.Source().Witness()
		request.Request.Selection, err = domain.HistorySelectionOf(request.Request.Selection.From(), request.Request.Selection.To(), 64, result.Page.Next())
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 65 || frames < 2 {
		t.Fatal("frame trimming lost history", len(seen), frames)
	}
}
func TestHistoryFrameOversizedSingleHeaderFailsExplicitly(t *testing.T) {
	s, r := historyFrameServer(t, 4000)
	record := recordHistoryRequest(r)
	reply := runtimeResponse{Version: 1, ID: "limit"}
	s.historyReply(&reply, &record)
	if reply.Code != "limit" || reply.History != nil {
		t.Fatal("oversize history header silently truncated")
	}
}
func TestHistoryProtocolRejectsEveryMixedRequestArm(t *testing.T) {
	r, _ := historyProtocolFixture(t)
	record := recordHistoryRequest(r)
	for _, request := range []runtimeRequest{
		{Operation: "status", History: &record}, {Operation: "hello", HistoryHint: &struct{}{}},
		{Operation: "history_page", History: &record, Control: &controlRequestRecord{}},
		{Operation: "history_page", History: &record, Receipt: &controlReceiptRequestRecord{}},
		{Operation: "history_page", History: &record, Attach: &outputAttachRecord{}},
		{Operation: "history_page", History: &record, Launch: &launchRequestRecord{}},
		{Operation: "history_page", History: &record, Page: &runPageRequestRecord{}},
		{Operation: "history_hint", HistoryHint: &struct{}{}, Observation: &observationRecord{}},
		{Operation: "history_hint", HistoryHint: &struct{}{}, History: &record},
	} {
		if request.singleArm() {
			t.Fatal("mixed/cross-operation request accepted")
		}
	}
}
