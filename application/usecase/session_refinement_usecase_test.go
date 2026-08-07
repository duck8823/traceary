package usecase_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

type fixedRefineClock struct{ at time.Time }

func (c fixedRefineClock) Now() time.Time { return c.at }

type sessionRefinementRepositoryStub struct {
	bySession map[types.SessionID]*model.SessionRefinement
	saved     []*model.SessionRefinement
	// saveResults forces successive SaveIfAdvances outcomes after the
	// generation CAS check. When empty, a matching expectedGeneration succeeds
	// and stores the refinement. A scripted true still fails if the caller
	// submitted a stale expectedGeneration (mirrors the real WHERE clause).
	saveResults []bool
	// raceWinnersOnLose is consumed one entry per forced scripted loss so the
	// next re-read observes concurrent progress (generation and coverage
	// advance). Real CAS losses always leave a different persisted row.
	raceWinnersOnLose []*model.SessionRefinement
	raceWinnerIdx     int
	saveCalls         int
	// expectedGens records every expectedGeneration the use case submitted.
	expectedGens []int
}

func (s *sessionRefinementRepositoryStub) FindBySessionID(
	_ context.Context,
	sessionID types.SessionID,
) (types.Optional[*model.SessionRefinement], error) {
	if row, ok := s.bySession[sessionID]; ok {
		return types.Some(row), nil
	}
	return types.None[*model.SessionRefinement](), nil
}

func (s *sessionRefinementRepositoryStub) SaveIfAdvances(
	_ context.Context,
	refinement *model.SessionRefinement,
	expectedGeneration int,
) (bool, error) {
	s.saveCalls++
	s.expectedGens = append(s.expectedGens, expectedGeneration)

	// Mirror WHERE session_refinements.generation = ?: 0 means "no row yet".
	storedGen := 0
	if row, ok := s.bySession[refinement.SessionID()]; ok {
		storedGen = row.Generation()
	}
	if expectedGeneration != storedGen {
		return false, nil
	}

	if len(s.saveResults) > 0 {
		idx := s.saveCalls - 1
		if idx >= len(s.saveResults) {
			idx = len(s.saveResults) - 1
		}
		if !s.saveResults[idx] {
			// Concurrent writer landed: install the next race winner so the
			// following re-read cannot keep seeing the pre-CAS snapshot.
			s.installNextRaceWinner()
			return false, nil
		}
	}
	s.saved = append(s.saved, refinement)
	if s.bySession == nil {
		s.bySession = map[types.SessionID]*model.SessionRefinement{}
	}
	s.bySession[refinement.SessionID()] = refinement
	return true, nil
}

func (s *sessionRefinementRepositoryStub) installNextRaceWinner() {
	if s.raceWinnerIdx >= len(s.raceWinnersOnLose) {
		return
	}
	winner := s.raceWinnersOnLose[s.raceWinnerIdx]
	s.raceWinnerIdx++
	if winner == nil {
		return
	}
	if s.bySession == nil {
		s.bySession = map[types.SessionID]*model.SessionRefinement{}
	}
	s.bySession[winner.SessionID()] = winner
}

type sessionEventOrderRepositoryStub struct {
	earliest      map[types.SessionID]types.EventID
	eventSessions map[types.EventID]types.SessionID
	// after[left][right] = left is strictly after right
	after map[types.EventID]map[types.EventID]bool
}

func (s *sessionEventOrderRepositoryStub) EarliestEventID(
	_ context.Context,
	sessionID types.SessionID,
) (types.Optional[types.EventID], error) {
	if id, ok := s.earliest[sessionID]; ok {
		return types.Some(id), nil
	}
	return types.None[types.EventID](), nil
}

func (s *sessionEventOrderRepositoryStub) FindEventSessionID(
	_ context.Context,
	eventID types.EventID,
) (types.Optional[types.SessionID], error) {
	if sid, ok := s.eventSessions[eventID]; ok {
		return types.Some(sid), nil
	}
	return types.None[types.SessionID](), nil
}

