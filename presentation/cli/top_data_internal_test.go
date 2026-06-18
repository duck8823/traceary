package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	domtypes "github.com/duck8823/traceary/domain/types"
)

// topDataSessionStub satisfies usecase.SessionUsecase by embedding the
// interface and overriding only the methods topDataLoader exercises.
// Calling any other method panics, which is exactly what we want from a
// test seam: it keeps the stub small while making accidental callers
// fail loudly.
type topDataSessionStub struct {
	usecase.SessionUsecase

	listResult   []apptypes.SessionSummary
	listErr      error
	listCriteria apptypes.SessionListCriteria
	listCalls    int

	lineageByID  map[domtypes.SessionID][]apptypes.SessionSummary
	lineageErr   error
	lineageCalls []domtypes.SessionID
}

func (s *topDataSessionStub) List(_ context.Context, criteria apptypes.SessionListCriteria) ([]apptypes.SessionSummary, error) {
	s.listCriteria = criteria
	s.listCalls++
	return s.listResult, s.listErr
}

func (s *topDataSessionStub) Lineage(_ context.Context, id domtypes.SessionID) ([]apptypes.SessionSummary, error) {
	s.lineageCalls = append(s.lineageCalls, id)
	if s.lineageErr != nil {
		return nil, s.lineageErr
	}
	if lineage, ok := s.lineageByID[id]; ok {
		return lineage, nil
	}
	return nil, nil
}

// topDataEventStub satisfies usecase.EventUsecase via the same embedded
// interface trick. Only List is exercised by topDataLoader.
type topDataEventStub struct {
	usecase.EventUsecase

	listEvents   []*model.Event
	listErr      error
	listCriteria apptypes.EventListCriteria
	listCalls    int
	showDetails  apptypes.EventDetails
	showErr      error
	showEventID  domtypes.EventID
	showCalls    int
}

func (s *topDataEventStub) List(_ context.Context, criteria apptypes.EventListCriteria) ([]*model.Event, error) {
	s.listCriteria = criteria
	s.listCalls++
	return s.listEvents, s.listErr
}

func (s *topDataEventStub) Show(_ context.Context, eventID domtypes.EventID) (apptypes.EventDetails, error) {
	s.showEventID = eventID
	s.showCalls++
	return s.showDetails, s.showErr
}

// topDataMemoryStub satisfies usecase.MemoryUsecase via the embedded
// interface trick. Tests opt into List/ListStale/Show behavior per surface.
type topDataMemoryStub struct {
	usecase.MemoryUsecase

	listResult          []apptypes.MemorySummary
	listFunc            func(apptypes.MemoryListCriteria) ([]apptypes.MemorySummary, error)
	listErr             error
	listCriteria        apptypes.MemoryListCriteria
	listCriteriaCalls   []apptypes.MemoryListCriteria
	listCalls           int
	staleResult         apptypes.StaleMemoryListResult
	staleErr            error
	staleMemoryCriteria apptypes.StaleMemoryListCriteria
	staleMemoryCalls    int
	showDetails         apptypes.MemoryDetails
	showErr             error
	showMemoryID        domtypes.MemoryID
	showCalls           int
	countResult         apptypes.MemoryStatusCounts
	countErr            error
	countCalls          int
	countFunc           func(apptypes.MemoryListCriteria) (apptypes.MemoryStatusCounts, error)
}

func (s *topDataMemoryStub) List(_ context.Context, criteria apptypes.MemoryListCriteria) ([]apptypes.MemorySummary, error) {
	s.listCriteria = criteria
	s.listCriteriaCalls = append(s.listCriteriaCalls, criteria)
	s.listCalls++
	if s.listFunc != nil {
		return s.listFunc(criteria)
	}
	return s.listResult, s.listErr
}

func (s *topDataMemoryStub) ListStale(_ context.Context, criteria apptypes.StaleMemoryListCriteria) (apptypes.StaleMemoryListResult, error) {
	s.staleMemoryCriteria = criteria
	s.staleMemoryCalls++
	return s.staleResult, s.staleErr
}

func (s *topDataMemoryStub) Show(_ context.Context, memoryID domtypes.MemoryID) (apptypes.MemoryDetails, error) {
	s.showMemoryID = memoryID
	s.showCalls++
	return s.showDetails, s.showErr
}

func (s *topDataMemoryStub) CountByStatus(_ context.Context, criteria apptypes.MemoryListCriteria) (apptypes.MemoryStatusCounts, error) {
	s.countCalls++
	if s.countFunc != nil {
		return s.countFunc(criteria)
	}
	return s.countResult, s.countErr
}

// TestTopDataLoader_LoadSnapshot_UsesTrueCountsWhenScanSaturated guards #1111:
// when the bounded reliability scan saturates (returns the cap), the snapshot's
// accepted/candidate totals come from the true CountByStatus query rather than
// the capped scan count.
func TestTopDataLoader_LoadSnapshot_UsesTrueCountsWhenScanSaturated(t *testing.T) {
	t.Parallel()

	now := fixedStartedAt.Add(48 * time.Hour)
	candidate := memorySummaryWithUpdatedAt(t, "mem-candidate", domtypes.MemoryStatusCandidate, now.Add(-time.Hour))
	saturated := make([]apptypes.MemorySummary, 0, topReliabilityMemoryScanLimit)
	for i := 0; i < topReliabilityMemoryScanLimit; i++ {
		saturated = append(saturated, candidate)
	}
	memory := &topDataMemoryStub{
		listResult:  saturated,
		countResult: apptypes.MemoryStatusCounts{Accepted: 7, Candidate: 5000},
	}
	loader := newTopDataLoader(nil, nil, memory)

	snap, err := loader.loadSnapshot(context.Background(), topDataCriteria{
		CandidateLimit:   1,
		StaleMemoryLimit: 1,
		StaleAfter:       24 * time.Hour,
		Now:              now,
	})
	if err != nil {
		t.Fatalf("loadSnapshot() error = %v", err)
	}

	if !snap.Reliability.MemoryScanLimited {
		t.Fatalf("MemoryScanLimited = false, want true when the scan returns the cap")
	}
	if memory.countCalls != 1 {
		t.Fatalf("CountByStatus calls = %d, want 1 when the scan saturated", memory.countCalls)
	}
	if got, want := snap.Reliability.CandidateMemoryCount, 5000; got != want {
		t.Fatalf("CandidateMemoryCount = %d, want %d (true count, not the capped scan)", got, want)
	}
	if got, want := snap.Reliability.AcceptedMemoryCount, 7; got != want {
		t.Fatalf("AcceptedMemoryCount = %d, want %d (true count)", got, want)
	}
}

