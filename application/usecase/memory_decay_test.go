package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	domtypes "github.com/duck8823/traceary/domain/types"
)

type decayQueryFake struct {
	summaries []apptypes.MemorySummary
}

func (f *decayQueryFake) List(context.Context, apptypes.MemoryListCriteria) ([]apptypes.MemorySummary, error) {
	return f.summaries, nil
}
func (f *decayQueryFake) Search(context.Context, apptypes.MemorySearchCriteria) ([]apptypes.MemorySummary, error) {
	return nil, nil
}
func (f *decayQueryFake) GetDetails(context.Context, domtypes.MemoryID) (apptypes.MemoryDetails, error) {
	return apptypes.MemoryDetails{}, nil
}

var _ queryservice.MemoryQueryService = (*decayQueryFake)(nil)

type decayRepoFake struct {
	find  *model.Memory
	saves int
}

func (r *decayRepoFake) Save(context.Context, *model.Memory) error {
	r.saves++
	return nil
}
func (r *decayRepoFake) FindByID(context.Context, domtypes.MemoryID) (domtypes.Optional[*model.Memory], error) {
	if r.find == nil {
		return domtypes.None[*model.Memory](), nil
	}
	return domtypes.Some(r.find), nil
}
func (r *decayRepoFake) SaveDistillation(context.Context, *model.Memory, []*model.Memory) error {
	return nil
}
func (r *decayRepoFake) SaveSupersession(context.Context, *model.Memory, *model.Memory) error {
	return nil
}

func (r *decayRepoFake) BackfillCandidateTTLs(context.Context, time.Duration) (int, error) {
	r.saves++
	return 1, nil
}

type decayClock struct{ t time.Time }

func (c decayClock) Now() time.Time { return c.t }

func TestMemoryUsecase_Decay_DryRunReportsEligibleWithoutWriting(t *testing.T) {
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	id, err := domtypes.MemoryIDFrom("mem-decay-1")
	if err != nil {
		t.Fatal(err)
	}
	mem, err := model.NewMemoryCandidateWithClock(
		id,
		domtypes.MemoryTypeDecision,
		domtypes.WorkspaceScopeOf(domtypes.Workspace("ws")),
		"old extracted fact",
		domtypes.MemorySourceExtracted,
		nil, nil,
		domtypes.None[domtypes.MemoryID](),
		decayClock{t: old},
	)
	if err != nil {
		t.Fatal(err)
	}
	details, err := apptypes.MemoryDetailsFrom(mem)
	if err != nil {
		t.Fatal(err)
	}
	query := &decayQueryFake{summaries: []apptypes.MemorySummary{details.Summary()}}
	repo := &decayRepoFake{}
	sut := usecase.NewMemoryUsecase(repo, query, nil)

	result, err := sut.Decay(context.Background(), apptypes.MemoryDecayCriteria{
		OlderThan: 24 * time.Hour,
		Limit:     10,
		Apply:     false,
		Now:       now,
	})
	if err != nil {
		t.Fatalf("Decay() error = %v", err)
	}
	if result.Applied {
		t.Fatal("dry-run must not set Applied")
	}
	if len(result.ExpiredIDs) != 1 || result.ExpiredIDs[0] != "mem-decay-1" {
		t.Fatalf("ExpiredIDs = %#v", result.ExpiredIDs)
	}
	if repo.saves != 0 {
		t.Fatalf("dry-run must not Save, saves=%d", repo.saves)
	}
	if result.Backfilled != 0 {
		t.Fatalf("stamped extracted candidate Backfilled=%d, want 0", result.Backfilled)
	}
}

func TestMemoryUsecase_Decay_UsesCreatedAtAndCountsUnstamped(t *testing.T) {
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	id, err := domtypes.MemoryIDFrom("mem-decay-unstamped")
	if err != nil {
		t.Fatal(err)
	}
	scope := domtypes.WorkspaceScopeOf(domtypes.Workspace("ws"))
	summary, err := apptypes.MemorySummaryOf(
		id,
		domtypes.MemoryTypeDecision,
		scope,
		"legacy extracted with null expires_at",
		domtypes.MemoryStatusCandidate,
		domtypes.ConfidenceLow,
		domtypes.MemorySourceExtracted,
		domtypes.None[domtypes.MemoryID](),
		domtypes.None[time.Time](),
		old,
		domtypes.None[time.Time](),
		old,
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	query := &decayQueryFake{summaries: []apptypes.MemorySummary{summary}}
	repo := &decayRepoFake{}
	sut := usecase.NewMemoryUsecase(repo, query, nil)

	dry, err := sut.Decay(context.Background(), apptypes.MemoryDecayCriteria{
		OlderThan: 30 * 24 * time.Hour,
		Limit:     10,
		Apply:     false,
		Now:       now,
	})
	if err != nil {
		t.Fatalf("Decay() dry-run error = %v", err)
	}
	if dry.Backfilled != 1 {
		t.Fatalf("Backfilled=%d, want 1 unstamped extracted", dry.Backfilled)
	}
	if len(dry.ExpiredIDs) != 1 {
		t.Fatalf("ExpiredIDs=%#v, want due from created_at despite fresh updated_at", dry.ExpiredIDs)
	}
	if repo.saves != 0 {
		t.Fatalf("dry-run must not backfill, saves=%d", repo.saves)
	}

	applied, err := sut.Decay(context.Background(), apptypes.MemoryDecayCriteria{
		OlderThan: 30 * 24 * time.Hour,
		Limit:     10,
		Apply:     true,
		Now:       now,
	})
	if err != nil {
		t.Fatalf("Decay() apply error = %v", err)
	}
	if applied.Backfilled != 1 {
		t.Fatalf("apply Backfilled=%d, want SQL stamp count", applied.Backfilled)
	}
	if repo.saves < 1 {
		t.Fatal("apply must call BackfillCandidateTTLs")
	}
}

func TestMemoryUsecase_Restore_ExpiredToCandidate(t *testing.T) {
	id, _ := domtypes.MemoryIDFrom("mem-restore-1")
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	mem, err := model.NewMemoryCandidateWithClock(
		id,
		domtypes.MemoryTypeDecision,
		domtypes.WorkspaceScopeOf(domtypes.Workspace("ws")),
		"restorable fact",
		domtypes.MemorySourceExtracted,
		nil, nil,
		domtypes.None[domtypes.MemoryID](),
		decayClock{t: now},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := mem.Expire(now); err != nil {
		t.Fatal(err)
	}
	repo := &decayRepoFake{find: mem}
	sut := usecase.NewMemoryUsecase(repo, &decayQueryFake{}, nil)
	details, err := sut.Restore(context.Background(), id)
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if details.Summary().Status() != domtypes.MemoryStatusCandidate {
		t.Fatalf("status = %s", details.Summary().Status())
	}
	if repo.saves != 1 {
		t.Fatalf("saves = %d", repo.saves)
	}
}