func (s *sessionEventOrderRepositoryStub) EventIsStrictlyAfter(
	_ context.Context,
	left, right types.EventID,
) (bool, error) {
	if s.after == nil {
		return false, nil
	}
	if byRight, ok := s.after[left]; ok {
		return byRight[right], nil
	}
	return false, nil
}

func mustRefinement(
	t *testing.T,
	sessionID types.SessionID,
	generation int,
	from, to types.EventID,
	summary string,
) *model.SessionRefinement {
	t.Helper()
	row, err := model.NewSessionRefinement(
		sessionID,
		generation,
		from,
		to,
		summary,
		"",
		"agent",
		time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		false,
	)
	if err != nil {
		t.Fatalf("NewSessionRefinement() error = %v", err)
	}
	return row
}

func TestSessionRefinementUsecase_Refine(t *testing.T) {
	t.Parallel()

	sessionID := types.SessionID("sess-a")
	otherSession := types.SessionID("sess-b")
	evt1 := types.EventID("evt-1")
	evt2 := types.EventID("evt-2")
	evt3 := types.EventID("evt-3")
	foreignEvt := types.EventID("evt-foreign")
	now := time.Date(2026, 8, 8, 15, 0, 0, 0, time.UTC)
	session := model.NewSession(sessionID, now, "cli", "codex", "ws")

	tests := []struct {
		name        string
		session     *model.Session
		repo        *sessionRefinementRepositoryStub
		eventOrder  *sessionEventOrderRepositoryStub
		input       usecase.SessionRefineInput
		wantOutcome model.SessionRefineOutcome
		wantGen     int
		wantFrom    types.EventID
		wantTo      types.EventID
		wantSummary string
		wantSaves   int
		wantErr     error
	}{
		{
			name:    "first refine inserts generation 1 with earliest covers_from",
			session: session,
			repo:    &sessionRefinementRepositoryStub{},
			eventOrder: &sessionEventOrderRepositoryStub{
				earliest:      map[types.SessionID]types.EventID{sessionID: evt1},
				eventSessions: map[types.EventID]types.SessionID{evt2: sessionID},
			},
			input: usecase.SessionRefineInput{
				SessionID: sessionID, Summary: "first summary", ProducedBy: "agent", CoversTo: evt2,
			},
			wantOutcome: model.SessionRefineOutcomeCreated,
			wantGen:     1,
			wantFrom:    evt1,
			wantTo:      evt2,
			wantSummary: "first summary",
			wantSaves:   1,
		},
		{
			name:    "later covers_to supersedes and bumps generation while keeping covers_from",
			session: session,
			repo: &sessionRefinementRepositoryStub{
				bySession: map[types.SessionID]*model.SessionRefinement{
					sessionID: mustRefinement(t, sessionID, 1, evt1, evt2, "first summary"),
				},
			},
			eventOrder: &sessionEventOrderRepositoryStub{
				eventSessions: map[types.EventID]types.SessionID{evt3: sessionID},
				after: map[types.EventID]map[types.EventID]bool{
					evt3: {evt2: true},
				},
			},
			input: usecase.SessionRefineInput{
				SessionID: sessionID, Summary: "merged summary", ProducedBy: "agent", CoversTo: evt3,
			},
			wantOutcome: model.SessionRefineOutcomeSuperseded,
			wantGen:     2,
			wantFrom:    evt1,
			wantTo:      evt3,
			wantSummary: "merged summary",
			wantSaves:   1,
		},
		{
			name:    "same covers_to is a no-op",
			session: session,
			repo: &sessionRefinementRepositoryStub{
				bySession: map[types.SessionID]*model.SessionRefinement{
					sessionID: mustRefinement(t, sessionID, 2, evt1, evt3, "merged summary"),
				},
			},
			eventOrder: &sessionEventOrderRepositoryStub{
				eventSessions: map[types.EventID]types.SessionID{evt3: sessionID},
				after: map[types.EventID]map[types.EventID]bool{
					evt3: {evt3: false},
				},
			},
			input: usecase.SessionRefineInput{
				SessionID: sessionID, Summary: "should not write", ProducedBy: "agent", CoversTo: evt3,
			},
			wantOutcome: model.SessionRefineOutcomeUnchanged,
			wantGen:     2,
			wantFrom:    evt1,
			wantTo:      evt3,
			wantSummary: "merged summary",
			wantSaves:   0,
		},
		{
			name:    "older covers_to is a no-op and does not downgrade",
			session: session,
			repo: &sessionRefinementRepositoryStub{
				bySession: map[types.SessionID]*model.SessionRefinement{
					sessionID: mustRefinement(t, sessionID, 2, evt1, evt3, "merged summary"),
				},
			},
			eventOrder: &sessionEventOrderRepositoryStub{
				eventSessions: map[types.EventID]types.SessionID{evt2: sessionID},
				after: map[types.EventID]map[types.EventID]bool{
					evt2: {evt3: false},
				},
			},
			input: usecase.SessionRefineInput{
				SessionID: sessionID, Summary: "downgrade attempt", ProducedBy: "agent", CoversTo: evt2,
			},
			wantOutcome: model.SessionRefineOutcomeUnchanged,
			wantGen:     2,
			wantFrom:    evt1,
			wantTo:      evt3,
			wantSummary: "merged summary",
			wantSaves:   0,
		},
		{
			name:    "unknown session id is an error",
			session: nil,
			repo:    &sessionRefinementRepositoryStub{},
			eventOrder: &sessionEventOrderRepositoryStub{
				eventSessions: map[types.EventID]types.SessionID{evt1: sessionID},
			},
			input: usecase.SessionRefineInput{
				SessionID: sessionID, Summary: "x", ProducedBy: "agent", CoversTo: evt1,
			},
			wantErr: model.ErrInvalidSessionState,
		},
		{
			name:    "event belonging to a different session is an error",
			session: session,
			repo:    &sessionRefinementRepositoryStub{},
			eventOrder: &sessionEventOrderRepositoryStub{
				eventSessions: map[types.EventID]types.SessionID{foreignEvt: otherSession},
			},
			input: usecase.SessionRefineInput{
				SessionID: sessionID, Summary: "x", ProducedBy: "agent", CoversTo: foreignEvt,
			},
			wantErr: model.ErrInvalidSessionRefinement,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sessionStub := &sessionRepositoryStub{}
			if tt.session != nil {
				sessionStub.session = tt.session
			}
			sut := usecase.NewSessionRefinementUsecase(sessionStub, tt.repo, tt.eventOrder, fixedRefineClock{at: now})
			got, err := sut.Refine(context.Background(), tt.input)
			if tt.wantErr != nil {
				if err == nil || !errors.Is(err, tt.wantErr) {
					t.Fatalf("Refine() error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Refine() error = %v", err)
			}
			if got.Outcome() != tt.wantOutcome {
				t.Fatalf("Outcome() = %q, want %q", got.Outcome(), tt.wantOutcome)
			}
			row := got.Refinement()
			if diff := cmp.Diff(tt.wantGen, row.Generation()); diff != "" {
				t.Fatalf("Generation mismatch (-want +got):\n%s", diff)
			}
			if row.CoversFromEventID() != tt.wantFrom || row.CoversToEventID() != tt.wantTo {
				t.Fatalf("coverage = %s..%s, want %s..%s", row.CoversFromEventID(), row.CoversToEventID(), tt.wantFrom, tt.wantTo)
			}
			if row.Summary() != tt.wantSummary {
				t.Fatalf("Summary() = %q, want %q", row.Summary(), tt.wantSummary)
			}
			if len(tt.repo.saved) != tt.wantSaves {
				t.Fatalf("saves = %d, want %d", len(tt.repo.saved), tt.wantSaves)
			}
		})
	}
}

