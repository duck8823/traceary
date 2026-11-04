package runtime_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	domain "github.com/duck8823/traceary/domain/runtime"
)

func historyDomainFixture(t *testing.T) (domain.HistorySource, domain.HistorySelection, time.Time) {
	t.Helper()
	p, err := domain.ProjectOf("/project", "/project/.git", "project-witness")
	if err != nil {
		t.Fatal(err)
	}
	w, err := domain.WorktreeOf(p.ID(), "/project", "/project/.git", "/store.db", "worktree-witness")
	if err != nil {
		t.Fatal(err)
	}
	s, err := domain.HistorySourceOf(w, "worktree-witness")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	v, err := domain.HistorySelectionOf(base, base.Add(time.Hour), 2, domain.HistoryAnchor{})
	if err != nil {
		t.Fatal(err)
	}
	return s, v, base
}
func historyDomainEvent(t *testing.T, id string, at time.Time) domain.HistoryEvent {
	t.Helper()
	e, err := domain.HistoryEventOf(domain.HistoryEventState{ID: id, Kind: "note", Client: "cli", Agent: "fixture", SessionID: "session", Workspace: "/project", CreatedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func TestHistoryPageUsesCompleteOrderedPrefixAndCanonicalContinuation(t *testing.T) {
	s, v, base := historyDomainFixture(t)
	rows := []domain.HistoryEvent{historyDomainEvent(t, "c", base.Add(time.Second)), historyDomainEvent(t, "b", base), historyDomainEvent(t, "a", base)}
	p, err := domain.HistoryPageOf(s, v, rows)
	if err != nil || !p.More() || p.Next().ID != "b" || len(p.Events()) != 2 {
		t.Fatal("page silently lost continuation", err)
	}
	rows[0] = domain.HistoryEvent{}
	cloned := p.Events()
	cloned[0] = domain.HistoryEvent{}
	if p.Events()[0].Snapshot().ID != "c" {
		t.Fatal("page aliases caller memory")
	}
	trimmed, err := p.Prefix(1)
	if err != nil || !trimmed.More() || trimmed.Next().ID != "c" {
		t.Fatal("frame trim skipped rows", err)
	}
	next, err := domain.HistorySelectionOf(v.From(), v.To(), 2, p.Next())
	if err != nil {
		t.Fatal(err)
	}
	last, err := domain.HistoryPageOf(s, next, []domain.HistoryEvent{historyDomainEvent(t, "a", base)})
	if err != nil || last.More() || !last.Next().IsZero() {
		t.Fatal("last page invented cursor", err)
	}
}
func TestHistoryPageRejectsForeignInvalidDuplicateAndUnorderedRows(t *testing.T) {
	s, v, base := historyDomainFixture(t)
	good := historyDomainEvent(t, "b", base)
	foreign := good.Snapshot()
	foreign.Workspace = "/foreign"
	f, err := domain.HistoryEventOf(foreign)
	if err != nil {
		t.Fatal(err)
	}
	for _, rows := range [][]domain.HistoryEvent{{{}}, {f}, {good, good}, {historyDomainEvent(t, "a", base), good}, {historyDomainEvent(t, "z", v.To())}, {good, historyDomainEvent(t, "b", base.Add(-time.Second))}, {good, good, good, good}} {
		if _, err = domain.HistoryPageOf(s, v, rows); err == nil {
			t.Fatal("invalid history row was accepted/skipped")
		}
	}
}
func TestHistorySelectionAndEventBoundsRefusePartialAnchorsAndUnsafeMetadata(t *testing.T) {
	_, v, base := historyDomainFixture(t)
	for _, a := range []domain.HistoryAnchor{{ID: "id"}, {At: base}, {At: base.Add(-time.Second), ID: "id"}, {At: v.To(), ID: "id"}, {At: base, ID: "bad\n"}} {
		if _, err := domain.HistorySelectionOf(v.From(), v.To(), 2, a); err == nil {
			t.Fatal("invalid anchor admitted")
		}
	}
	for _, limit := range []int{0, -1, 65} {
		if _, err := domain.HistorySelectionOf(v.From(), v.To(), limit, domain.HistoryAnchor{}); err == nil {
			t.Fatal("invalid limit admitted")
		}
	}
	if _, err := domain.HistorySelectionOf(base, base, 2, domain.HistoryAnchor{}); err == nil {
		t.Fatal("empty window admitted")
	}
	for _, bad := range []string{"bad\x00", "bad\r", "bad\n", "bad\xff", strings.Repeat("x", 257)} {
		state := historyDomainEvent(t, "ok", base).Snapshot()
		state.SourceHook = bad
		if _, err := domain.HistoryEventOf(state); err == nil {
			t.Fatal("unsafe/oversize metadata admitted")
		}
	}
	s, _, _ := historyDomainFixture(t)
	changed, err := domain.HistorySourceOf(s.Worktree(), "replaced-store-witness")
	if err != nil || !changed.StoreIdentityChanged() {
		t.Fatal("store replacement hidden", err)
	}
	if _, err = domain.HistorySourceOf(domain.Worktree{}, "witness"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal(err)
	}
}

func TestHistoryUTCNormalizationCannotCrossSupportedYearBoundary(t *testing.T) {
	local := time.Date(1, 1, 1, 0, 0, 0, 0, time.FixedZone("offset", 3600))
	if _, err := domain.HistorySelectionOf(local, local.Add(time.Hour), 1, domain.HistoryAnchor{}); err == nil {
		t.Fatal("constructor normalized to unsupported UTC year zero")
	}
	state := domain.HistoryEventState{ID: "id", Kind: "note", Workspace: "/project", CreatedAt: local}
	if _, err := domain.HistoryEventOf(state); err == nil {
		t.Fatal("event normalized to unsupported UTC year zero")
	}
	state.CreatedAt = time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	state.ID = " padded "
	if _, err := domain.HistoryEventOf(state); err == nil {
		t.Fatal("noncanonical event ID accepted")
	}
}

func TestHistoryPageTransportReconstructionNeverInventsLookaheadRows(t *testing.T) {
	s, v, base := historyDomainFixture(t)
	e := historyDomainEvent(t, "b", base)
	state := domain.HistoryPageState{Source: s, Selection: v, Events: []domain.HistoryEvent{e}, More: true, Next: domain.HistoryAnchor{At: base, ID: "b"}}
	p, err := domain.HistoryPageFrom(state)
	if err != nil || !p.More() || len(p.Events()) != 1 {
		t.Fatal(err)
	}
	for _, bad := range []domain.HistoryPageState{
		{Source: s, Selection: v, Events: []domain.HistoryEvent{}, More: true, Next: state.Next},
		{Source: s, Selection: v, Events: state.Events, More: false, Next: state.Next},
		{Source: s, Selection: v, Events: state.Events, More: true, Next: domain.HistoryAnchor{At: base, ID: "a"}},
		{Source: s, Selection: v, Events: nil},
	} {
		if _, err = domain.HistoryPageFrom(bad); err == nil {
			t.Fatal("invalid peer page reconstructed")
		}
	}
}
