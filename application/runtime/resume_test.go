package runtime_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"golang.org/x/xerrors"
)

// The fake journal models trusted bookkeeping only. Atomic filesystem/CAS
// integrity is independently exercised by ExecutionStore's real-root tests.
type resumeJournal struct {
	failReserve, failLaunchReserve, failLive, failRecord bool
	failPhase                                            domain.ResumePhase
	*controlJournal
	mu       sync.Mutex
	records  []domain.RecoveryRecordState
	attempts map[string]domain.ResumeAttemptState
}

func (j *resumeJournal) ListRecovery(context.Context) ([]domain.RecoveryRecordState, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]domain.RecoveryRecordState{}, j.records...), nil
}
func (j *resumeJournal) ListResumeAttempts(context.Context) ([]domain.ResumeAttemptState, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	var out []domain.ResumeAttemptState
	for _, a := range j.attempts {
		out = append(out, a)
	}
	return out, nil
}
func (j *resumeJournal) SaveRecovery(_ context.Context, s domain.RecoveryRecordState) error {
	if j.failRecord {
		return domain.ErrUnsafe
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	for i, r := range j.records {
		if r.Binding.Snapshot().Identity == s.Binding.Snapshot().Identity {
			j.records[i] = s
			return nil
		}
	}
	j.records = append(j.records, s)
	return nil
}
func (j *resumeJournal) ReserveResume(_ context.Context, s domain.ResumeAttemptState) (domain.ResumeAttemptState, bool, error) {
	if j.failReserve {
		return domain.ResumeAttemptState{}, false, domain.ErrUnsafe
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if old, ok := j.attempts[s.RequestID]; ok {
		return old, false, nil
	}
	for _, old := range j.attempts {
		if old.Binding.ConversationKey() == s.Binding.ConversationKey() && old.Phase != domain.ResumeFailedBeforeStart {
			return domain.ResumeAttemptState{}, false, domain.ErrChanged
		}
	}
	j.attempts[s.RequestID] = s
	return s, true, nil
}
func (j *resumeJournal) SaveResume(_ context.Context, before, after domain.ResumeAttemptState) error {
	if after.Phase == j.failPhase {
		return domain.ErrUnsafe
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	old, ok := j.attempts[before.RequestID]
	if !ok {
		return domain.ErrNotFound
	}
	if old == after {
		return nil
	}
	if old != before {
		return domain.ErrChanged
	}
	j.attempts[before.RequestID] = after
	return nil
}

func (j *resumeJournal) ReserveLaunch(ctx context.Context, s domain.RunSnapshot) error {
	if j.failLaunchReserve {
		return domain.ErrUnsafe
	}
	return j.launchJournal.ReserveLaunch(ctx, s)
}
func (j *resumeJournal) SaveLaunch(ctx context.Context, s domain.RunSnapshot) error {
	if j.failLive && s.State == domain.RunLive {
		return domain.ErrUnsafe
	}
	return j.launchJournal.SaveLaunch(ctx, s)
}

type resumeOwnedChild struct {
	*controlChild
	binding domain.RecoveryBinding
	gate    chan struct{}
	entered chan struct{}
	once    sync.Once
}

func (c *resumeOwnedChild) RecoveryBinding(ctx context.Context) (domain.RecoveryBinding, error) {
	if c.gate != nil {
		c.once.Do(func() { close(c.entered) })
		select {
		case <-c.gate:
		case <-ctx.Done():
			return domain.RecoveryBinding{}, xerrors.Errorf("fixture native confirmation: %w", ctx.Err())
		}
	}
	return c.binding, nil
}

type nativeResumeFactory struct {
	child          *resumeOwnedChild
	calls          atomic.Int32
	fail, mismatch bool
	noChild        bool
	startErr       error
}

func (f *nativeResumeFactory) Start(context.Context, domain.LaunchDescriptor) (app.OwnedChild, error) {
	return nil, domain.ErrUnsupported
}
func (f *nativeResumeFactory) RevalidateRecovery(context.Context, domain.RecoveryBinding) error {
	if f.fail {
		return domain.ErrChanged
	}
	return nil
}
func (f *nativeResumeFactory) Resume(_ context.Context, d domain.LaunchDescriptor, old domain.RecoveryBinding) (app.OwnedChild, error) {
	f.calls.Add(1)
	if f.noChild {
		return nil, f.startErr
	}
	b := old.Snapshot()
	b.Identity = d.Identity()
	b.PID = f.child.PID()
	if f.mismatch {
		b.NativeSessionID = "another-conversation"
	}
	binding, err := domain.RecoveryBindingOf(b)
	if err != nil {
		return nil, xerrors.Errorf("fixture resume binding: %w", err)
	}
	f.child.binding = binding
	return f.child, f.startErr
}
func resumeFixture(t *testing.T) (*app.Runtime, app.ResumeRequest, *nativeResumeFactory, *resumeJournal, *launchRegistry) {
	t.Helper()
	_, launch, base, registry := runtimeFixture(t, false)
	profile, err := domain.HostProfileFrom(domain.HostProfileState{ID: "codex-fixture", Host: "codex", Executable: "/owned/agent", ExecutableWitness: "fixture", Version: "fixture 1"})
	if err != nil {
		t.Fatal(err)
	}
	old := domain.LaunchIdentity{Run: strings.Repeat("1", 32), Generation: strings.Repeat("2", 32), Epoch: strings.Repeat("3", 32)}
	binding, err := domain.RecoveryBindingOf(domain.RecoveryBindingState{Identity: old, Target: registry.target, Profile: profile, PID: 321, NativeSessionID: "original-native", NativeHistoryPath: "/native/original.jsonl", NativeNamespaceWitness: strings.Repeat("a", 64), Model: "synthetic-model", Provider: "synthetic-provider"})
	if err != nil {
		t.Fatal(err)
	}
	record, err := domain.NewRecoveryRecord(binding)
	if err != nil {
		t.Fatal(err)
	}
	if err := record.ObserveOwnedExit(old, 7); err != nil {
		t.Fatal(err)
	}
	journal := &resumeJournal{controlJournal: &controlJournal{launchJournal: base.journal}, records: []domain.RecoveryRecordState{record.Snapshot()}, attempts: map[string]domain.ResumeAttemptState{}}
	factory := &nativeResumeFactory{child: &resumeOwnedChild{controlChild: &controlChild{fakeChild: base.child, result: app.ControlEffect{Outcome: domain.ControlDelivered}}}}
	owner, err := app.NewRuntime(registry, launchProfiles{profile}, factory, journal, launch.Epoch)
	if err != nil {
		t.Fatal(err)
	}
	return owner, app.ResumeRequest{Epoch: launch.Epoch, Selection: app.OutputSelection{Identity: old, Worktree: registry.target.Worktree().ID(), Profile: profile.ID()}, RequestID: "resume-request", WorkLossAcknowledged: true}, factory, journal, registry
}
func TestRuntimeResumeConcurrentDuplicatesCreateOneConfirmedManagedOwner(t *testing.T) {
	owner, request, factory, _, _ := resumeFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			result, err := owner.Resume(ctx, request)
			if err != nil || result.Phase != domain.ResumeConfirmed || !result.Owned || result.Selection.Identity == request.Selection.Identity {
				t.Errorf("resume: %+v %v", result, err)
			}
		})
	}
	wg.Wait()
	if factory.calls.Load() != 1 || len(owner.List()) != 1 {
		t.Fatal("duplicate native Start")
	}
	factory.child.finish()
	if err := owner.AwaitIdle(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeResumedExitPreservesActualFinalModelOnlyAfterPublication(t *testing.T) {
	for _, kind := range []string{"published model change", "unpublished model change", "published provider change", "published conversation change"} {
		t.Run(kind, func(t *testing.T) {
			owner, request, factory, journal, _ := resumeFixture(t)
			if kind == "unpublished model change" {
				factory.startErr = domain.ErrUnsafe
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			result, err := owner.Resume(ctx, request)
			if kind == "unpublished model change" {
				if !errors.Is(err, app.ErrLaunchUnknown) || !result.Owned {
					t.Fatalf("unknown owned resume lost: %+v %v", result, err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			b := factory.child.binding.Snapshot()
			switch kind {
			case "published provider change":
				b.Provider = "foreign"
			case "published conversation change":
				b.NativeSessionID = "foreign"
			default:
				b.Model = "actual-final-model"
			}
			binding, err := domain.RecoveryBindingOf(b)
			if err != nil {
				t.Fatal(err)
			}
			factory.child.binding = binding // Before closing the Wait gate; no concurrent source query.
			factory.child.finish()
			if err := owner.AwaitIdle(ctx); err != nil {
				t.Fatal(err)
			}
			records, err := journal.ListRecovery(ctx)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, record := range records {
				if record.Binding.Snapshot().Identity == result.Selection.Identity && record.ExitObserved {
					found = true
					if record.Binding != binding {
						t.Fatal("final native binding lost")
					}
				}
			}
			if found != (kind == "published model change") {
				t.Fatalf("wrong final-model/identity acceptance: %v", found)
			}
		})
	}
}
func TestRuntimeResumeRefusesUnacknowledgedOrUnknownOldExit(t *testing.T) {
	owner, request, factory, journal, _ := resumeFixture(t)
	request.WorkLossAcknowledged = false
	if _, err := owner.Resume(context.Background(), request); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("missing consent admitted: %v", err)
	}
	request.WorkLossAcknowledged = true
	journal.records[0].ExitObserved = false
	journal.records[0].ExitCode = 0
	if _, err := owner.Resume(context.Background(), request); !errors.Is(err, domain.ErrChanged) {
		t.Fatalf("unknown old exit qualified: %v", err)
	}
	if factory.calls.Load() != 0 {
		t.Fatal("started before positive managed exit")
	}
}

func TestRuntimeResumeCanceledCallerRetainsUnconfirmedChildAndExplicitStop(t *testing.T) {
	owner, request, factory, _, _ := resumeFixture(t)
	factory.child.gate = make(chan struct{})
	factory.child.entered = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type response struct {
		result app.ResumeResult
		err    error
	}
	first := make(chan response, 1)
	go func() { r, e := owner.Resume(ctx, request); first <- response{r, e} }()
	select {
	case <-factory.child.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("confirmation not reached")
	}
	runs := owner.List()
	if len(runs) != 1 {
		t.Fatal("actual resumed child lost before confirmation")
	}
	selection := app.OutputSelection{Identity: runs[0].Identity, Worktree: runs[0].Target.Worktree().ID(), Profile: runs[0].Profile.ID()}
	input, err := domain.ControlIntentFrom(domain.ControlIntentState{RequestID: "new-input", Kind: domain.ControlInput, ManualTerminal: true, InputBytes: 3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Control(context.Background(), app.ControlRequest{Selection: selection, Intent: input, Data: []byte("new")}); !errors.Is(err, app.ErrRuntimeRejected) {
		t.Fatalf("input reached unconfirmed child: %v", err)
	}
	stop, err := domain.ControlIntentFrom(domain.ControlIntentState{RequestID: "human-stop", Kind: domain.ControlStop, WorkLossAcknowledged: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Control(context.Background(), app.ControlRequest{Selection: selection, Intent: stop}); err != nil {
		t.Fatalf("explicit Stop unavailable: %v", err)
	}
	cancel()
	got := <-first
	if !errors.Is(got.err, app.ErrLaunchUnknown) || !got.result.Owned || got.result.Selection != selection {
		t.Fatalf("canceled caller discarded child: %+v %v", got.result, got.err)
	}
	select {
	case <-factory.child.exited:
		t.Fatal("cancellation automatically killed child")
	default:
	}
	close(factory.child.gate)
	wait, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	result, err := owner.Resume(wait, request)
	if err != nil || result.Phase != domain.ResumeConfirmed || factory.calls.Load() != 1 {
		t.Fatalf("duplicate did not inspect same operation: %+v %v", result, err)
	}
	if factory.child.effects.Load() != 1 {
		t.Fatal("pending input was replayed")
	}
	factory.child.finish()
	if err := owner.AwaitIdle(wait); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeResumeStoredOutcomesNeverReconstructOwnershipOrStart(t *testing.T) {
	for _, phase := range []domain.ResumePhase{domain.ResumeReserved, domain.ResumeUnknown, domain.ResumeConfirmed} {
		t.Run(string(phase), func(t *testing.T) {
			owner, request, factory, journal, registry := resumeFixture(t)
			record, err := domain.RecoveryRecordFrom(journal.records[0])
			if err != nil {
				t.Fatal(err)
			}
			next := domain.LaunchIdentity{Run: strings.Repeat("4", 32), Generation: strings.Repeat("5", 32), Epoch: request.Epoch}
			attempt, err := domain.NewResumeAttempt(request.RequestID, record, next)
			if err != nil {
				t.Fatal(err)
			}
			if phase == domain.ResumeUnknown {
				if err := attempt.MarkUnknown(); err != nil {
					t.Fatal(err)
				}
			}
			if phase == domain.ResumeConfirmed {
				if err := attempt.MarkStarted(); err != nil {
					t.Fatal(err)
				}
				b := record.Snapshot().Binding.Snapshot()
				b.Identity = next
				b.PID = 999
				confirmed, err := domain.RecoveryBindingOf(b)
				if err != nil {
					t.Fatal(err)
				}
				if err := attempt.Confirm(confirmed); err != nil {
					t.Fatal(err)
				}
			}
			journal.attempts[request.RequestID] = attempt.Snapshot()
			// Even a currently unsupported launcher can inspect persisted outcomes.
			restarted, err := app.NewRuntime(registry, launchProfiles{record.Snapshot().Binding.Snapshot().Profile}, &launchFactory{}, journal, request.Epoch)
			if err != nil {
				t.Fatal(err)
			}
			result, err := restarted.Resume(context.Background(), request)
			if phase == domain.ResumeConfirmed {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, app.ErrLaunchUnknown) {
				t.Fatalf("saved uncertainty rearmed: %v", err)
			}
			if result.Phase != phase || result.Owned || result.Selection.Identity != next || factory.calls.Load() != 0 || restarted.HasActive() || len(restarted.List()) != 0 {
				t.Fatalf("history adopted as live owner: %+v", result)
			}
			request.Selection.Identity.Run = strings.Repeat("8", 32)
			if _, err := owner.Resume(context.Background(), request); !errors.Is(err, domain.ErrChanged) {
				t.Fatalf("request selection changed silently: %v", err)
			}
		})
	}
}

func TestRuntimeResumeMismatchedNativeIdentityRetainsChildButNeverRebindsHistory(t *testing.T) {
	owner, request, factory, journal, _ := resumeFixture(t)
	factory.mismatch = true
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := owner.Resume(ctx, request)
	if !errors.Is(err, app.ErrLaunchUnknown) || !result.Owned || result.Phase != domain.ResumeUnknown || factory.calls.Load() != 1 {
		t.Fatalf("native mismatch accepted: %+v %v", result, err)
	}
	select {
	case <-factory.child.exited:
		t.Fatal("mismatch automatically killed child")
	default:
	}
	factory.child.finish()
	if err := owner.AwaitIdle(ctx); err != nil {
		t.Fatal(err)
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if len(journal.records) != 1 || journal.attempts[request.RequestID].Phase != domain.ResumeUnknown {
		t.Fatal("mismatched native conversation promoted at exit")
	}
}

func TestRuntimeResumeFailureWindowsNeverReplayOrLoseActualChild(t *testing.T) {
	for _, kind := range []string{"intent", "execution reservation", "known no start", "unknown start", "live execution", "started outcome", "native binding", "confirmed outcome"} {
		t.Run(kind, func(t *testing.T) {
			owner, request, factory, journal, _ := resumeFixture(t)
			owned := true
			wantCalls := int32(1)
			wantPhase := domain.ResumeUnknown
			switch kind {
			case "intent":
				journal.failReserve = true
				owned = false
				wantCalls = 0
			case "execution reservation":
				journal.failLaunchReserve = true
				owned = false
				wantCalls = 0
				wantPhase = domain.ResumeFailedBeforeStart
			case "known no start":
				factory.noChild = true
				factory.startErr = domain.ErrUnsupported
				owned = false
				wantPhase = domain.ResumeFailedBeforeStart
			case "unknown start":
				factory.noChild = true
				factory.startErr = app.ErrLaunchUnknown
				owned = false
			case "live execution":
				journal.failLive = true
			case "started outcome":
				journal.failPhase = domain.ResumeStarted
			case "native binding":
				journal.failRecord = true
			case "confirmed outcome":
				journal.failPhase = domain.ResumeConfirmed
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			result, err := owner.Resume(ctx, request)
			if err == nil || result.Owned != owned || result.Phase != wantPhase || factory.calls.Load() != wantCalls {
				t.Fatalf("failure outcome: %+v %v calls=%d", result, err, factory.calls.Load())
			}
			again, err := owner.Resume(ctx, request)
			if err == nil || again != result || factory.calls.Load() != wantCalls {
				t.Fatalf("failure replayed: %+v %v", again, err)
			}
			if owned {
				select {
				case <-factory.child.exited:
					t.Fatal("failed publication automatically killed child")
				default:
				}
				factory.child.finish()
				if err := owner.AwaitIdle(ctx); err != nil {
					t.Fatal(err)
				}
			} else if kind == "unknown start" {
				if !owner.HasActive() {
					t.Fatal("ambiguous native Start released ownership reservation")
				}
			} else if owner.HasActive() {
				t.Fatal("known no-child operation leaked capacity")
			}
		})
	}
}

func TestRuntimeResumeRefusesDisabledChangedTargetAndNativeSourceBeforeStart(t *testing.T) {
	for _, kind := range []string{"disabled", "target", "source", "selection"} {
		t.Run(kind, func(t *testing.T) {
			owner, request, factory, _, registry := resumeFixture(t)
			want := domain.ErrChanged
			switch kind {
			case "disabled":
				owner.DisableLaunches()
				want = app.ErrRuntimeBusy
			case "target":
				registry.changed.Store(true)
			case "source":
				factory.fail = true
			case "selection":
				request.Selection.Identity.Generation = strings.Repeat("9", 32)
			}
			if _, err := owner.Resume(context.Background(), request); !errors.Is(err, want) {
				t.Fatalf("preflight bypass: %v", err)
			}
			if factory.calls.Load() != 0 || owner.HasActive() {
				t.Fatal("rejected resume started or leaked capacity")
			}
		})
	}
}
