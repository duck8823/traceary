package model_test

import (
	"errors"
	"testing"
	"time"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestNewSessionWithRuntimeModeRejectsZeroMode(t *testing.T) {
	t.Parallel()

	agent, _ := types.AgentFrom("codex")
	sessionID, _ := types.SessionIDFrom("runtime-zero")
	_, err := model.NewSessionWithRuntimeMode(
		sessionID,
		time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC),
		types.Client("hook"),
		agent,
		types.Workspace("duck8823/traceary"),
		types.RuntimeMode(""),
	)
	if err == nil {
		t.Fatal("NewSessionWithRuntimeMode() error = nil, want invalid zero mode")
	}
}

func TestSessionFinalizeOneShotAppliesOneTerminalState(t *testing.T) {
	t.Parallel()

	session := newLifecycleTestSession(t, types.RuntimeModeOneShot)
	endedAt := session.StartedAt().Add(time.Minute)

	transition, err := session.FinalizeOneShot(endedAt, types.TerminalReasonSuccess, "completed")
	if err != nil {
		t.Fatalf("FinalizeOneShot() error = %v", err)
	}
	if transition != model.SessionTerminalTransitionApplied {
		t.Fatalf("FinalizeOneShot() transition = %q, want applied", transition)
	}
	if session.RuntimeMode() != types.RuntimeModeOneShot {
		t.Fatalf("RuntimeMode() = %q, want one_shot", session.RuntimeMode())
	}
	if got, ok := session.TerminalReason().Value(); !ok || got != types.TerminalReasonSuccess {
		t.Fatalf("TerminalReason() = %q/%v, want success/present", got, ok)
	}
	if got, ok := session.EndedAt().Value(); !ok || !got.Equal(endedAt) {
		t.Fatalf("EndedAt() = %v/%v, want %v/present", got, ok, endedAt)
	}
	if session.Summary() != "completed" {
		t.Fatalf("Summary() = %q, want completed", session.Summary())
	}
}

func TestSessionFinalizeOneShotSameReasonIsIdempotent(t *testing.T) {
	t.Parallel()

	session := newLifecycleTestSession(t, types.RuntimeModeOneShot)
	firstAt := session.StartedAt().Add(time.Minute)
	if _, err := session.FinalizeOneShot(firstAt, types.TerminalReasonTimeout, "first"); err != nil {
		t.Fatalf("first FinalizeOneShot() error = %v", err)
	}

	transition, err := session.FinalizeOneShot(firstAt.Add(time.Minute), types.TerminalReasonTimeout, "redelivery")
	if err != nil {
		t.Fatalf("duplicate FinalizeOneShot() error = %v", err)
	}
	if transition != model.SessionTerminalTransitionAlreadyApplied {
		t.Fatalf("duplicate transition = %q, want already_applied", transition)
	}
	if got, _ := session.EndedAt().Value(); !got.Equal(firstAt) {
		t.Fatalf("duplicate overwrote EndedAt() = %v, want %v", got, firstAt)
	}
	if session.Summary() != "first" {
		t.Fatalf("duplicate overwrote Summary() = %q, want first", session.Summary())
	}
}

func TestSessionFinalizeOneShotConflictFailsClosed(t *testing.T) {
	t.Parallel()

	session := newLifecycleTestSession(t, types.RuntimeModeOneShot)
	firstAt := session.StartedAt().Add(time.Minute)
	if _, err := session.FinalizeOneShot(firstAt, types.TerminalReasonSuccess, "first"); err != nil {
		t.Fatalf("first FinalizeOneShot() error = %v", err)
	}

	_, err := session.FinalizeOneShot(firstAt.Add(time.Minute), types.TerminalReasonFailure, "conflict")
	if err == nil {
		t.Fatal("conflicting FinalizeOneShot() error = nil")
	}
	if !errors.Is(err, model.ErrConflictingTerminalState) {
		t.Fatalf("conflicting FinalizeOneShot() error = %v, want ErrConflictingTerminalState", err)
	}
	var conflict *model.SessionTerminalConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("conflicting FinalizeOneShot() error = %T, want SessionTerminalConflictError", err)
	}
	if conflict.CurrentReason() != types.TerminalReasonSuccess || conflict.ProposedReason() != types.TerminalReasonFailure {
		t.Fatalf("conflict reasons = %q/%q", conflict.CurrentReason(), conflict.ProposedReason())
	}
	if got, _ := session.TerminalReason().Value(); got != types.TerminalReasonSuccess {
		t.Fatalf("conflict overwrote reason = %q, want success", got)
	}
	if got, _ := session.EndedAt().Value(); !got.Equal(firstAt) {
		t.Fatalf("conflict overwrote EndedAt() = %v, want %v", got, firstAt)
	}
	if session.Summary() != "first" {
		t.Fatalf("conflict overwrote Summary() = %q, want first", session.Summary())
	}
}

func TestSessionFinalizeOneShotRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		endedAt time.Time
		reason  types.TerminalReason
	}{
		{name: "zero time", endedAt: time.Time{}, reason: types.TerminalReasonSuccess},
		{name: "before start", endedAt: time.Date(2026, 7, 22, 11, 59, 0, 0, time.UTC), reason: types.TerminalReasonSuccess},
		{name: "zero reason", endedAt: time.Date(2026, 7, 22, 12, 1, 0, 0, time.UTC), reason: types.TerminalReason("")},
		{name: "unknown reason", endedAt: time.Date(2026, 7, 22, 12, 1, 0, 0, time.UTC), reason: types.TerminalReason("other")},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			session := newLifecycleTestSession(t, types.RuntimeModeOneShot)
			if _, err := session.FinalizeOneShot(tt.endedAt, tt.reason, "summary"); err == nil {
				t.Fatal("FinalizeOneShot() error = nil, want validation error")
			}
			if _, ok := session.EndedAt().Value(); ok {
				t.Fatal("invalid transition mutated EndedAt()")
			}
		})
	}
}

func TestSessionFinalizeOneShotRejectsInteractiveMode(t *testing.T) {
	t.Parallel()
	startedAt := time.Now().Add(-time.Minute)
	session := model.NewSession("interactive", startedAt, "cli", "codex", "workspace")
	if _, err := session.FinalizeOneShot(time.Now(), types.TerminalReasonSuccess, "done"); !errors.Is(err, model.ErrInvalidSessionState) {
		t.Fatalf("FinalizeOneShot() error = %v, want ErrInvalidSessionState", err)
	}
}

func TestNewSessionWithRuntimeModeAndParent(t *testing.T) {
	t.Parallel()

	agent, _ := types.AgentFrom("codex")
	session, err := model.NewSessionWithRuntimeModeAndParent(
		types.SessionID("child"),
		time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC),
		types.Client("cli"),
		agent,
		types.Workspace("workspace"),
		types.RuntimeModeOneShot,
		types.SessionID("parent"),
	)
	if err != nil {
		t.Fatalf("NewSessionWithRuntimeModeAndParent() error = %v", err)
	}
	if session.RuntimeMode() != types.RuntimeModeOneShot || session.ParentSessionID() != types.SessionID("parent") {
		t.Fatalf("session lifecycle = mode=%q parent=%q", session.RuntimeMode(), session.ParentSessionID())
	}
	if _, err := model.NewSessionWithRuntimeModeAndParent(
		types.SessionID("self"), time.Now(), types.Client("cli"), agent, types.Workspace("workspace"), types.RuntimeModeOneShot, types.SessionID("self"),
	); err == nil || !errors.Is(err, model.ErrInvalidSessionState) {
		t.Fatalf("self-parent error = %v, want ErrInvalidSessionState", err)
	}
}

func newLifecycleTestSession(t *testing.T, mode types.RuntimeMode) *model.Session {
	t.Helper()
	agent, _ := types.AgentFrom("codex")
	sessionID, _ := types.SessionIDFrom("lifecycle-" + mode.String())
	session, err := model.NewSessionWithRuntimeMode(
		sessionID,
		time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC),
		types.Client("hook"),
		agent,
		types.Workspace("duck8823/traceary"),
		mode,
	)
	if err != nil {
		t.Fatalf("NewSessionWithRuntimeMode() error = %v", err)
	}
	return session
}

func TestOrdinaryTerminalWritersRefuseOneShot(t *testing.T) {
	for _, ended := range []bool{false, true} {
		session := newLifecycleTestSession(t, types.RuntimeModeOneShot)
		at := session.StartedAt().Add(time.Minute)
		if ended {
			if _, err := session.FinalizeOneShot(at, types.TerminalReasonFailure, "human"); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := session.Terminate(at.Add(time.Minute), types.TerminalReasonSuccess, "forged"); err == nil {
			t.Fatal("ordinary Terminate accepted one-shot")
		}
		if err := session.End(at.Add(time.Minute), "forged"); err == nil {
			t.Fatal("ordinary End accepted one-shot")
		}
		if reason, ok := session.TerminalReason().Value(); ok != ended || (ok && reason != types.TerminalReasonFailure) {
			t.Fatal("result changed")
		}
	}
}

func TestOrdinaryTerminateKeepsInteractiveContract(t *testing.T) {
	session := newLifecycleTestSession(t, types.RuntimeModeInteractive)
	at := session.StartedAt().Add(time.Minute)
	transition, err := session.Terminate(at, types.TerminalReasonSuccess, "human summary")
	if err != nil || transition != model.SessionTerminalTransitionApplied {
		t.Fatalf("ordinary terminate = %s/%v", transition, err)
	}
	if session.Summary() != "human summary" {
		t.Fatal("ordinary summary changed")
	}
}
