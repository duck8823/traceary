package model_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestRunLineageReconcileExactReplayAndConflict(t *testing.T) {
	t.Parallel()
	identity, _ := types.RunIdentityFrom("codex", "run-1")
	packet, _ := types.PacketIdentityFrom(strings.Repeat("a", 64), 0)
	current, err := model.RunLineageOf(identity, types.None[types.RunIdentity](), types.None[types.SessionID](), types.EmptyRunWorkAttribution(), types.Some(packet), types.Some[int64](0))
	if err != nil {
		t.Fatal(err)
	}
	replay, _ := model.RunLineageOf(identity, types.None[types.RunIdentity](), types.None[types.SessionID](), types.EmptyRunWorkAttribution(), types.Some(packet), types.Some[int64](0))
	transition, err := current.Reconcile(replay)
	if err != nil || transition != model.RunLineageTransitionAlreadyApplied {
		t.Fatalf("replay = %q, %v", transition, err)
	}
	conflict, _ := model.RunLineageOf(identity, types.None[types.RunIdentity](), types.None[types.SessionID](), types.EmptyRunWorkAttribution(), types.Some(packet), types.Some[int64](1))
	if _, err := current.Reconcile(conflict); !errors.Is(err, model.ErrConflictingRunLineage) {
		t.Fatalf("conflict error = %v", err)
	}
}

func TestRunLineageRejectsSelfParent(t *testing.T) {
	t.Parallel()
	identity, _ := types.RunIdentityFrom("codex", "run-1")
	if _, err := model.RunLineageOf(identity, types.Some(identity), types.None[types.SessionID](), types.EmptyRunWorkAttribution(), types.None[types.PacketIdentity](), types.None[int64]()); !errors.Is(err, model.ErrInvalidRunLineage) {
		t.Fatalf("self-parent error = %v", err)
	}
}
