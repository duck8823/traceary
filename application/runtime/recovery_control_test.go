package runtime_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"golang.org/x/xerrors"
)

// This journal exposes the real pre-publication owner window through public
// APIs. It is not a native confirmation or recovery-certification fixture.
type publicationControlJournal struct {
	*controlJournal
	entered, release chan struct{}
	once             sync.Once
}

func (j *publicationControlJournal) SaveLaunch(ctx context.Context, run domain.RunSnapshot) error {
	if run.State == domain.RunLive {
		j.once.Do(func() { close(j.entered) })
		select {
		case <-j.release:
		case <-ctx.Done():
			return xerrors.Errorf("wait for fixture publication: %w", ctx.Err())
		}
	}
	return j.launchJournal.SaveLaunch(ctx, run)
}

func TestUnpublishedOwnedRunRejectsNewInputButAllowsExplicitHumanStop(t *testing.T) {
	_, launch, base, registry := runtimeFixture(t, false)
	profile, err := domain.HostProfileFrom(domain.HostProfileState{ID: "codex-fixture", Host: "codex", Executable: "/owned/agent", ExecutableWitness: "fixture", Version: "fixture 1"})
	if err != nil {
		t.Fatal(err)
	}
	child := &controlChild{fakeChild: base.child, result: app.ControlEffect{Outcome: domain.ControlDelivered}}
	journal := &publicationControlJournal{
		controlJournal: &controlJournal{launchJournal: base.journal}, entered: make(chan struct{}), release: make(chan struct{}),
	}
	owner, err := app.NewRuntime(registry, launchProfiles{profile}, controlFactory{child}, journal, launch.Epoch)
	if err != nil {
		t.Fatal(err)
	}
	var release sync.Once
	defer func() { release.Do(func() { close(journal.release) }); child.finish() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := owner.Launch(ctx, launch); result <- err }()
	select {
	case <-journal.entered:
	case <-ctx.Done():
		t.Fatal("actual launch did not reach the publication boundary")
	}
	runs := owner.List()
	if len(runs) != 1 || runs[0].State != domain.RunLive {
		t.Fatal("no actual owned child before publication")
	}
	selection := app.OutputSelection{Identity: runs[0].Identity, Worktree: runs[0].Target.Worktree().ID(), Profile: runs[0].Profile.ID()}
	input, err := domain.ControlIntentFrom(domain.ControlIntentState{RequestID: "input-after-confirmation", Kind: domain.ControlInput, ManualTerminal: true, InputBytes: 3})
	if err != nil {
		t.Fatal(err)
	}
	request := app.ControlRequest{Selection: selection, Intent: input, Data: []byte("new")}
	if _, err := owner.Control(ctx, request); !errors.Is(err, app.ErrRuntimeRejected) || child.effects.Load() != 0 {
		t.Fatalf("input reached an unpublished owner: effects=%d error=%v", child.effects.Load(), err)
	}
	metadata, err := domain.DerivedObservationOf(domain.DerivedObservationState{Host: "codex", HostVersion: "fixture 1", Kind: domain.ObservationPromptBoundary, EventID: "unconfirmed"})
	if err != nil {
		t.Fatal(err)
	}
	observation, err := app.SelectedObservationOf(selection, metadata)
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.CheckObservation(ctx, observation); !errors.Is(err, app.ErrRuntimeRejected) {
		t.Fatalf("unpublished owner accepted observation: %v", err)
	}
	stop, err := domain.ControlIntentFrom(domain.ControlIntentState{RequestID: "explicit-stop", Kind: domain.ControlStop, WorkLossAcknowledged: true})
	if err != nil {
		t.Fatal(err)
	}
	stopped, err := owner.Control(ctx, app.ControlRequest{Selection: selection, Intent: stop})
	if err != nil || stopped.Receipt.Outcome != domain.ControlDelivered || child.effects.Load() != 1 {
		t.Fatalf("explicit stop of the actual child was unavailable: %v", err)
	}
	child.result = app.ControlEffect{Outcome: domain.ControlDelivered, WrittenBytes: 3}
	release.Do(func() { close(journal.release) })
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	value, err := owner.Control(ctx, request)
	if err != nil || value.Receipt.Outcome != domain.ControlDelivered || child.effects.Load() != 2 {
		t.Fatalf("published owner rejected the original unconsumed input request: %v", err)
	}
	if _, err := owner.Control(ctx, request); err != nil || child.effects.Load() != 2 {
		t.Fatal("input receipt inspection replayed the effect")
	}
	if err := owner.CheckObservation(ctx, observation); err != nil {
		t.Fatalf("publication did not open observation admission: %v", err)
	}
}
