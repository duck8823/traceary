package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

type orphanRecordRepoStub struct {
	recorded []*model.SessionOrphanRange
}

func (s *orphanRecordRepoStub) Record(_ context.Context, orphan *model.SessionOrphanRange) error {
	s.recorded = append(s.recorded, orphan)
	return nil
}

func (s *orphanRecordRepoStub) DiscoverCandidates(context.Context, time.Duration, time.Time, int) (model.SessionOrphanCandidates, error) {
	return model.SessionOrphanCandidates{}, nil
}

func (s *orphanRecordRepoStub) LoadMaterial(
	context.Context, types.SessionID, types.Optional[types.EventID], types.EventID,
) (model.SessionOrphanMaterial, error) {
	return model.SessionOrphanMaterial{}, nil
}

type refinementFindStub struct {
	row *model.SessionRefinement
}

func (s *refinementFindStub) FindBySessionID(context.Context, types.SessionID) (types.Optional[*model.SessionRefinement], error) {
	if s.row == nil {
		return types.None[*model.SessionRefinement](), nil
	}
	return types.Some(s.row), nil
}

func (s *refinementFindStub) SaveIfAdvances(context.Context, *model.SessionRefinement, int) (bool, error) {
	return false, nil
}

type eventOrderStub struct {
	// after[left+"|"+right] = left is strictly after right
	after map[string]bool
}

func (s *eventOrderStub) EarliestEventID(context.Context, types.SessionID) (types.Optional[types.EventID], error) {
	return types.None[types.EventID](), nil
}

func (s *eventOrderStub) FindEventSessionID(_ context.Context, _ types.EventID) (types.Optional[types.SessionID], error) {
	return types.Some(types.SessionID("sess-1")), nil
}

func (s *eventOrderStub) EventIsStrictlyAfter(_ context.Context, left, right types.EventID) (bool, error) {
	if s.after == nil {
		return false, nil
	}
	return s.after[left.String()+"|"+right.String()], nil
}

func TestSessionOrphanRangeUsecase_RecordAtCompact_AfterUnfoldedRange(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	existing, err := model.NewSessionRefinement(
		"sess-1", 1, "evt-a", "evt-b", "folded", "", "agent", now, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	repo := &orphanRecordRepoStub{}
	sut := usecase.NewSessionOrphanRangeUsecase(
		repo,
		&refinementFindStub{row: existing},
		&eventOrderStub{after: map[string]bool{"evt-compact|evt-b": true}},
		fixedOrphanClock{at: now},
	)

	if err := sut.RecordAtCompact(context.Background(), "sess-1", "evt-compact"); err != nil {
		t.Fatalf("RecordAtCompact() error = %v", err)
	}
	if len(repo.recorded) != 1 {
		t.Fatalf("recorded = %d, want 1", len(repo.recorded))
	}
	got := repo.recorded[0]
	if got.ToEventID() != "evt-compact" {
		t.Fatalf("ToEventID = %s", got.ToEventID())
	}
	from, ok := got.FromEventID().Value()
	if !ok || from != "evt-b" {
		t.Fatalf("FromEventID = %v present=%v, want evt-b", from, ok)
	}
}

func TestSessionOrphanRangeUsecase_RecordAtCompact_WhenDigestCoveredEverything(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	// Digest just covered through the compact event itself.
	existing, err := model.NewSessionRefinement(
		"sess-1", 1, "evt-a", "evt-compact", "digest", "", "hook:post-compact:claude", now, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	repo := &orphanRecordRepoStub{}
	sut := usecase.NewSessionOrphanRangeUsecase(
		repo,
		&refinementFindStub{row: existing},
		&eventOrderStub{},
		fixedOrphanClock{at: now},
	)

	if err := sut.RecordAtCompact(context.Background(), "sess-1", "evt-compact"); err != nil {
		t.Fatalf("RecordAtCompact() error = %v", err)
	}
	if len(repo.recorded) != 0 {
		t.Fatalf("recorded = %d, want 0 when digest covers compact event", len(repo.recorded))
	}
}

func TestSessionOrphanRangeUsecase_RecordAtCompact_NoRefinementStartsAtFirstEvent(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	repo := &orphanRecordRepoStub{}
	sut := usecase.NewSessionOrphanRangeUsecase(
		repo,
		&refinementFindStub{},
		&eventOrderStub{},
		fixedOrphanClock{at: now},
	)

	if err := sut.RecordAtCompact(context.Background(), "sess-1", "evt-compact"); err != nil {
		t.Fatalf("RecordAtCompact() error = %v", err)
	}
	if len(repo.recorded) != 1 {
		t.Fatalf("recorded = %d, want 1", len(repo.recorded))
	}
	if _, ok := repo.recorded[0].FromEventID().Value(); ok {
		t.Fatal("FromEventID present, want None (range starts at first event)")
	}
	if repo.recorded[0].ToEventID() != "evt-compact" {
		t.Fatalf("ToEventID = %s", repo.recorded[0].ToEventID())
	}
}