// TestTopDataLoader_LoadSnapshot_HygieneUsesCandidateOnlyScanWhenSaturated
// guards #1169 (Codex round-6): when the mixed accepted+candidate scan
// saturates on newer accepted rows, the candidate hygiene summary must come
// from a bounded candidate-only scan rather than the starved mixed sample, so a
// nonzero candidate_count is never reported with all-zero hygiene.
func TestTopDataLoader_LoadSnapshot_HygieneUsesCandidateOnlyScanWhenSaturated(t *testing.T) {
	t.Parallel()

	now := fixedStartedAt.Add(48 * time.Hour)
	// The mixed scan saturates entirely on accepted rows; no candidate would
	// reach the hygiene sample if it reused this slice.
	accepted := memorySummaryWithUpdatedAt(t, "mem-accepted", domtypes.MemoryStatusAccepted, now.Add(-time.Hour))
	saturatedAccepted := make([]apptypes.MemorySummary, 0, topReliabilityMemoryScanLimit)
	for i := 0; i < topReliabilityMemoryScanLimit; i++ {
		saturatedAccepted = append(saturatedAccepted, accepted)
	}
	// The candidate-only scan returns real candidates with hygiene signals: one
	// fresh actionable note and one stale note.
	candidateRows := []apptypes.MemorySummary{
		memorySummaryWithFactAndUpdatedAt(t, "cand-fresh", domtypes.MemoryStatusCandidate, "Prefer table-driven tests for new code", now.Add(-time.Hour)),
		memorySummaryWithFactAndUpdatedAt(t, "cand-stale", domtypes.MemoryStatusCandidate, "Use cmp.Diff for assertions", now.Add(-30*24*time.Hour)),
	}

	var hygieneScanCalls int
	memory := &topDataMemoryStub{
		countResult: apptypes.MemoryStatusCounts{Accepted: topReliabilityMemoryScanLimit, Candidate: 2},
		listFunc: func(criteria apptypes.MemoryListCriteria) ([]apptypes.MemorySummary, error) {
			statuses := criteria.Statuses()
			candidateOnly := len(statuses) == 1 && statuses[0] == domtypes.MemoryStatusCandidate
			switch {
			case candidateOnly && !criteria.RememberIntentPriority():
				// Reliability candidate-only hygiene scan (no remember-intent
				// ordering, unlike the candidate pane's loadCandidates scan).
				hygieneScanCalls++
				return candidateRows, nil
			case candidateOnly:
				// Candidate pane (loadCandidates) — also candidate-only but
				// remember-intent prioritised.
				return candidateRows, nil
			default:
				// Mixed reliability scan on [Accepted, Candidate].
				return saturatedAccepted, nil
			}
		},
	}
	loader := newTopDataLoader(nil, nil, memory)

	snap, err := loader.loadSnapshot(context.Background(), topDataCriteria{
		CandidateLimit:   1,
		StaleMemoryLimit: 1,
		StaleAfter:       24 * time.Hour,
		Now:              now,
	})
	if err != nil {
		t.Fatalf("loadSnapshot() error = %v", err)
	}

	if !snap.Reliability.MemoryScanLimited {
		t.Fatalf("MemoryScanLimited = false, want true when the mixed scan returns the cap")
	}
	if hygieneScanCalls != 1 {
		t.Fatalf("candidate-only hygiene scan calls = %d, want 1 when the mixed scan saturated", hygieneScanCalls)
	}
	if got, want := snap.Reliability.CandidateMemoryCount, 2; got != want {
		t.Fatalf("CandidateMemoryCount = %d, want %d (true count)", got, want)
	}
	hygiene := snap.Reliability.CandidateHygiene
	if hygiene.LikelyActionable != 1 {
		t.Fatalf("CandidateHygiene.LikelyActionable = %d, want 1 (from candidate-only scan, not the starved mixed sample)", hygiene.LikelyActionable)
	}
	if hygiene.Stale != 1 {
		t.Fatalf("CandidateHygiene.Stale = %d, want 1 (from candidate-only scan)", hygiene.Stale)
	}
	total := hygiene.Stale + hygiene.Duplicate + hygiene.FragmentLike + hygiene.ExtractedHidden + hygiene.LikelyActionable
	if total == 0 {
		t.Fatalf("CandidateHygiene all zero with CandidateMemoryCount=%d — hygiene was starved by the mixed scan", snap.Reliability.CandidateMemoryCount)
	}
}

// fixedStartedAt is the deterministic anchor every fixture in this file
// derives session/event timestamps from. Pinning a single instant keeps
// table-driven assertions readable without `time.Now()` drift.
var fixedStartedAt = time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)

func sessionSummaryFixture(id string, parent string, started time.Time, status string, latestKind domtypes.EventKind, latestMessage string) apptypes.SessionSummary {
	endedAt := domtypes.None[time.Time]()
	if status == "ended" {
		endedAt = domtypes.Some(started.Add(30 * time.Minute))
	}
	return apptypes.SessionSummaryOf(
		domtypes.SessionID(id),
		domtypes.Workspace("duck8823/traceary"),
		started,
		endedAt,
		status,
		7,
		2,
		[]string{"claude"},
		"",
		"",
		domtypes.SessionID(parent),
		domtypes.Client("claude"),
		started.Add(5*time.Minute),
		apptypes.SessionSummaryLatestEventOf(latestKind, latestMessage),
	)
}

func TestTopDataLoader_LoadSessions_BuildsActiveTreeAndForwardsCriteria(t *testing.T) {
	t.Parallel()

	root := sessionSummaryFixture("root", "", fixedStartedAt, "ended", domtypes.EventKindSessionEnded, "root ended")
	child := sessionSummaryFixture("child", "root", fixedStartedAt.Add(10*time.Minute), "active", domtypes.EventKindTranscript, "active child")

	session := &topDataSessionStub{
		listResult: []apptypes.SessionSummary{child},
		lineageByID: map[domtypes.SessionID][]apptypes.SessionSummary{
			domtypes.SessionID("child"): {root, child},
		},
	}
	loader := newTopDataLoader(session, nil, nil)

	roots, err := loader.loadSessions(context.Background(), topDataCriteria{
		Workspace:    "duck8823/traceary",
		Client:       "claude",
		Agent:        "claude",
		SessionLimit: 50,
	})
	if err != nil {
		t.Fatalf("loadSessions() error = %v", err)
	}

	if got, want := session.listCriteria.Limit(), 50; got != want {
		t.Fatalf("List criteria Limit = %d, want %d", got, want)
	}
	if !session.listCriteria.ActiveOnly() {
		t.Fatalf("List criteria ActiveOnly = false, want true")
	}
	if got, want := session.listCriteria.Workspace().String(), "duck8823/traceary"; got != want {
		t.Fatalf("List criteria Workspace = %q, want %q", got, want)
	}
	if got, want := session.listCriteria.Client().String(), "claude"; got != want {
		t.Fatalf("List criteria Client = %q, want %q", got, want)
	}
	if got, want := session.listCriteria.Agent().String(), "claude"; got != want {
		t.Fatalf("List criteria Agent = %q, want %q", got, want)
	}

	if len(roots) != 1 {
		t.Fatalf("roots length = %d, want 1 (ended root retained because the active child references it)", len(roots))
	}
	if got, want := roots[0].summary.SessionID().String(), "root"; got != want {
		t.Fatalf("root[0].SessionID = %q, want %q", got, want)
	}
	if len(roots[0].children) != 1 {
		t.Fatalf("root[0].children length = %d, want 1", len(roots[0].children))
	}
	if got, want := roots[0].children[0].summary.SessionID().String(), "child"; got != want {
		t.Fatalf("root[0].children[0].SessionID = %q, want %q", got, want)
	}
}

func TestTopDataLoader_LoadSessions_NonPositiveLimitIsNoOp(t *testing.T) {
	t.Parallel()

	session := &topDataSessionStub{
		listResult: []apptypes.SessionSummary{
			sessionSummaryFixture("only", "", fixedStartedAt, "active", domtypes.EventKindNote, "noop"),
		},
	}
	loader := newTopDataLoader(session, nil, nil)

	roots, err := loader.loadSessions(context.Background(), topDataCriteria{SessionLimit: 0})
	if err != nil {
		t.Fatalf("loadSessions() error = %v", err)
	}
	if roots != nil {
		t.Fatalf("roots = %v, want nil when SessionLimit <= 0", roots)
	}
	if session.listCalls != 0 {
		t.Fatalf("session.List calls = %d, want 0", session.listCalls)
	}
}

func TestTopDataLoader_LoadSessions_ListErrorWrapsLocalizedMessage(t *testing.T) {
	t.Parallel()

	session := &topDataSessionStub{listErr: errors.New("boom")}
	loader := newTopDataLoader(session, nil, nil)

	_, err := loader.loadSessions(context.Background(), topDataCriteria{SessionLimit: 10})
	if err == nil {
		t.Fatalf("loadSessions() error = nil, want wrapped list error")
	}
	if !errors.Is(err, session.listErr) {
		t.Fatalf("loadSessions() error chain missing underlying boom: %v", err)
	}
}

