package runtime_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/duck8823/traceary/application"
	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"github.com/duck8823/traceary/infrastructure/filesystem"
)

type antigravityClaimChild struct {
	*fakeChild
	pid int
}

func (c *antigravityClaimChild) PID() int { return c.pid }

type antigravityClaimChildren struct {
	mu       sync.Mutex
	children []*antigravityClaimChild
}

func (f *antigravityClaimChildren) Start(_ context.Context, _ domain.LaunchDescriptor) (app.OwnedChild, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	child := &antigravityClaimChild{fakeChild: &fakeChild{exited: make(chan struct{})}, pid: 1000 + len(f.children)}
	f.children = append(f.children, child)
	return child, nil
}
func (f *antigravityClaimChildren) finish() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.children {
		c.finish()
	}
}

// Native field names come from the measured inventory. This is synthetic
// delivery/admission coverage, not another native launch or generation witness.
func TestAntigravityDecodedClaimsNeverPromoteReusedSessionToCurrentInteraction(t *testing.T) {
	_, req, base, registry := runtimeFixture(t, false)
	profile, err := domain.HostProfileFrom(domain.HostProfileState{ID: "codex-fixture", Host: "antigravity", Executable: "/owned/agent", ExecutableWitness: "fixture", Version: "1.5.0"})
	if err != nil {
		t.Fatal(err)
	}
	children := &antigravityClaimChildren{}
	t.Cleanup(children.finish)
	owner, err := app.NewRuntime(registry, launchProfiles{profile}, children, base.journal, req.Epoch)
	if err != nil {
		t.Fatal(err)
	}
	first, err := owner.Launch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	req.RequestID = "same-cwd-second"
	second, err := owner.Launch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if first.Target != second.Target || first.Identity == second.Identity {
		t.Fatal("same target lost separate generation")
	}
	decoder := filesystem.NewAntigravityObservationClaimDecoder()
	for _, run := range []domain.RunSnapshot{first, second} {
		for _, event := range []struct {
			name, id string
			kind     domain.ObservationKind
		}{
			{"UserPromptSubmit", "spool:new", domain.ObservationPromptBoundary},
			{"Stop", "spool:old", domain.ObservationTurnBoundary},
			{"UserPromptSubmit", "spool:new", domain.ObservationPromptBoundary},
			{"Stop", "spool:ancient", domain.ObservationTurnBoundary},
		} {
			claim, e := decoder.Decode(context.Background(), application.AntigravityObservationClaimInput{SourceVersion: profile.Snapshot().Version, ExpectedBoundary: event.kind, Payload: `{"conversationId":"00112233-4455-4677-8899-aabbccddeeff","fullyIdle":true,"body":{"turn_id":"untrusted-turn"},"timestamp":"1970-01-01T00:00:00Z","workspacePaths":["/project"]}`})
			if e != nil || !claim.Available {
				t.Fatal(claim, e)
			}
			metadata, e := domain.DerivedObservationOf(domain.DerivedObservationState{Host: "antigravity", HostVersion: profile.Snapshot().Version, Kind: event.kind, EventID: event.id, NativeSessionID: claim.SessionID})
			if e != nil {
				t.Fatal(e)
			}
			selected := app.SelectedObservation{Selection: app.OutputSelection{Identity: run.Identity, Worktree: run.Target.Worktree().ID(), Profile: run.Profile.ID()}, Metadata: metadata}
			if e = owner.RecordObservation(context.Background(), selected); e != nil {
				t.Fatal(e)
			}
			view, e := owner.SubscribeObservations(selected.Selection)
			if e != nil || view.Projection.Interaction != domain.ProjectionUnknown || view.Projection.Attention.EventID != "" || view.Projection.Process != domain.RunLive {
				t.Fatal("untrusted replay promoted interaction/exit", view, e)
			}
			// Claims do not repair an old generation, missing epoch, or other owner.
			foreign := selected
			foreign.Selection.Identity.Generation = second.Identity.Generation
			if run.Identity == first.Identity {
				if e = owner.RecordObservation(context.Background(), foreign); !errors.Is(e, domain.ErrChanged) {
					t.Fatal("foreign generation admitted", e)
				}
			}
			missing := selected
			missing.Selection.Identity.Epoch = ""
			if e = owner.RecordObservation(context.Background(), missing); !errors.Is(e, domain.ErrInvalid) {
				t.Fatal("missing epoch inferred from claim", e)
			}
		}
	}
	children.finish()
	if err = owner.AwaitIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
	old := selectedObservation(t, first, "spool:late-after-wait")
	if err = owner.RecordObservation(context.Background(), old); !errors.Is(err, app.ErrRuntimeRejected) {
		t.Fatal("late callback revived exited owner", err)
	}
}
