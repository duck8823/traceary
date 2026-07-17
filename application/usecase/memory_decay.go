package usecase

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

const defaultMemoryDecayLimit = 500

// Decay expires eligible auto-extracted candidates and optionally supersedes
// exact duplicates. Dry-run is the default (criteria.Apply == false).
func (u *memoryUsecase) Decay(ctx context.Context, criteria apptypes.MemoryDecayCriteria) (apptypes.MemoryDecayResult, error) {
	if u.memoryQuery == nil {
		return apptypes.MemoryDecayResult{}, xerrors.Errorf("memory query service is not configured")
	}
	if criteria.Apply && u.memoryRepo == nil {
		return apptypes.MemoryDecayResult{}, xerrors.Errorf("memory repository is not configured")
	}

	olderThan := criteria.OlderThan
	if olderThan <= 0 {
		olderThan = domtypes.DefaultMemoryDecayOlderThan
	}
	policy, err := domtypes.MemoryDecayPolicyOf(olderThan, nil)
	if err != nil {
		return apptypes.MemoryDecayResult{}, err
	}
	limit := criteria.Limit
	if limit <= 0 {
		limit = defaultMemoryDecayLimit
	}
	now := criteria.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	sources := policy.Sources()
	summaries, err := u.listCandidateSummariesForDecay(ctx, sources)
	if err != nil {
		return apptypes.MemoryDecayResult{}, err
	}
	if ws, ok := criteria.Workspace.Value(); ok && strings.TrimSpace(ws.String()) != "" {
		filtered := make([]apptypes.MemorySummary, 0, len(summaries))
		for _, s := range summaries {
			if s.Scope().Kind() == domtypes.MemoryScopeKindWorkspace && s.Scope().Key() == ws.String() {
				filtered = append(filtered, s)
			}
		}
		summaries = filtered
	}

	result := apptypes.MemoryDecayResult{
		ExpiredIDs:    make([]string, 0),
		SupersededIDs: make([]string, 0),
		Skipped:       map[string]int{},
		Applied:       criteria.Apply,
		OlderThan:     olderThan,
		Scanned:       len(summaries),
	}

	// Expire pass (oldest first).
	sort.SliceStable(summaries, func(i, j int) bool {
		if summaries[i].UpdatedAt().Equal(summaries[j].UpdatedAt()) {
			return summaries[i].MemoryID().String() < summaries[j].MemoryID().String()
		}
		return summaries[i].UpdatedAt().Before(summaries[j].UpdatedAt())
	})

	eligible := make([]apptypes.MemorySummary, 0)
	for _, s := range summaries {
		// Reconstruct eligibility without full aggregate: mirror domain rules.
		if s.Status() != domtypes.MemoryStatusCandidate {
			result.Skipped["not_candidate"]++
			continue
		}
		if !policy.AllowsSource(s.Source()) {
			result.Skipped["source"]++
			continue
		}
		cutoff := now.Add(-olderThan)
		if !s.UpdatedAt().UTC().Before(cutoff) {
			result.Skipped["too_new"]++
			continue
		}
		eligible = append(eligible, s)
	}

	remaining := 0
	if len(eligible) > limit {
		remaining = len(eligible) - limit
		eligible = eligible[:limit]
	}
	result.RemainingAfter = remaining

	for _, s := range eligible {
		result.ExpiredIDs = append(result.ExpiredIDs, s.MemoryID().String())
		if !criteria.Apply {
			continue
		}
		if _, err := u.Expire(ctx, s.MemoryID(), domtypes.Some(now)); err != nil {
			result.Skipped["expire_error"]++
			// Drop from expired list on failure.
			result.ExpiredIDs = result.ExpiredIDs[:len(result.ExpiredIDs)-1]
			continue
		}
	}

	if criteria.Dedupe {
		if err := u.decayDedupePass(ctx, criteria.Apply, &result); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (u *memoryUsecase) decayDedupePass(ctx context.Context, apply bool, result *apptypes.MemoryDecayResult) error {
	// Reload candidates for exact-key collapse.
	summaries, err := u.listCandidateSummariesForDecay(ctx, domtypes.DefaultDecaySources())
	if err != nil {
		return err
	}
	type group struct {
		items []apptypes.MemorySummary
	}
	groups := map[string]*group{}
	for _, s := range summaries {
		key := fmt.Sprintf("%s\x00%s\x00%s\x00%s", s.Scope().Kind().String(), s.Scope().Key(), s.MemoryType().String(), strings.TrimSpace(s.Fact()))
		g := groups[key]
		if g == nil {
			g = &group{}
			groups[key] = g
		}
		g.items = append(g.items, s)
	}
	for _, g := range groups {
		if len(g.items) < 2 {
			continue
		}
		sort.SliceStable(g.items, func(i, j int) bool {
			if g.items[i].UpdatedAt().Equal(g.items[j].UpdatedAt()) {
				return g.items[i].MemoryID().String() > g.items[j].MemoryID().String()
			}
			return g.items[i].UpdatedAt().After(g.items[j].UpdatedAt())
		})
		// Keep newest; supersede older.
		for _, s := range g.items[1:] {
			result.SupersededIDs = append(result.SupersededIDs, s.MemoryID().String())
			if !apply {
				continue
			}
			if err := u.supersedeCandidateDuplicate(ctx, s.MemoryID()); err != nil {
				result.Skipped["dedupe_error"]++
				result.SupersededIDs = result.SupersededIDs[:len(result.SupersededIDs)-1]
			}
		}
	}
	return nil
}

func (u *memoryUsecase) supersedeCandidateDuplicate(ctx context.Context, memoryID domtypes.MemoryID) error {
	memory, err := u.findMemoryByID(ctx, memoryID)
	if err != nil {
		return err
	}
	if err := memory.MarkCandidateSupersededByDuplicate(); err != nil {
		return err
	}
	if err := u.memoryRepo.Save(ctx, memory); err != nil {
		return xerrors.Errorf("failed to save superseded duplicate: %w", err)
	}
	return nil
}

func (u *memoryUsecase) listCandidateSummariesForDecay(ctx context.Context, sources []domtypes.MemorySource) ([]apptypes.MemorySummary, error) {
	const pageSize = 2000
	var all []apptypes.MemorySummary
	offset := 0
	for {
		builder := apptypes.NewMemoryListCriteriaBuilder(pageSize).
			Offset(offset).
			Statuses([]domtypes.MemoryStatus{domtypes.MemoryStatusCandidate})
		if len(sources) > 0 {
			builder = builder.Sources(sources)
		}
		page, err := u.memoryQuery.List(ctx, builder.Build())
		if err != nil {
			return nil, xerrors.Errorf("failed to list candidates for decay: %w", err)
		}
		if len(page) == 0 {
			break
		}
		all = append(all, page...)
		if len(page) < pageSize {
			break
		}
		offset += pageSize
	}
	return all, nil
}

// Restore returns an expired memory to candidate status.
func (u *memoryUsecase) Restore(ctx context.Context, memoryID domtypes.MemoryID) (apptypes.MemoryDetails, error) {
	if u.memoryRepo == nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("memory repository is not configured")
	}
	memory, err := u.findMemoryByID(ctx, memoryID)
	if err != nil {
		return apptypes.MemoryDetails{}, err
	}
	if err := memory.RestoreToCandidate(); err != nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("failed to restore memory: %w", err)
	}
	if err := u.memoryRepo.Save(ctx, memory); err != nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("failed to save restored memory: %w", err)
	}
	details, err := apptypes.MemoryDetailsFrom(memory)
	if err != nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("failed to build memory details: %w", err)
	}
	return details, nil
}