func TestTopDataLoader_LoadSessions_StaleActiveDroppedByDefault(t *testing.T) {
	t.Parallel()

	now := fixedStartedAt.Add(48 * time.Hour)
	fresh := sessionSummaryFixture("fresh", "", now.Add(-time.Hour), "active", domtypes.EventKindTranscript, "fresh")
	stale := sessionSummaryFixture("stale", "", now.Add(-30*time.Hour), "stale", domtypes.EventKindTranscript, "stale")

	session := &topDataSessionStub{
		listResult: []apptypes.SessionSummary{fresh, stale},
		lineageByID: map[domtypes.SessionID][]apptypes.SessionSummary{
			domtypes.SessionID("fresh"): {fresh},
			domtypes.SessionID("stale"): {stale},
		},
	}
	loader := newTopDataLoader(session, nil, nil)

	roots, err := loader.loadSessions(context.Background(), topDataCriteria{
		SessionLimit: 50,
		StaleAfter:   24 * time.Hour,
		Now:          now,
	})
	if err != nil {
		t.Fatalf("loadSessions() error = %v", err)
	}
	if len(roots) != 1 {
		t.Fatalf("roots length = %d, want 1 (stale active session must be dropped)", len(roots))
	}
	if got, want := roots[0].summary.SessionID().String(), "fresh"; got != want {
		t.Fatalf("retained root = %q, want %q", got, want)
	}
}

func TestTopDataLoader_LoadSessions_AllowStaleKeepsStaleActive(t *testing.T) {
	t.Parallel()

	now := fixedStartedAt.Add(48 * time.Hour)
	stale := sessionSummaryFixture("stale", "", now.Add(-30*time.Hour), "stale", domtypes.EventKindTranscript, "stale")

	session := &topDataSessionStub{
		listResult: []apptypes.SessionSummary{stale},
		lineageByID: map[domtypes.SessionID][]apptypes.SessionSummary{
			domtypes.SessionID("stale"): {stale},
		},
	}
	loader := newTopDataLoader(session, nil, nil)

	roots, err := loader.loadSessions(context.Background(), topDataCriteria{
		SessionLimit: 50,
		StaleAfter:   24 * time.Hour,
		AllowStale:   true,
		Now:          now,
	})
	if err != nil {
		t.Fatalf("loadSessions() error = %v", err)
	}
	if len(roots) != 1 {
		t.Fatalf("roots length = %d, want 1 (--allow-stale opts the stale active back in)", len(roots))
	}
}

func TestTopDataLoader_LoadSessions_DisabledStaleAfterKeepsStaleStatus(t *testing.T) {
	t.Parallel()

	now := fixedStartedAt.Add(48 * time.Hour)
	stale := sessionSummaryFixture("stale", "", now.Add(-30*time.Hour), "stale", domtypes.EventKindTranscript, "stale")

	session := &topDataSessionStub{
		listResult: []apptypes.SessionSummary{stale},
		lineageByID: map[domtypes.SessionID][]apptypes.SessionSummary{
			domtypes.SessionID("stale"): {stale},
		},
	}
	loader := newTopDataLoader(session, nil, nil)

	roots, err := loader.loadSessions(context.Background(), topDataCriteria{
		SessionLimit: 50,
		StaleAfter:   0,
		Now:          now,
	})
	if err != nil {
		t.Fatalf("loadSessions() error = %v", err)
	}
	if len(roots) != 1 {
		t.Fatalf("roots length = %d, want 1 (--stale-after=0 disables stale filtering)", len(roots))
	}
}

func TestTopDataLoader_LoadSessions_CustomStaleAfterKeepsWithinThreshold(t *testing.T) {
	t.Parallel()

	now := fixedStartedAt.Add(48 * time.Hour)
	staleByStoreDefault := sessionSummaryFixture("store-stale", "", now.Add(-30*time.Hour), "stale", domtypes.EventKindTranscript, "within custom threshold")

	session := &topDataSessionStub{
		listResult: []apptypes.SessionSummary{staleByStoreDefault},
		lineageByID: map[domtypes.SessionID][]apptypes.SessionSummary{
			domtypes.SessionID("store-stale"): {staleByStoreDefault},
		},
	}
	loader := newTopDataLoader(session, nil, nil)

	roots, err := loader.loadSessions(context.Background(), topDataCriteria{
		SessionLimit: 50,
		StaleAfter:   48 * time.Hour,
		Now:          now,
	})
	if err != nil {
		t.Fatalf("loadSessions() error = %v", err)
	}
	if len(roots) != 1 {
		t.Fatalf("roots length = %d, want 1 (30h-old store-stale session is within --stale-after=48h)", len(roots))
	}
	if got, want := roots[0].summary.SessionID().String(), "store-stale"; got != want {
		t.Fatalf("retained root = %q, want %q", got, want)
	}
}

func TestTopDataLoader_LoadSessions_EndedNeverConsideredStale(t *testing.T) {
	t.Parallel()

	now := fixedStartedAt.Add(48 * time.Hour)
	// `ended` sessions are filtered out by ActiveOnly upstream, but the
	// staleness predicate is shared with the JSON encoder which iterates
	// over all summaries. Guard against accidentally tagging an ended
	// session whose start happens to be older than 24h.
	endedOld := apptypes.SessionSummaryOf(
		domtypes.SessionID("ended-old"),
		domtypes.Workspace("duck8823/traceary"),
		now.Add(-72*time.Hour),
		domtypes.Some(now.Add(-1*time.Hour)),
		"ended",
		1, 0, []string{"claude"}, "", "", domtypes.SessionID(""),
	)
	if topDataSummaryIsStale(endedOld, 24*time.Hour, now) {
		t.Fatalf("ended session must not be reported as stale")
	}
}