func TestSessionRefinementUsecase_Refine_RetriesWhenSaveIfAdvancesLosesRace(t *testing.T) {
	t.Parallel()

	sessionID := types.SessionID("sess-race")
	evt1 := types.EventID("evt-1")
	evt2 := types.EventID("evt-2")
	evt3 := types.EventID("evt-3")
	evt4 := types.EventID("evt-4")
	now := time.Date(2026, 8, 8, 15, 0, 0, 0, time.UTC)
	session := model.NewSession(sessionID, now, "cli", "codex", "ws")
	// Event order: evt1 < evt2 < evt3 < evt4 (strictly after is transitive for tests).
	eventOrder := &sessionEventOrderRepositoryStub{
		eventSessions: map[types.EventID]types.SessionID{
			evt3: sessionID,
			evt4: sessionID,
		},
		after: map[types.EventID]map[types.EventID]bool{
			evt3: {evt2: true},
			evt4: {evt2: true, evt3: true},
		},
	}

	t.Run("winner advanced but not past caller: retry supersedes with recomputed generation", func(t *testing.T) {
		t.Parallel()

		// Original read: gen 1 covering ..evt2.
		// Race winner: gen 2 covering ..evt3 (past original, still behind caller's evt4).
		// Retry must supersede from the winner: gen 3, covers_from=evt1, covers_to=evt4.
		// expectedGeneration sequence must be [1, 2] — stale 1 on the retry would
		// be rejected by the CAS check and is the bug this assertion guards.
		winner := mustRefinement(t, sessionID, 2, evt1, evt3, "winner summary")
		repo := &sessionRefinementRepositoryStub{
			bySession: map[types.SessionID]*model.SessionRefinement{
				sessionID: mustRefinement(t, sessionID, 1, evt1, evt2, "first summary"),
			},
			saveResults:       []bool{false, true},
			raceWinnersOnLose: []*model.SessionRefinement{winner},
		}
		wantRow, err := model.NewSessionRefinement(
			sessionID, 3, evt1, evt4, "after race", "", "agent", now, false,
		)
		if err != nil {
			t.Fatalf("NewSessionRefinement() error = %v", err)
		}
		want, err := model.SessionRefineResultOf(model.SessionRefineOutcomeSuperseded, wantRow)
		if err != nil {
			t.Fatalf("SessionRefineResultOf() error = %v", err)
		}

		sut := usecase.NewSessionRefinementUsecase(
			&sessionRepositoryStub{session: session},
			repo,
			eventOrder,
			fixedRefineClock{at: now},
		)
		got, err := sut.Refine(context.Background(), usecase.SessionRefineInput{
			SessionID: sessionID, Summary: "after race", ProducedBy: "agent", CoversTo: evt4,
		})
		if err != nil {
			t.Fatalf("Refine() error = %v", err)
		}
		if diff := cmp.Diff(want, got, cmp.AllowUnexported(model.SessionRefineResult{}, model.SessionRefinement{})); diff != "" {
			t.Fatalf("SessionRefineResult mismatch (-want +got):\n%s", diff)
		}
		if repo.saveCalls != 2 {
			t.Fatalf("SaveIfAdvances calls = %d, want 2", repo.saveCalls)
		}
		if len(repo.saved) != 1 {
			t.Fatalf("successful saves = %d, want 1", len(repo.saved))
		}
		if diff := cmp.Diff([]int{1, 2}, repo.expectedGens); diff != "" {
			t.Fatalf("expectedGeneration sequence mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("winner advanced past caller: retry returns unchanged without write", func(t *testing.T) {
		t.Parallel()

		// Original read: gen 1 covering ..evt2.
		// Race winner: gen 2 covering ..evt4 (past caller's evt3).
		// Retry must return unchanged and not attempt another write.
		winner := mustRefinement(t, sessionID, 2, evt1, evt4, "winner past caller")
		repo := &sessionRefinementRepositoryStub{
			bySession: map[types.SessionID]*model.SessionRefinement{
				sessionID: mustRefinement(t, sessionID, 1, evt1, evt2, "first summary"),
			},
			saveResults:       []bool{false},
			raceWinnersOnLose: []*model.SessionRefinement{winner},
		}
		want, err := model.SessionRefineResultOf(model.SessionRefineOutcomeUnchanged, winner)
		if err != nil {
			t.Fatalf("SessionRefineResultOf() error = %v", err)
		}

		sut := usecase.NewSessionRefinementUsecase(
			&sessionRepositoryStub{session: session},
			repo,
			eventOrder,
			fixedRefineClock{at: now},
		)
		got, err := sut.Refine(context.Background(), usecase.SessionRefineInput{
			SessionID: sessionID, Summary: "stale attempt", ProducedBy: "agent", CoversTo: evt3,
		})
		if err != nil {
			t.Fatalf("Refine() error = %v", err)
		}
		if diff := cmp.Diff(want, got, cmp.AllowUnexported(model.SessionRefineResult{}, model.SessionRefinement{})); diff != "" {
			t.Fatalf("SessionRefineResult mismatch (-want +got):\n%s", diff)
		}
		if repo.saveCalls != 1 {
			t.Fatalf("SaveIfAdvances calls = %d, want 1 (second decision needs no write)", repo.saveCalls)
		}
		if len(repo.saved) != 0 {
			t.Fatalf("successful saves = %d, want 0", len(repo.saved))
		}
		if diff := cmp.Diff([]int{1}, repo.expectedGens); diff != "" {
			t.Fatalf("expectedGeneration sequence mismatch (-want +got):\n%s", diff)
		}
	})
}

func TestSessionRefinementUsecase_Refine_ExhaustsWhenSaveIfAdvancesAlwaysLoses(t *testing.T) {
	t.Parallel()

	sessionID := types.SessionID("sess-exhaust")
	evt1 := types.EventID("evt-1")
	evt2 := types.EventID("evt-2")
	evt3 := types.EventID("evt-3")
	evt4 := types.EventID("evt-4")
	evt5 := types.EventID("evt-5")
	evt6 := types.EventID("evt-6")
	now := time.Date(2026, 8, 8, 15, 0, 0, 0, time.UTC)
	session := model.NewSession(sessionID, now, "cli", "codex", "ws")

	// Three consecutive CAS losses must each install a different concurrent
	// winner that advances generation and covers_to while remaining strictly
	// behind the caller's evt6 — so the caller still has a legitimate advance
	// to attempt and still exhausts the bounded retry loop. An unchanged row
	// across three losses is not a state the real store can be in.
	// Event order: evt1 < evt2 < evt3 < evt4 < evt5 < evt6.
	repo := &sessionRefinementRepositoryStub{
		bySession: map[types.SessionID]*model.SessionRefinement{
			sessionID: mustRefinement(t, sessionID, 1, evt1, evt2, "first summary"),
		},
		saveResults: []bool{false},
		raceWinnersOnLose: []*model.SessionRefinement{
			mustRefinement(t, sessionID, 2, evt1, evt3, "winner gen2"),
			mustRefinement(t, sessionID, 3, evt1, evt4, "winner gen3"),
			mustRefinement(t, sessionID, 4, evt1, evt5, "winner gen4"),
		},
	}
	eventOrder := &sessionEventOrderRepositoryStub{
		eventSessions: map[types.EventID]types.SessionID{evt6: sessionID},
		after: map[types.EventID]map[types.EventID]bool{
			evt6: {evt2: true, evt3: true, evt4: true, evt5: true},
		},
	}

	sut := usecase.NewSessionRefinementUsecase(
		&sessionRepositoryStub{session: session},
		repo,
		eventOrder,
		fixedRefineClock{at: now},
	)
	got, err := sut.Refine(context.Background(), usecase.SessionRefineInput{
		SessionID: sessionID, Summary: "never lands", ProducedBy: "agent", CoversTo: evt6,
	})
	if err == nil {
		t.Fatalf("Refine() error = nil, want exhaustion error; result = %+v", got)
	}
	if !strings.Contains(err.Error(), "after 3 concurrent attempts") {
		t.Fatalf("Refine() error = %v, want bounded-attempt exhaustion message", err)
	}
	if repo.saveCalls != 3 {
		t.Fatalf("SaveIfAdvances calls = %d, want 3", repo.saveCalls)
	}
	if len(repo.saved) != 0 {
		t.Fatalf("successful saves = %d, want 0", len(repo.saved))
	}
	if diff := cmp.Diff([]int{1, 2, 3}, repo.expectedGens); diff != "" {
		t.Fatalf("expectedGeneration sequence mismatch (-want +got):\n%s", diff)
	}
}
