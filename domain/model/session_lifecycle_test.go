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

func TestSessionTerminateAppliesOneTerminalState(t *testing.T) {
	t.Parallel()

	session := newLifecycleTestSession(t, types.RuntimeModeOneShot)
	endedAt := session.StartedAt().Add(time.Minute)

	transition, err := session.Terminate(endedAt, types.TerminalReasonSuccess, "completed")
	if err != nil {
		t.Fatalf("Terminate() error = %v", err)
	}
	if transition != model.SessionTerminalTransitionApplied {
		t.Fatalf("Terminate() transition = %q, want applied", transition)
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

func TestSessionTerminateSameReasonIsIdempotent(t *testing.T) {
	t.Parallel()

	session := newLifecycleTestSession(t, types.RuntimeModeOneShot)
	firstAt := session.StartedAt().Add(time.Minute)
	if _, err := session.Terminate(firstAt, types.TerminalReasonTimeout, "first"); err != nil {
		t.Fatalf("first Terminate() error = %v", err)
	}

	transition, err := session.Terminate(firstAt.Add(time.Minute), types.TerminalReasonTimeout, "redelivery")
	if err != nil {
		t.Fatalf("duplicate Terminate() error = %v", err)
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

func TestSessionTerminateConflictFailsClosed(t *testing.T) {
	t.Parallel()

	session := newLifecycleTestSession(t, types.RuntimeModeOneShot)
	firstAt := session.StartedAt().Add(time.Minute)
	if _, err := session.Terminate(firstAt, types.TerminalReasonSuccess, "first"); err != nil {
		t.Fatalf("first Terminate() error = %v", err)
	}

	_, err := session.Terminate(firstAt.Add(time.Minute), types.TerminalReasonFailure, "conflict")
	if err == nil {
		t.Fatal("conflicting Terminate() error = nil")
	}
	if !errors.Is(err, model.ErrConflictingTerminalState) {
		t.Fatalf("conflicting Terminate() error = %v, want ErrConflictingTerminalState", err)
	}
	var conflict *model.SessionTerminalConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("conflicting Terminate() error = %T, want SessionTerminalConflictError", err)
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

func TestSessionTerminateRejectsInvalidInput(t *testing.T) {
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
			if _, err := session.Terminate(tt.endedAt, tt.reason, "summary"); err == nil {
				t.Fatal("Terminate() error = nil, want validation error")
			}
			if _, ok := session.EndedAt().Value(); ok {
				t.Fatal("invalid transition mutated EndedAt()")
			}
		})
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
