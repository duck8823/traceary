package model_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestNewMemoryCandidate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 13, 9, 0, 0, 0, time.UTC)
	memoryID, _ := types.MemoryIDFrom("mem-1")
	evidence, _ := types.EvidenceRefFrom(types.EvidenceRefKindEvent, "event-1")
	artifact, _ := types.ArtifactRefFrom(types.ArtifactRefKindURL, "https://example.com/docs")
	memory, err := model.NewMemoryCandidateWithClock(
		memoryID,
		types.MemoryTypeDecision,
		types.WorkspaceScopeOf(types.Workspace("github.com/duck8823/traceary")),
		"  Release issues close only after tagged release  ",
		types.MemorySourceExtracted,
		[]types.EvidenceRef{evidence},
		[]types.ArtifactRef{artifact},
		types.None[types.MemoryID](),
		fakeClock{now: now},
	)
	if err != nil {
		t.Fatalf("NewMemoryCandidate() error = %v", err)
	}

	if diff := cmp.Diff(memoryID, memory.MemoryID()); diff != "" {
		t.Errorf("MemoryID() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(types.MemoryTypeDecision, memory.MemoryType()); diff != "" {
		t.Errorf("MemoryType() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("Release issues close only after tagged release", memory.Fact()); diff != "" {
		t.Errorf("Fact() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(types.MemoryStatusCandidate, memory.Status()); diff != "" {
		t.Errorf("Status() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(types.ConfidenceLow, memory.Confidence()); diff != "" {
		t.Errorf("Confidence() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(types.MemorySourceExtracted, memory.Source()); diff != "" {
		t.Errorf("Source() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(now, memory.CreatedAt()); diff != "" {
		t.Errorf("CreatedAt() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(now, memory.UpdatedAt()); diff != "" {
		t.Errorf("UpdatedAt() mismatch (-want +got):\n%s", diff)
	}
	evidenceRefsGot := memory.EvidenceRefs()
	if len(evidenceRefsGot) != 1 {
		t.Fatalf("len(EvidenceRefs()) = %d, want 1", len(evidenceRefsGot))
	}
	if evidenceRefsGot[0].Kind() != evidence.Kind() || evidenceRefsGot[0].Value() != evidence.Value() {
		t.Fatalf("EvidenceRefs()[0] = (%s, %s), want (%s, %s)", evidenceRefsGot[0].Kind(), evidenceRefsGot[0].Value(), evidence.Kind(), evidence.Value())
	}
	artifactRefsGot := memory.ArtifactRefs()
	if len(artifactRefsGot) != 1 {
		t.Fatalf("len(ArtifactRefs()) = %d, want 1", len(artifactRefsGot))
	}
	if artifactRefsGot[0].Kind() != artifact.Kind() || artifactRefsGot[0].Value() != artifact.Value() {
		t.Fatalf("ArtifactRefs()[0] = (%s, %s), want (%s, %s)", artifactRefsGot[0].Kind(), artifactRefsGot[0].Value(), artifact.Kind(), artifact.Value())
	}
}

func TestNewAcceptedMemory(t *testing.T) {
	t.Parallel()

	memoryID, _ := types.MemoryIDFrom("mem-2")
	previousID, _ := types.MemoryIDFrom("mem-1")
	memory, err := model.NewAcceptedMemory(
		memoryID,
		types.MemoryTypePreference,
		types.AgentScopeOf(types.Agent("codex")),
		"Use English for CLI output",
		types.ConfidenceVerified,
		types.MemorySourceManual,
		nil,
		nil,
		types.Some(previousID),
	)
	if err != nil {
		t.Fatalf("NewAcceptedMemory() error = %v", err)
	}

	if diff := cmp.Diff(types.MemoryStatusAccepted, memory.Status()); diff != "" {
		t.Errorf("Status() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(types.ConfidenceVerified, memory.Confidence()); diff != "" {
		t.Errorf("Confidence() mismatch (-want +got):\n%s", diff)
	}
	if supersedes, ok := memory.Supersedes().Value(); !ok {
		t.Fatalf("Supersedes() should be present")
	} else if diff := cmp.Diff(previousID, supersedes); diff != "" {
		t.Errorf("Supersedes() mismatch (-want +got):\n%s", diff)
	}
}

func TestMemory_StateTransitions(t *testing.T) {
	t.Parallel()

	newCandidate := func(t *testing.T) *model.Memory {
		t.Helper()
		memoryID, _ := types.MemoryIDFrom("mem-transition")
		memory, err := model.NewMemoryCandidate(
			memoryID,
			types.MemoryTypeLesson,
			types.SessionFamilyScopeOf(types.SessionID("session-family-1")),
			"Remember to rerun lint after hook changes",
			types.MemorySourceExtracted,
			nil,
			nil,
			types.None[types.MemoryID](),
		)
		if err != nil {
			t.Fatalf("NewMemoryCandidate() error = %v", err)
		}
		return memory
	}

	t.Run("accepts candidate memory", func(t *testing.T) {
		t.Parallel()
		memory := newCandidate(t)
		if err := memory.Accept(types.ConfidenceHigh); err != nil {
			t.Fatalf("Accept() error = %v", err)
		}
		if diff := cmp.Diff(types.MemoryStatusAccepted, memory.Status()); diff != "" {
			t.Errorf("Status() mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(types.ConfidenceHigh, memory.Confidence()); diff != "" {
			t.Errorf("Confidence() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("rejects candidate memory", func(t *testing.T) {
		t.Parallel()
		memory := newCandidate(t)
		if err := memory.Reject(); err != nil {
			t.Fatalf("Reject() error = %v", err)
		}
		if diff := cmp.Diff(types.MemoryStatusRejected, memory.Status()); diff != "" {
			t.Errorf("Status() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("marks accepted memory as superseded", func(t *testing.T) {
		t.Parallel()
		memory := newCandidate(t)
		if err := memory.Accept(types.ConfidenceMedium); err != nil {
			t.Fatalf("Accept() error = %v", err)
		}
		if err := memory.MarkSuperseded(); err != nil {
			t.Fatalf("MarkSuperseded() error = %v", err)
		}
		if diff := cmp.Diff(types.MemoryStatusSuperseded, memory.Status()); diff != "" {
			t.Errorf("Status() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("marks candidate memory as superseded", func(t *testing.T) {
		t.Parallel()
		memory := newCandidate(t)
		if err := memory.MarkSuperseded(); err != nil {
			t.Fatalf("MarkSuperseded() error = %v", err)
		}
		if diff := cmp.Diff(types.MemoryStatusSuperseded, memory.Status()); diff != "" {
			t.Errorf("Status() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("expires accepted memory", func(t *testing.T) {
		t.Parallel()
		memory := newCandidate(t)
		if err := memory.Accept(types.ConfidenceMedium); err != nil {
			t.Fatalf("Accept() error = %v", err)
		}
		expiresAt := time.Date(2026, 4, 14, 12, 0, 0, 0, time.UTC)
		if err := memory.Expire(expiresAt); err != nil {
			t.Fatalf("Expire() error = %v", err)
		}
		if diff := cmp.Diff(types.MemoryStatusExpired, memory.Status()); diff != "" {
			t.Errorf("Status() mismatch (-want +got):\n%s", diff)
		}
		if got, ok := memory.ExpiresAt().Value(); !ok {
			t.Fatalf("ExpiresAt() should be present")
		} else if diff := cmp.Diff(expiresAt, got); diff != "" {
			t.Errorf("ExpiresAt() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("rejecting accepted memory returns invalid state", func(t *testing.T) {
		t.Parallel()
		memory := newCandidate(t)
		if err := memory.Accept(types.ConfidenceMedium); err != nil {
			t.Fatalf("Accept() error = %v", err)
		}
		err := memory.Reject()
		if !errors.Is(err, model.ErrInvalidMemoryState) {
			t.Fatalf("Reject() error = %v, want ErrInvalidMemoryState", err)
		}
	})

	t.Run("accepting rejected memory returns invalid state", func(t *testing.T) {
		t.Parallel()
		memory := newCandidate(t)
		if err := memory.Reject(); err != nil {
			t.Fatalf("Reject() error = %v", err)
		}
		err := memory.Accept(types.ConfidenceHigh)
		if !errors.Is(err, model.ErrInvalidMemoryState) {
			t.Fatalf("Accept() error = %v, want ErrInvalidMemoryState", err)
		}
	})

	t.Run("superseding rejected memory returns invalid state", func(t *testing.T) {
		t.Parallel()
		memory := newCandidate(t)
		if err := memory.Reject(); err != nil {
			t.Fatalf("Reject() error = %v", err)
		}
		err := memory.MarkSuperseded()
		if !errors.Is(err, model.ErrInvalidMemoryState) {
			t.Fatalf("MarkSuperseded() error = %v, want ErrInvalidMemoryState", err)
		}
	})

	t.Run("expiring rejected memory returns invalid state", func(t *testing.T) {
		t.Parallel()
		memory := newCandidate(t)
		if err := memory.Reject(); err != nil {
			t.Fatalf("Reject() error = %v", err)
		}
		err := memory.Expire(time.Date(2026, 4, 14, 15, 0, 0, 0, time.UTC))
		if !errors.Is(err, model.ErrInvalidMemoryState) {
			t.Fatalf("Expire() error = %v, want ErrInvalidMemoryState", err)
		}
	})
}

func TestNewMemoryCandidate_ClonesSlices(t *testing.T) {
	t.Parallel()

	memoryID, _ := types.MemoryIDFrom("mem-clone")
	evidence, _ := types.EvidenceRefFrom(types.EvidenceRefKindEvent, "event-9")
	artifact, _ := types.ArtifactRefFrom(types.ArtifactRefKindFile, "docs/spec.md")
	evidenceRefs := []types.EvidenceRef{evidence}
	artifactRefs := []types.ArtifactRef{artifact}
	memory, err := model.NewMemoryCandidate(
		memoryID,
		types.MemoryTypeConstraint,
		types.WorkspaceScopeOf(types.Workspace("github.com/duck8823/traceary")),
		"Keep CLI messages in English",
		types.MemorySourceManual,
		evidenceRefs,
		artifactRefs,
		types.None[types.MemoryID](),
	)
	if err != nil {
		t.Fatalf("NewMemoryCandidate() error = %v", err)
	}

	evidenceRefs[0] = types.EvidenceRef{}
	artifactRefs[0] = types.ArtifactRef{}

	evidenceRefsGot := memory.EvidenceRefs()
	if len(evidenceRefsGot) != 1 {
		t.Fatalf("len(EvidenceRefs()) = %d, want 1", len(evidenceRefsGot))
	}
	if evidenceRefsGot[0].Kind() != evidence.Kind() || evidenceRefsGot[0].Value() != evidence.Value() {
		t.Fatalf("EvidenceRefs()[0] = (%s, %s), want (%s, %s)", evidenceRefsGot[0].Kind(), evidenceRefsGot[0].Value(), evidence.Kind(), evidence.Value())
	}
	artifactRefsGot := memory.ArtifactRefs()
	if len(artifactRefsGot) != 1 {
		t.Fatalf("len(ArtifactRefs()) = %d, want 1", len(artifactRefsGot))
	}
	if artifactRefsGot[0].Kind() != artifact.Kind() || artifactRefsGot[0].Value() != artifact.Value() {
		t.Fatalf("ArtifactRefs()[0] = (%s, %s), want (%s, %s)", artifactRefsGot[0].Kind(), artifactRefsGot[0].Value(), artifact.Kind(), artifact.Value())
	}
}
