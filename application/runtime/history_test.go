package runtime_test

import (
	"context"
	"errors"
	"testing"
	"time"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
)

// These bounded fakes model read-only ports, not process ownership or commit hints.
type historyReadFixture struct {
	source                                 domain.HistorySource
	rows                                   []domain.HistoryEvent
	reads, resolves, rechecks              int
	failure, recheckFailure, readerFailure error
	cancelResolve, cancelRecheck           context.CancelFunc
}

func (f *historyReadFixture) ResolveHistorySource(context.Context, domain.WorktreeID) (domain.HistorySource, error) {
	f.resolves++
	if f.cancelResolve != nil {
		f.cancelResolve()
	}
	return f.source, f.failure
}
func (f *historyReadFixture) RevalidateHistorySource(context.Context, domain.HistorySource) error {
	f.rechecks++
	if f.cancelRecheck != nil {
		f.cancelRecheck()
	}
	return f.recheckFailure
}

func TestHistoryCancellationDuringSourceChecksIsNotMisclassifiedAsUnsafe(t *testing.T) {
	for _, stage := range []string{"resolve", "revalidate"} {
		t.Run(stage, func(t *testing.T) {
			f, r := historyAppFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if stage == "resolve" {
				f.cancelResolve = cancel
				f.failure = domain.ErrUnsafe
			} else {
				f.cancelRecheck = cancel
				f.recheckFailure = domain.ErrUnsafe
			}
			s, err := app.NewHistory(f, f)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.Page(ctx, r); !errors.Is(err, context.Canceled) {
				t.Fatal("source cancellation lost", err)
			}
		})
	}
}
func (f *historyReadFixture) ReadHistoryPage(_ context.Context, s domain.HistorySource, v domain.HistorySelection) ([]domain.HistoryEvent, error) {
	f.reads++
	if s != f.source || !v.Valid() {
		return nil, domain.ErrInvalid
	}
	return f.rows, f.readerFailure
}
func historyAppFixture(t *testing.T) (*historyReadFixture, app.HistoryRequest) {
	t.Helper()
	p, err := domain.ProjectOf("/project", "/project/.git", "project")
	if err != nil {
		t.Fatal(err)
	}
	w, err := domain.WorktreeOf(p.ID(), "/project", "/project/.git", "/store.db", "original")
	if err != nil {
		t.Fatal(err)
	}
	s, err := domain.HistorySourceOf(w, "current")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	v, err := domain.HistorySelectionOf(base, base.Add(time.Hour), 1, domain.HistoryAnchor{})
	if err != nil {
		t.Fatal(err)
	}
	f := &historyReadFixture{source: s}
	for _, id := range []string{"b", "a"} {
		e, err := domain.HistoryEventOf(domain.HistoryEventState{ID: id, Kind: "note", Workspace: w.Path(), CreatedAt: base})
		if err != nil {
			t.Fatal(err)
		}
		f.rows = append(f.rows, e)
	}
	return f, app.HistoryRequest{Worktree: w.ID(), Selection: v}
}
func TestHistoryPageResolvesReadsRevalidatesAndDoesNotGrantNativeAuthority(t *testing.T) {
	f, r := historyAppFixture(t)
	s, err := app.NewHistory(f, f)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Page(context.Background(), r)
	if err != nil || !p.More() || p.Next().ID != "b" || !p.Source().StoreIdentityChanged() || f.reads != 1 || f.resolves != 1 || f.rechecks != 1 {
		t.Fatal("history snapshot contract lost", err)
	}
	f.recheckFailure = domain.ErrChanged
	if _, err = s.Page(context.Background(), r); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("mid-read source replacement accepted", err)
	}
}
func TestHistoryRefusesStaleContinuationBeforeOpeningAnyReader(t *testing.T) {
	f, r := historyAppFixture(t)
	s, err := app.NewHistory(f, f)
	if err != nil {
		t.Fatal(err)
	}
	r.ExpectedWitness = "old-store"
	if _, err = s.Page(context.Background(), r); !errors.Is(err, domain.ErrChanged) || f.reads != 0 {
		t.Fatal("stale continuation read a replacement store", err)
	}
	r.ExpectedWitness = ""
	r.Selection, err = domain.HistorySelectionOf(r.Selection.From(), r.Selection.To(), 1, domain.HistoryAnchor{At: r.Selection.From(), ID: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Page(context.Background(), r); !errors.Is(err, domain.ErrInvalid) || f.reads != 0 {
		t.Fatal("unbound continuation accepted", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = s.Page(ctx, r); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled history accepted", err)
	}
}
func TestHistoryDoesNotRetryBusyOrRepairMalformedRows(t *testing.T) {
	f, r := historyAppFixture(t)
	s, err := app.NewHistory(f, f)
	if err != nil {
		t.Fatal(err)
	}
	f.rows = []domain.HistoryEvent{{}}
	if _, err = s.Page(context.Background(), r); !errors.Is(err, domain.ErrInvalid) || f.reads != 1 {
		t.Fatal("invalid row hidden", err)
	}
	f.rows = nil
	if p, err := s.Page(context.Background(), r); err != nil || p.Events() == nil || p.More() {
		t.Fatal("empty snapshot invalid", err)
	}
	reads, rechecks := f.reads, f.rechecks
	f.readerFailure = app.ErrRuntimeBusy
	if _, err = s.Page(context.Background(), r); !errors.Is(err, app.ErrRuntimeBusy) || f.reads != reads+1 || f.rechecks != rechecks {
		t.Fatal("busy reader retried or revalidated after failure", err)
	}
	if _, err = app.NewHistory(nil, f); err == nil {
		t.Fatal("missing source capability")
	}
	if _, err = app.NewHistory(f, nil); err == nil {
		t.Fatal("missing read capability")
	}
}