func TestWriteTopSnapshotJSON_EmitsStaleActiveMetadata(t *testing.T) {
	t.Parallel()

	now := fixedStartedAt.Add(48 * time.Hour)
	stale := sessionSummaryFixture("stale", "", now.Add(-30*time.Hour), "stale", domtypes.EventKindTranscript, "stale")
	var buf bytes.Buffer
	if err := writeTopSnapshotJSON(&buf, topDataSnapshot{
		Sessions:   []*sessionNode{{summary: stale}},
		StaleAfter: 24 * time.Hour,
		AllowStale: true,
		Now:        now,
	}); err != nil {
		t.Fatalf("writeTopSnapshotJSON() error = %v", err)
	}

	var payload struct {
		Sessions []struct {
			SessionID        string   `json:"session_id"`
			Status           string   `json:"status"`
			IsStale          bool     `json:"is_stale"`
			StaleAfterSec    *float64 `json:"stale_after_seconds"`
			StaleAgeSec      *float64 `json:"stale_age_seconds"`
			LatestEventAt    string   `json:"latest_event_at"`
			LatestEventKind  string   `json:"latest_event_kind"`
			LatestEventMesg  string   `json:"latest_event_message"`
			LegacyTotalEvent int      `json:"total_events"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal(top snapshot) error = %v\n%s", err, buf.String())
	}
	if len(payload.Sessions) != 1 {
		t.Fatalf("sessions length = %d, want 1; payload=%s", len(payload.Sessions), buf.String())
	}
	got := payload.Sessions[0]
	if got.SessionID != "stale" || got.Status != "stale" {
		t.Fatalf("stale node id/status = %q/%q, want stale/stale", got.SessionID, got.Status)
	}
	if !got.IsStale {
		t.Fatalf("is_stale = false, want true; payload=%s", buf.String())
	}
	if got.StaleAfterSec == nil || *got.StaleAfterSec != (24*time.Hour).Seconds() {
		t.Fatalf("stale_after_seconds = %v, want %v", got.StaleAfterSec, (24 * time.Hour).Seconds())
	}
	if got.StaleAgeSec == nil || *got.StaleAgeSec != (6*time.Hour).Seconds() {
		t.Fatalf("stale_age_seconds = %v, want %v", got.StaleAgeSec, (6 * time.Hour).Seconds())
	}
	if got.LatestEventAt == "" || got.LatestEventKind == "" || got.LatestEventMesg == "" || got.LegacyTotalEvent == 0 {
		t.Fatalf("legacy top JSON fields were not preserved: %+v", got)
	}
}

func TestTopDataLoader_LoadFailures_BuildsCriteriaAndReturnsEvents(t *testing.T) {
	t.Parallel()

	failure := mustEvent(t, "evt-fail", domtypes.EventKindCommandExecuted, "go test ./... [exit=1]")
	event := &topDataEventStub{listEvents: []*model.Event{failure}}
	loader := newTopDataLoader(nil, event, nil)

	got, err := loader.loadFailures(context.Background(), topDataCriteria{
		Workspace:    "duck8823/traceary",
		Client:       "claude",
		Agent:        "claude",
		FailureLimit: 5,
	})
	if err != nil {
		t.Fatalf("loadFailures() error = %v", err)
	}
	if len(got) != 1 || got[0].EventID().String() != "evt-fail" {
		t.Fatalf("loadFailures() = %#v, want one evt-fail event", got)
	}

	if got, want := event.listCriteria.Limit(), 5; got != want {
		t.Fatalf("List criteria Limit = %d, want %d", got, want)
	}
	if !event.listCriteria.FailuresOnly() {
		t.Fatalf("List criteria FailuresOnly = false, want true")
	}
	if got, want := event.listCriteria.Workspace().String(), "duck8823/traceary"; got != want {
		t.Fatalf("List criteria Workspace = %q, want %q", got, want)
	}
}

func TestTopDataLoader_LoadFailures_ZeroLimitIsNoOp(t *testing.T) {
	t.Parallel()

	event := &topDataEventStub{}
	loader := newTopDataLoader(nil, event, nil)

	got, err := loader.loadFailures(context.Background(), topDataCriteria{FailureLimit: 0})
	if err != nil {
		t.Fatalf("loadFailures() error = %v", err)
	}
	if got != nil {
		t.Fatalf("loadFailures() = %v, want nil when FailureLimit <= 0", got)
	}
	if event.listCalls != 0 {
		t.Fatalf("event.List calls = %d, want 0", event.listCalls)
	}
}

func TestTopDataLoader_LoadRecentCommands_FiltersByCommandKind(t *testing.T) {
	t.Parallel()

	cmd := mustEvent(t, "evt-cmd", domtypes.EventKindCommandExecuted, "ls -la")
	event := &topDataEventStub{listEvents: []*model.Event{cmd}}
	loader := newTopDataLoader(nil, event, nil)

	got, err := loader.loadRecentCommands(context.Background(), topDataCriteria{RecentCommandLimit: 3})
	if err != nil {
		t.Fatalf("loadRecentCommands() error = %v", err)
	}
	if len(got) != 1 || got[0].EventID().String() != "evt-cmd" {
		t.Fatalf("loadRecentCommands() = %#v, want one evt-cmd event", got)
	}

	if got, want := event.listCriteria.Kind(), domtypes.EventKindCommandExecuted; got != want {
		t.Fatalf("List criteria Kind = %q, want %q", got, want)
	}
	if event.listCriteria.FailuresOnly() {
		t.Fatalf("List criteria FailuresOnly = true, want false (recent commands include successes)")
	}
}

func TestTopDataLoader_LoadCandidates_FiltersByCandidateStatus(t *testing.T) {
	t.Parallel()

	workspace, err := domtypes.WorkspaceFrom("duck8823/traceary")
	if err != nil {
		t.Fatalf("WorkspaceFrom: %v", err)
	}
	candidate, err := apptypes.MemorySummaryOf(
		domtypes.MemoryID("mem-1"),
		domtypes.MemoryTypePreference,
		domtypes.WorkspaceScopeOf(workspace),
		"prefer table-driven subtests",
		domtypes.MemoryStatusCandidate,
		domtypes.ConfidenceMedium,
		domtypes.MemorySourceManual,
		domtypes.None[domtypes.MemoryID](),
		domtypes.None[time.Time](),
		fixedStartedAt,
		domtypes.None[time.Time](),
		fixedStartedAt,
		fixedStartedAt,
	)
	if err != nil {
		t.Fatalf("MemorySummaryOf: %v", err)
	}
	memory := &topDataMemoryStub{listResult: []apptypes.MemorySummary{candidate}}
	loader := newTopDataLoader(nil, nil, memory)

	got, err := loader.loadCandidates(context.Background(), topDataCriteria{CandidateLimit: 4})
	if err != nil {
		t.Fatalf("loadCandidates() error = %v", err)
	}
	if len(got) != 1 || got[0].MemoryID().String() != "mem-1" {
		t.Fatalf("loadCandidates() = %#v, want one mem-1 candidate", got)
	}

	statuses := memory.listCriteria.Statuses()
	if len(statuses) != 1 || statuses[0] != domtypes.MemoryStatusCandidate {
		t.Fatalf("List criteria Statuses = %v, want [candidate]", statuses)
	}
	if !memory.listCriteria.RememberIntentPriority() {
		t.Fatalf("List criteria RememberIntentPriority = false, want true (matches inbox ordering)")
	}
	if got, want := memory.listCriteria.Limit(), 4; got != want {
		t.Fatalf("List criteria Limit = %d, want %d", got, want)
	}
	if scopes := memory.listCriteria.Scopes(); len(scopes) != 0 {
		t.Fatalf("List criteria Scopes = %v, want none when Workspace/Agent are unset", scopes)
	}
}

func TestCountCandidatesBySource_RememberIntent(t *testing.T) {
	t.Parallel()

	workspace, err := domtypes.WorkspaceFrom("duck8823/traceary")
	if err != nil {
		t.Fatalf("WorkspaceFrom: %v", err)
	}
	makeCandidate := func(t *testing.T, id string, source domtypes.MemorySource) apptypes.MemorySummary {
		t.Helper()
		summary, err := apptypes.MemorySummaryOf(
			domtypes.MemoryID(id),
			domtypes.MemoryTypePreference,
			domtypes.WorkspaceScopeOf(workspace),
			"remember this fact",
			domtypes.MemoryStatusCandidate,
			domtypes.ConfidenceMedium,
			source,
			domtypes.None[domtypes.MemoryID](),
			domtypes.None[time.Time](),
			fixedStartedAt,
			domtypes.None[time.Time](),
			fixedStartedAt,
			fixedStartedAt,
		)
		if err != nil {
			t.Fatalf("MemorySummaryOf: %v", err)
		}
		return summary
	}

	candidates := []apptypes.MemorySummary{
		makeCandidate(t, "mem-remember", domtypes.MemorySourceRememberIntent),
		makeCandidate(t, "mem-extracted", domtypes.MemorySourceExtracted),
	}
	if got := countCandidatesBySource(candidates, domtypes.MemorySourceRememberIntent); got != 1 {
		t.Fatalf("remember-intent count = %d, want 1", got)
	}
}

func TestTopDataLoader_LoadCandidates_WorkspaceAndAgentAddScopes(t *testing.T) {
	t.Parallel()

	memory := &topDataMemoryStub{}
	loader := newTopDataLoader(nil, nil, memory)

	if _, err := loader.loadCandidates(context.Background(), topDataCriteria{
		Workspace:      "  duck8823/traceary  ",
		Client:         "claude",
		Agent:          "  claude  ",
		CandidateLimit: 4,
	}); err != nil {
		t.Fatalf("loadCandidates() error = %v", err)
	}

	scopes := memory.listCriteria.Scopes()
	if len(scopes) != 2 {
		t.Fatalf("List criteria Scopes length = %d, want 2 (workspace + agent)", len(scopes))
	}
	workspaceScope, ok := scopes[0].(domtypes.WorkspaceScope)
	if !ok {
		t.Fatalf("scopes[0] = %T, want WorkspaceScope", scopes[0])
	}
	if got, want := workspaceScope.Workspace().String(), "duck8823/traceary"; got != want {
		t.Fatalf("scopes[0].Workspace = %q, want %q (trimmed)", got, want)
	}
	agentScope, ok := scopes[1].(domtypes.AgentScope)
	if !ok {
		t.Fatalf("scopes[1] = %T, want AgentScope", scopes[1])
	}
	if got, want := agentScope.Agent().String(), "claude"; got != want {
		t.Fatalf("scopes[1].Agent = %q, want %q (trimmed)", got, want)
	}
	statuses := memory.listCriteria.Statuses()
	if len(statuses) != 1 || statuses[0] != domtypes.MemoryStatusCandidate {
		t.Fatalf("List criteria Statuses = %v, want [candidate] when scoped", statuses)
	}
	if !memory.listCriteria.RememberIntentPriority() {
		t.Fatalf("List criteria RememberIntentPriority = false, want true when scoped")
	}
}

func TestTopDataLoader_LoadCandidates_WorkspaceOnlyScope(t *testing.T) {
	t.Parallel()

	memory := &topDataMemoryStub{}
	loader := newTopDataLoader(nil, nil, memory)

	if _, err := loader.loadCandidates(context.Background(), topDataCriteria{
		Workspace:      "duck8823/traceary",
		CandidateLimit: 4,
	}); err != nil {
		t.Fatalf("loadCandidates() error = %v", err)
	}

	scopes := memory.listCriteria.Scopes()
	if len(scopes) != 1 {
		t.Fatalf("List criteria Scopes length = %d, want 1 (workspace only)", len(scopes))
	}
	if _, ok := scopes[0].(domtypes.WorkspaceScope); !ok {
		t.Fatalf("scopes[0] = %T, want WorkspaceScope", scopes[0])
	}
}

func TestTopDataLoader_LoadCandidates_AgentOnlyScope(t *testing.T) {
	t.Parallel()

	memory := &topDataMemoryStub{}
	loader := newTopDataLoader(nil, nil, memory)

	if _, err := loader.loadCandidates(context.Background(), topDataCriteria{
		Agent:          "claude",
		CandidateLimit: 4,
	}); err != nil {
		t.Fatalf("loadCandidates() error = %v", err)
	}

	scopes := memory.listCriteria.Scopes()
	if len(scopes) != 1 {
		t.Fatalf("List criteria Scopes length = %d, want 1 (agent only)", len(scopes))
	}
	if _, ok := scopes[0].(domtypes.AgentScope); !ok {
		t.Fatalf("scopes[0] = %T, want AgentScope", scopes[0])
	}
}

func TestTopDataLoader_LoadCandidates_ClientDoesNotAddScope(t *testing.T) {
	t.Parallel()

	memory := &topDataMemoryStub{}
	loader := newTopDataLoader(nil, nil, memory)

	if _, err := loader.loadCandidates(context.Background(), topDataCriteria{
		Client:         "claude",
		CandidateLimit: 4,
	}); err != nil {
		t.Fatalf("loadCandidates() error = %v", err)
	}

	if scopes := memory.listCriteria.Scopes(); len(scopes) != 0 {
		t.Fatalf("List criteria Scopes = %v, want none when only Client is set (client has no memory scope equivalent)", scopes)
	}
}

func TestTopDataLoader_LoadStaleMemories_ForwardsScopesAndLimit(t *testing.T) {
	t.Parallel()

	staleSummary := memorySummaryFixture(t, "mem-stale", domtypes.MemoryStatusSuperseded, "stale fixture")
	staleRow, err := apptypes.StaleMemoryRowOf(staleSummary, apptypes.StaleMemoryReasonSuperseded)
	if err != nil {
		t.Fatalf("StaleMemoryRowOf: %v", err)
	}
	staleResult, err := apptypes.StaleMemoryListResultOf(7, []apptypes.StaleMemoryRow{staleRow})
	if err != nil {
		t.Fatalf("StaleMemoryListResultOf: %v", err)
	}
	memory := &topDataMemoryStub{staleResult: staleResult}
	loader := newTopDataLoader(nil, nil, memory)

	got, err := loader.loadStaleMemories(context.Background(), topDataCriteria{
		Workspace:        "  duck8823/traceary  ",
		Client:           "claude",
		Agent:            "  codex  ",
		StaleMemoryLimit: 4,
	})
	if err != nil {
		t.Fatalf("loadStaleMemories() error = %v", err)
	}
	if got.Count() != 7 {
		t.Fatalf("loadStaleMemories().Count = %d, want 7", got.Count())
	}
	if items := got.Items(); len(items) != 1 || items[0].Summary().MemoryID().String() != "mem-stale" {
		t.Fatalf("loadStaleMemories().Items = %#v, want one mem-stale", items)
	}
	if got, want := memory.staleMemoryCriteria.Limit(), 4; got != want {
		t.Fatalf("ListStale criteria Limit = %d, want %d", got, want)
	}
	scopes := memory.staleMemoryCriteria.Scopes()
	if len(scopes) != 2 {
		t.Fatalf("ListStale criteria Scopes length = %d, want 2 (workspace + agent)", len(scopes))
	}
	workspaceScope, ok := scopes[0].(domtypes.WorkspaceScope)
	if !ok {
		t.Fatalf("scopes[0] = %T, want WorkspaceScope", scopes[0])
	}
	if got, want := workspaceScope.Workspace().String(), "duck8823/traceary"; got != want {
		t.Fatalf("scopes[0].Workspace = %q, want %q", got, want)
	}
	agentScope, ok := scopes[1].(domtypes.AgentScope)
	if !ok {
		t.Fatalf("scopes[1] = %T, want AgentScope", scopes[1])
	}
	if got, want := agentScope.Agent().String(), "codex"; got != want {
		t.Fatalf("scopes[1].Agent = %q, want %q", got, want)
	}
}

func TestTopDataLoader_LoadStaleMemories_ZeroLimitIsNoOp(t *testing.T) {
	t.Parallel()

	memory := &topDataMemoryStub{}
	loader := newTopDataLoader(nil, nil, memory)

	got, err := loader.loadStaleMemories(context.Background(), topDataCriteria{StaleMemoryLimit: 0})
	if err != nil {
		t.Fatalf("loadStaleMemories() error = %v", err)
	}
	if got.Count() != 0 || len(got.Items()) != 0 {
		t.Fatalf("loadStaleMemories() = %#v, want empty result when StaleMemoryLimit <= 0", got)
	}
	if memory.staleMemoryCalls != 0 {
		t.Fatalf("memory.ListStale calls = %d, want 0", memory.staleMemoryCalls)
	}
}

func TestTopDataLoader_LoadDetail_SessionUsesLineageAndRecentEvents(t *testing.T) {
	t.Parallel()

	root := sessionSummaryFixture("root", "", fixedStartedAt, "ended", domtypes.EventKindSessionEnded, "root ended")
	child := sessionSummaryFixture("child", "root", fixedStartedAt.Add(10*time.Minute), "active", domtypes.EventKindTranscript, "child active")
	event := mustEvent(t, "evt-session", domtypes.EventKindTranscript, "first session event line\nsecond session event line")
	session := &topDataSessionStub{lineageByID: map[domtypes.SessionID][]apptypes.SessionSummary{
		domtypes.SessionID("child"): {root, child},
	}}
	events := &topDataEventStub{listEvents: []*model.Event{event}}
	loader := newTopDataLoader(session, events, nil)

	got, err := loader.loadDetail(context.Background(), topDetailRequest{
		target: topDetailTarget{kind: topDetailSession, title: "SESSION child", sessionID: domtypes.SessionID("child")},
	})
	if err != nil {
		t.Fatalf("loadDetail(session) error = %v", err)
	}
	joined := strings.Join(got.lines, "\n")
	for _, expect := range []string{"SESSION_ID: child", "LINEAGE:", "root", "child", "RECENT_EVENTS", "first session event line", "  second session event line"} {
		if !strings.Contains(joined, expect) {
			t.Fatalf("session detail missing %q:\n%s", expect, joined)
		}
	}
	for _, line := range got.lines {
		if strings.Contains(line, "first session event line") && strings.Contains(line, "second session event line") {
			t.Fatalf("multiline event body should be split into modal rows, got combined line %q", line)
		}
	}
	if len(session.lineageCalls) != 1 || session.lineageCalls[0] != "child" {
		t.Fatalf("Lineage calls = %#v, want child", session.lineageCalls)
	}
	if events.listCriteria.SessionID() != "child" || events.listCriteria.Limit() != topDetailRecentEventLimit {
		t.Fatalf("event List criteria = session %q limit %d, want child/%d", events.listCriteria.SessionID(), events.listCriteria.Limit(), topDetailRecentEventLimit)
	}
}

func TestTopDataLoader_LoadDetail_EventUsesShowAndFormatsAudit(t *testing.T) {
	t.Parallel()

	event := mustEvent(t, "evt-detail", domtypes.EventKindCommandExecuted, "full event body")
	audit := model.CommandAuditOf(
		event.EventID(),
		"go test ./...",
		"stdin payload",
		"stdout payload",
		false,
		false,
		domtypes.Some(1),
		false,
	)
	details, err := apptypes.EventDetailsOf(event, domtypes.Some(audit))
	if err != nil {
		t.Fatalf("EventDetailsOf: %v", err)
	}
	events := &topDataEventStub{showDetails: details}
	loader := newTopDataLoader(nil, events, nil)

	got, err := loader.loadDetail(context.Background(), topDetailRequest{
		target: topDetailTarget{kind: topDetailEvent, title: "EVENT evt-detail", eventID: event.EventID()},
	})
	if err != nil {
		t.Fatalf("loadDetail(event) error = %v", err)
	}
	joined := strings.Join(got.lines, "\n")
	for _, expect := range []string{"EVENT_ID: evt-detail", "MESSAGE: full event body", "COMMAND: go test ./...", "EXIT_CODE: 1", "stdout payload"} {
		if !strings.Contains(joined, expect) {
			t.Fatalf("event detail missing %q:\n%s", expect, joined)
		}
	}
	if events.showCalls != 1 || events.showEventID != "evt-detail" {
		t.Fatalf("Show calls/id = %d/%q, want 1/evt-detail", events.showCalls, events.showEventID)
	}
}

func TestTopDataLoader_LoadDetail_MemoryUsesShowAndFormatsRefs(t *testing.T) {
	t.Parallel()

	summary := memorySummaryFixture(t, "mem-detail", domtypes.MemoryStatusCandidate, "full memory fact")
	evidence, err := domtypes.EvidenceRefFrom(domtypes.EvidenceRefKindEvent, "evt-detail")
	if err != nil {
		t.Fatalf("EvidenceRefFrom: %v", err)
	}
	artifact, err := domtypes.ArtifactRefFrom(domtypes.ArtifactRefKindFile, "docs/plan.md")
	if err != nil {
		t.Fatalf("ArtifactRefFrom: %v", err)
	}
	details := apptypes.MemoryDetailsOf(summary, []domtypes.EvidenceRef{evidence}, []domtypes.ArtifactRef{artifact})
	memory := &topDataMemoryStub{showDetails: details}
	loader := newTopDataLoader(nil, nil, memory)

	got, err := loader.loadDetail(context.Background(), topDetailRequest{
		target: topDetailTarget{kind: topDetailMemory, title: "MEMORY mem-detail", memoryID: summary.MemoryID()},
	})
	if err != nil {
		t.Fatalf("loadDetail(memory) error = %v", err)
	}
	joined := strings.Join(got.lines, "\n")
	for _, expect := range []string{"MEMORY_ID: mem-detail", "FACT: full memory fact", "EVIDENCE_REFS:", "event:evt-detail", "ARTIFACT_REFS:", "file:docs/plan.md"} {
		if !strings.Contains(joined, expect) {
			t.Fatalf("memory detail missing %q:\n%s", expect, joined)
		}
	}
	if memory.showCalls != 1 || memory.showMemoryID != "mem-detail" {
		t.Fatalf("Show calls/id = %d/%q, want 1/mem-detail", memory.showCalls, memory.showMemoryID)
	}
}

func TestTopDataLoader_LoadSnapshot_AggregatesEveryPane(t *testing.T) {
	t.Parallel()

	root := sessionSummaryFixture("root", "", fixedStartedAt, "active", domtypes.EventKindTranscript, "active root")
	failure := mustEvent(t, "evt-fail", domtypes.EventKindCommandExecuted, "go test ./... [exit=1]")
	cmd := mustEvent(t, "evt-cmd", domtypes.EventKindCommandExecuted, "ls -la")
	workspace, err := domtypes.WorkspaceFrom("duck8823/traceary")
	if err != nil {
		t.Fatalf("WorkspaceFrom: %v", err)
	}
	candidate, err := apptypes.MemorySummaryOf(
		domtypes.MemoryID("mem-1"),
		domtypes.MemoryTypePreference,
		domtypes.WorkspaceScopeOf(workspace),
		"snapshot fixture",
		domtypes.MemoryStatusCandidate,
		domtypes.ConfidenceMedium,
		domtypes.MemorySourceManual,
		domtypes.None[domtypes.MemoryID](),
		domtypes.None[time.Time](),
		fixedStartedAt,
		domtypes.None[time.Time](),
		fixedStartedAt,
		fixedStartedAt,
	)
	if err != nil {
		t.Fatalf("MemorySummaryOf: %v", err)
	}
	staleSummary := memorySummaryFixture(t, "mem-stale", domtypes.MemoryStatusSuperseded, "stale snapshot fixture")
	staleRow, err := apptypes.StaleMemoryRowOf(staleSummary, apptypes.StaleMemoryReasonSuperseded)
	if err != nil {
		t.Fatalf("StaleMemoryRowOf: %v", err)
	}
	staleResult, err := apptypes.StaleMemoryListResultOf(3, []apptypes.StaleMemoryRow{staleRow})
	if err != nil {
		t.Fatalf("StaleMemoryListResultOf: %v", err)
	}

	session := &topDataSessionStub{
		listResult: []apptypes.SessionSummary{root},
		lineageByID: map[domtypes.SessionID][]apptypes.SessionSummary{
			domtypes.SessionID("root"): {root},
		},
	}
	memory := &topDataMemoryStub{listResult: []apptypes.MemorySummary{candidate}, staleResult: staleResult}
	// snapshotEventStub differentiates the two EventUsecase.List calls
	// loadSnapshot makes (failures vs recent commands) by inspecting
	// the FailuresOnly / Kind criteria, so each pane sees a distinct
	// fixture.
	event := &snapshotEventStub{failures: []*model.Event{failure}, commands: []*model.Event{cmd}}
	loader := newTopDataLoader(session, event, memory)

	snap, err := loader.loadSnapshot(context.Background(), topDataCriteria{
		SessionLimit:       10,
		FailureLimit:       3,
		RecentCommandLimit: 3,
		CandidateLimit:     2,
		StaleMemoryLimit:   2,
	})
	if err != nil {
		t.Fatalf("loadSnapshot() error = %v", err)
	}
	if len(snap.Sessions) != 1 || snap.Sessions[0].summary.SessionID().String() != "root" {
		t.Fatalf("snap.Sessions = %#v, want one root", snap.Sessions)
	}
	if len(snap.Failures) != 1 || snap.Failures[0].EventID().String() != "evt-fail" {
		t.Fatalf("snap.Failures = %#v, want one evt-fail", snap.Failures)
	}
	if len(snap.RecentCommands) != 1 || snap.RecentCommands[0].EventID().String() != "evt-cmd" {
		t.Fatalf("snap.RecentCommands = %#v, want one evt-cmd", snap.RecentCommands)
	}
	if len(snap.Candidates) != 1 || snap.Candidates[0].MemoryID().String() != "mem-1" {
		t.Fatalf("snap.Candidates = %#v, want one mem-1", snap.Candidates)
	}
	if snap.StaleMemories.Count() != 3 {
		t.Fatalf("snap.StaleMemories.Count = %d, want 3", snap.StaleMemories.Count())
	}
	if items := snap.StaleMemories.Items(); len(items) != 1 || items[0].Summary().MemoryID().String() != "mem-stale" {
		t.Fatalf("snap.StaleMemories.Items = %#v, want one mem-stale", items)
	}
}

func TestTopDataLoader_LoadSnapshot_ComputesReliabilityMetrics(t *testing.T) {
	t.Parallel()

	now := fixedStartedAt.Add(48 * time.Hour)
	fresh := sessionSummaryFixture("fresh", "", now.Add(-time.Hour), "active", domtypes.EventKindTranscript, "fresh")
	stale := sessionSummaryFixture("stale", "", now.Add(-30*time.Hour), "stale", domtypes.EventKindTranscript, "stale")
	session := &topDataSessionStub{
		listResult: []apptypes.SessionSummary{fresh, stale},
		lineageByID: map[domtypes.SessionID][]apptypes.SessionSummary{
			domtypes.SessionID("fresh"): {fresh},
			domtypes.SessionID("stale"): {stale},
		},
	}

	hugeFailure := mustEvent(t, "evt-huge-fail", domtypes.EventKindCommandExecuted, strings.Repeat("f", apptypes.DefaultTopSnapshotBodyLimit+1))
	hugeCommand := mustEvent(t, "evt-huge-cmd", domtypes.EventKindCommandExecuted, strings.Repeat("c", apptypes.DefaultTopSnapshotBodyLimit+1))
	event := &snapshotEventStub{failures: []*model.Event{hugeFailure}, commands: []*model.Event{hugeCommand}}

	accepted := memorySummaryWithUpdatedAt(t, "mem-accepted", domtypes.MemoryStatusAccepted, now.Add(-2*time.Hour))
	oldCandidate := memorySummaryWithUpdatedAt(t, "mem-old-candidate", domtypes.MemoryStatusCandidate, now.Add(-25*time.Hour))
	newCandidate := memorySummaryWithUpdatedAt(t, "mem-new-candidate", domtypes.MemoryStatusCandidate, now.Add(-3*time.Hour))
	memory := &topDataMemoryStub{
		listFunc: func(criteria apptypes.MemoryListCriteria) ([]apptypes.MemorySummary, error) {
			statuses := criteria.Statuses()
			if len(statuses) == 1 && statuses[0] == domtypes.MemoryStatusCandidate {
				return []apptypes.MemorySummary{oldCandidate}, nil
			}
			return []apptypes.MemorySummary{accepted, oldCandidate, newCandidate}, nil
		},
	}
	loader := newTopDataLoader(session, event, memory)

	snap, err := loader.loadSnapshot(context.Background(), topDataCriteria{
		SessionLimit:       10,
		FailureLimit:       1,
		RecentCommandLimit: 1,
		CandidateLimit:     1,
		StaleMemoryLimit:   1,
		StaleAfter:         24 * time.Hour,
		Now:                now,
	})
	if err != nil {
		t.Fatalf("loadSnapshot() error = %v", err)
	}

	if got := snap.Reliability.StaleActiveSessionCount; got != 1 {
		t.Fatalf("StaleActiveSessionCount = %d, want 1", got)
	}
	if len(snap.Sessions) != 1 || snap.Sessions[0].summary.SessionID() != "fresh" {
		t.Fatalf("Sessions = %#v, want stale session filtered while still counted", snap.Sessions)
	}
	if got, want := snap.Reliability.AcceptedMemoryCount, 1; got != want {
		t.Fatalf("AcceptedMemoryCount = %d, want %d", got, want)
	}
	if got, want := snap.Reliability.CandidateMemoryCount, 2; got != want {
		t.Fatalf("CandidateMemoryCount = %d, want %d", got, want)
	}
	if got, want := snap.Reliability.CandidateAge.Count, 2; got != want {
		t.Fatalf("CandidateAge.Count = %d, want %d", got, want)
	}
	if !snap.Reliability.CandidateAge.Oldest.Equal(oldCandidate.UpdatedAt()) {
		t.Fatalf("CandidateAge.Oldest = %s, want %s", snap.Reliability.CandidateAge.Oldest, oldCandidate.UpdatedAt())
	}
	if got, want := snap.Reliability.CandidateAge.OldestAge, 25*time.Hour; got != want {
		t.Fatalf("CandidateAge.OldestAge = %s, want %s", got, want)
	}
	if got, want := snap.Reliability.CandidateAge.AverageAge, 14*time.Hour; got != want {
		t.Fatalf("CandidateAge.AverageAge = %s, want %s", got, want)
	}
	if got, want := snap.Reliability.LargePayloads.Count, 2; got != want {
		t.Fatalf("LargePayloads.Count = %d, want %d", got, want)
	}
	if got, want := snap.Reliability.LargePayloads.RecentCommandCount, 1; got != want {
		t.Fatalf("LargePayloads.RecentCommandCount = %d, want %d", got, want)
	}
	if got, want := snap.Reliability.LargePayloads.RecentFailureCount, 1; got != want {
		t.Fatalf("LargePayloads.RecentFailureCount = %d, want %d", got, want)
	}
	if got, want := len(memory.listCriteriaCalls), 2; got != want {
		t.Fatalf("memory.List calls = %d, want %d (candidate pane + reliability scan)", got, want)
	}
}

func TestTopDataLoader_LoadSnapshot_SkipsMemoryReliabilityWhenRequested(t *testing.T) {
	t.Parallel()

	hugeFailure := mustEvent(t, "evt-skip-huge-fail", domtypes.EventKindCommandExecuted, strings.Repeat("f", apptypes.DefaultTopSnapshotBodyLimit+1))
	command := mustEvent(t, "evt-skip-cmd", domtypes.EventKindCommandExecuted, "go test ./...")
	event := &snapshotEventStub{failures: []*model.Event{hugeFailure}, commands: []*model.Event{command}}
	memory := &topDataMemoryStub{listErr: context.Canceled, staleErr: context.Canceled}
	loader := newTopDataLoader(nil, event, memory)

	snap, err := loader.loadSnapshot(context.Background(), topDataCriteria{
		FailureLimit:          1,
		RecentCommandLimit:    1,
		CandidateLimit:        0,
		StaleMemoryLimit:      0,
		SkipMemoryReliability: true,
	})
	if err != nil {
		t.Fatalf("loadSnapshot() error = %v", err)
	}
	if got := memory.listCalls; got != 0 {
		t.Fatalf("memory.List calls = %d, want 0 when memory reliability is skipped", got)
	}
	if got := memory.staleMemoryCalls; got != 0 {
		t.Fatalf("memory.ListStale calls = %d, want 0 when stale memory pane is disabled", got)
	}
	if got, want := snap.Reliability.LargePayloads.Count, 1; got != want {
		t.Fatalf("LargePayloads.Count = %d, want %d", got, want)
	}
	if got := snap.Reliability.CandidateMemoryCount; got != 0 {
		t.Fatalf("CandidateMemoryCount = %d, want 0 when memory reliability is skipped", got)
	}
}

func TestWriteTopSnapshotJSON_EmitsReliabilityShape(t *testing.T) {
	t.Parallel()

	oldest := fixedStartedAt
	newest := fixedStartedAt.Add(2 * time.Hour)
	var buf bytes.Buffer
	if err := writeTopSnapshotJSON(&buf, topDataSnapshot{
		Reliability: topReliabilityMetrics{
			StaleActiveSessionCount: 2,
			AcceptedMemoryCount:     3,
			CandidateMemoryCount:    1,
			MemoryScanLimit:         topReliabilityMemoryScanLimit,
			CandidateAge: topCandidateAgeMetrics{
				Count:      1,
				Oldest:     oldest,
				Newest:     newest,
				OldestAge:  4 * time.Hour,
				AverageAge: 4 * time.Hour,
			},
			LargePayloads: topLargePayloadMetrics{
				Count:              2,
				RecentCommandCount: 1,
				RecentFailureCount: 1,
				SampledEventCount:  3,
				BodyLimitRunes:     apptypes.DefaultTopSnapshotBodyLimit,
			},
		},
	}); err != nil {
		t.Fatalf("writeTopSnapshotJSON() error = %v", err)
	}

	var payload struct {
		Reliability struct {
			StaleActiveSessionCount int `json:"stale_active_session_count"`
			Memory                  struct {
				AcceptedCount  int      `json:"accepted_count"`
				CandidateCount int      `json:"candidate_count"`
				AcceptedRatio  *float64 `json:"accepted_ratio"`
			} `json:"memory"`
			CandidateAge struct {
				Count             int    `json:"count"`
				OldestUpdatedAt   string `json:"oldest_updated_at"`
				NewestUpdatedAt   string `json:"newest_updated_at"`
				OldestAgeSeconds  int64  `json:"oldest_age_seconds"`
				AverageAgeSeconds int64  `json:"average_age_seconds"`
			} `json:"candidate_age"`
			LargePayloads struct {
				Count              int `json:"count"`
				RecentCommandCount int `json:"recent_command_count"`
				RecentFailureCount int `json:"recent_failure_count"`
				SampledEventCount  int `json:"sampled_event_count"`
				BodyLimitRunes     int `json:"body_limit_runes"`
			} `json:"large_payloads"`
		} `json:"reliability"`
	}
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal(top snapshot) error = %v\n%s", err, buf.String())
	}
	if got, want := payload.Reliability.StaleActiveSessionCount, 2; got != want {
		t.Fatalf("stale_active_session_count = %d, want %d", got, want)
	}
	if payload.Reliability.Memory.AcceptedRatio == nil || *payload.Reliability.Memory.AcceptedRatio != 0.75 {
		t.Fatalf("accepted_ratio = %v, want 0.75", payload.Reliability.Memory.AcceptedRatio)
	}
	if got, want := payload.Reliability.CandidateAge.OldestAgeSeconds, int64((4 * time.Hour).Seconds()); got != want {
		t.Fatalf("oldest_age_seconds = %d, want %d", got, want)
	}
	if got, want := payload.Reliability.LargePayloads.SampledEventCount, 3; got != want {
		t.Fatalf("sampled_event_count = %d, want %d", got, want)
	}
}

// snapshotEventStub returns separate fixtures for the failures call and
// the recent-commands call so loadSnapshot's two EventUsecase.List
// invocations can be distinguished by the FailuresOnly / Kind criteria.
type snapshotEventStub struct {
	usecase.EventUsecase

	events   []*model.Event
	failures []*model.Event
	commands []*model.Event
}

func (s *snapshotEventStub) List(_ context.Context, criteria apptypes.EventListCriteria) ([]*model.Event, error) {
	if criteria.FailuresOnly() {
		return s.failures, nil
	}
	if criteria.Kind() == domtypes.EventKindCommandExecuted {
		return s.commands, nil
	}
	events := make([]*model.Event, 0, len(s.events))
	for _, event := range s.events {
		if event == nil || criteria.From().IsZero() || !event.CreatedAt().Before(criteria.From()) {
			events = append(events, event)
		}
	}
	if criteria.Limit() > 0 && len(events) > criteria.Limit() {
		return events[:criteria.Limit()], nil
	}
	return events, nil
}

func TestTopDataLoader_LoadSnapshot_NoUsecasesReturnsEmpty(t *testing.T) {
	t.Parallel()

	loader := newTopDataLoader(nil, nil, nil)
	snap, err := loader.loadSnapshot(context.Background(), topDataCriteria{
		SessionLimit:       10,
		FailureLimit:       10,
		RecentCommandLimit: 10,
		CandidateLimit:     10,
		StaleMemoryLimit:   10,
	})
	if err != nil {
		t.Fatalf("loadSnapshot() error = %v", err)
	}
	if snap.Sessions != nil || snap.Failures != nil || snap.RecentCommands != nil || snap.Candidates != nil || snap.StaleMemories.Count() != 0 || len(snap.StaleMemories.Items()) != 0 {
		t.Fatalf("loadSnapshot() = %#v, want zero-value snapshot when no usecases are wired", snap)
	}
}

func memorySummaryFixture(t *testing.T, id string, status domtypes.MemoryStatus, fact string) apptypes.MemorySummary {
	t.Helper()
	return memorySummaryWithFactAndUpdatedAt(t, id, status, fact, fixedStartedAt)
}

func memorySummaryWithUpdatedAt(t *testing.T, id string, status domtypes.MemoryStatus, updatedAt time.Time) apptypes.MemorySummary {
	t.Helper()
	return memorySummaryWithFactAndUpdatedAt(t, id, status, "reliability metric fixture "+id, updatedAt)
}

func memorySummaryWithFactAndUpdatedAt(t *testing.T, id string, status domtypes.MemoryStatus, fact string, updatedAt time.Time) apptypes.MemorySummary {
	t.Helper()
	workspace, err := domtypes.WorkspaceFrom("duck8823/traceary")
	if err != nil {
		t.Fatalf("WorkspaceFrom: %v", err)
	}
	summary, err := apptypes.MemorySummaryOf(
		domtypes.MemoryID(id),
		domtypes.MemoryTypePreference,
		domtypes.WorkspaceScopeOf(workspace),
		fact,
		status,
		domtypes.ConfidenceMedium,
		domtypes.MemorySourceManual,
		domtypes.None[domtypes.MemoryID](),
		domtypes.None[time.Time](),
		fixedStartedAt,
		domtypes.None[time.Time](),
		fixedStartedAt,
		updatedAt,
	)
	if err != nil {
		t.Fatalf("MemorySummaryOf: %v", err)
	}
	return summary
}

func mustEvent(t *testing.T, id string, kind domtypes.EventKind, body string) *model.Event {
	t.Helper()
	return model.EventOf(
		domtypes.EventID(id),
		kind,
		domtypes.Client("claude"),
		domtypes.Agent("claude"),
		domtypes.SessionID("session-1"),
		domtypes.Workspace("duck8823/traceary"),
		body,
		fixedStartedAt,
	)
}
