package runtime

import (
	"context"
	"errors"
	"math"
	"sync"
	"time"

	domain "github.com/duck8823/traceary/domain/runtime"
	"golang.org/x/xerrors"
)

// ObservationRefreshBudget bounds the entire caller wait, not each retry/flight.
// MaxObservationContinuityGap is watcher continuity, NOT an agent-event TTL.
const (
	ObservationRefreshBudget    = 250 * time.Millisecond
	MaxObservationContinuityGap = 30 * time.Second
)

// ErrObservationUnknown means live evidence cannot be committed or read as
// current. It does not assert process death or grant input/resume authority.
var ErrObservationUnknown = errors.New("native observation continuity unknown")

// ContinuitySampler reads trusted local evidence. Unavailable native evidence is
// a closed sample, never a raw OS error. Implementations must honor cancellation
// where possible; the gate bounds waits even for an uninterruptible OS read.
type ContinuitySampler interface {
	Sample(context.Context) domain.ContinuitySample
}

// ObservationStamp binds the ORIGINAL intake to one gate/epoch/revision. Fields
// are private so client metadata cannot manufacture native continuity. The gate
// revision is independent of a per-run Freshness.Revision: NEVER interchange them.
type ObservationStamp struct {
	gate     *ObservationGate
	epoch    string
	revision uint64
	ctx      context.Context
}

var revokedObservationContext = func() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}()

// Context is revoked across a gap, wake or gate lifetime end. Cancellation alone
// is insufficient: consumers must also guard projection commit AND current read.
func (s ObservationStamp) Context() context.Context {
	if s.ctx == nil {
		return revokedObservationContext
	}
	return s.ctx
}

// WithCurrent guards a projection read or mutation with the original intake.
// fn must be bounded memory-only work: no I/O, gate reentry, or ownership locks.
// Native binding/causality validation happens BEFORE this guard, outside its lock.
func (s ObservationStamp) WithCurrent(ctx context.Context, fn func() error) error {
	if s.gate == nil {
		return ErrObservationUnknown
	}
	return s.gate.WithCurrent(ctx, s, fn)
}

type observationSampleFlight struct {
	done                chan struct{}
	request, generation uint64
	applied             bool
	revision            uint64
}

// ObservationGate serializes sample application without holding a memory lock
// over native I/O. At most ONE sampler goroutine can be blocked, regardless of
// intake volume. It cannot block Runtime ownership/management or Close. A hung
// OS call may outlive Close, but can never publish late evidence or gain rights.
type ObservationGate struct {
	mu                  sync.Mutex
	continuity          *domain.ObservationContinuity
	sampler             ContinuitySampler
	epoch               string
	ctx                 context.Context
	cancel              context.CancelFunc
	scope               context.Context
	cancelScope         context.CancelFunc
	flight              *observationSampleFlight
	request, generation uint64
	closed              bool
}

// NewObservationGate constructs a closed, epoch-local fence without native I/O.
func NewObservationGate(ctx context.Context, sampler ContinuitySampler, epoch string) (*ObservationGate, error) {
	if ctx == nil || sampler == nil || !(domain.LaunchIdentity{Epoch: epoch, Run: epoch, Generation: epoch}).Valid() {
		return nil, domain.ErrInvalid
	}
	continuity, err := domain.NewObservationContinuity(MaxObservationContinuityGap)
	if err != nil {
		return nil, xerrors.Errorf("create observation continuity: %w", err)
	}
	lifetime, cancel := context.WithCancel(ctx)
	scope, cancelScope := context.WithCancel(lifetime)
	cancelScope() // No intake authority exists before the first completed sample.
	return &ObservationGate{continuity: continuity, sampler: sampler, epoch: epoch, ctx: lifetime, cancel: cancel, scope: scope, cancelScope: cancelScope}, nil
}

// Snapshot is diagnostic, not a refreshed native projection read or intake stamp.
func (g *ObservationGate) Snapshot() domain.ContinuityState {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.continuity.Snapshot()
}

// Stamp refreshes native continuity before intake, NEVER at delayed dequeue.
func (g *ObservationGate) Stamp(ctx context.Context) (ObservationStamp, error) {
	revision, err := g.refresh(ctx)
	if err != nil {
		return ObservationStamp{}, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.currentLocked(ctx, revision) {
		return ObservationStamp{}, ErrObservationUnknown
	}
	return ObservationStamp{gate: g, epoch: g.epoch, revision: revision, ctx: g.scope}, nil
}

// WithCurrent refreshes again before validating the original immutable stamp.
// A healthy poll grants no native activity: only fn's independently validated
// per-binding evidence may update knowledge. Reading old evidence uses this SAME
// guard; an error must be rendered unknown, not the previously known projection.
func (g *ObservationGate) WithCurrent(ctx context.Context, stamp ObservationStamp, fn func() error) error {
	if stamp.gate != g || stamp.epoch != g.epoch || stamp.Context().Err() != nil || fn == nil {
		return ErrObservationUnknown
	}
	revision, err := g.refresh(ctx)
	if err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if revision != stamp.revision || !g.currentLocked(ctx, revision) || stamp.ctx != g.scope || stamp.Context().Err() != nil {
		return ErrObservationUnknown
	}
	return fn()
}

func (g *ObservationGate) currentLocked(ctx context.Context, revision uint64) bool {
	state := g.continuity.Snapshot()
	return !g.closed && ctx.Err() == nil && g.ctx.Err() == nil && state.Available && !state.Exhausted && state.Revision == revision && g.scope.Err() == nil
}

// InvalidateGap revokes queued/in-flight scopes without I/O. Queue owners call
// this AFTER releasing their queue mutex, never by nesting queue and gate locks.
func (g *ObservationGate) InvalidateGap() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.failClosedLocked()
}

// Close never waits for an uninterruptible sampler or releases process ownership.
func (g *ObservationGate) Close() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.closeLocked()
}

func (g *ObservationGate) closeLocked() {
	if !g.closed {
		g.closed = true
		g.cancel()
		g.cancelScope()
		_ = g.continuity.Observe(domain.ContinuitySample{})
	}
}

func (g *ObservationGate) failClosedLocked() {
	if g.closed {
		return
	}
	if g.generation == math.MaxUint64 {
		g.closeLocked()
		return
	}
	g.generation++ // Discard ANY sample started before this loss, even if healthy.
	g.applySampleLocked(domain.ContinuitySample{})
}

func (g *ObservationGate) applySampleLocked(sample domain.ContinuitySample) {
	before := g.continuity.Snapshot()
	_ = g.continuity.Observe(sample) // Exhaustion is represented by permanently closed state.
	after := g.continuity.Snapshot()
	if !after.Available {
		g.cancelScope()
	} else if !before.Available || before.Revision != after.Revision {
		g.cancelScope()
		g.scope, g.cancelScope = context.WithCancel(g.ctx)
	}
}

func (g *ObservationGate) refresh(ctx context.Context) (uint64, error) {
	if ctx.Err() != nil {
		return 0, ErrObservationUnknown
	}
	waitCtx, cancel := context.WithTimeout(ctx, ObservationRefreshBudget)
	defer cancel()
	g.mu.Lock()
	if g.closed || g.ctx.Err() != nil || g.request == math.MaxUint64 {
		g.closeLocked()
		g.mu.Unlock()
		return 0, ErrObservationUnknown
	}
	g.request++
	request := g.request
	g.mu.Unlock()
	for {
		g.mu.Lock()
		if g.closed || g.ctx.Err() != nil || waitCtx.Err() != nil {
			g.failClosedLocked()
			g.mu.Unlock()
			return 0, ErrObservationUnknown
		}
		flight := g.flight
		if flight == nil {
			flight = &observationSampleFlight{done: make(chan struct{}), request: g.request, generation: g.generation}
			g.flight = flight
			go g.sample(flight)
		}
		g.mu.Unlock()
		select {
		case <-waitCtx.Done():
			g.InvalidateGap()
			return 0, ErrObservationUnknown
		case <-g.ctx.Done():
			return 0, ErrObservationUnknown
		case <-flight.done:
		}
		g.mu.Lock()
		if !flight.applied || flight.generation != g.generation || g.closed || g.ctx.Err() != nil || waitCtx.Err() != nil {
			g.mu.Unlock()
			return 0, ErrObservationUnknown
		}
		g.mu.Unlock()
		if flight.request >= request {
			return flight.revision, nil
		}
		// A flight already started before this request cannot attest its intake.
		// Join its completion, then require a new read within the ORIGINAL deadline.
	}
}

func (g *ObservationGate) sample(flight *observationSampleFlight) {
	ctx, cancel := context.WithTimeout(g.ctx, ObservationRefreshBudget)
	defer cancel()
	sample := g.sampler.Sample(ctx) // No memory/queue/Runtime locks held over I/O.
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.closed && g.ctx.Err() == nil && flight.generation == g.generation {
		if ctx.Err() != nil {
			g.failClosedLocked()
		} else {
			g.applySampleLocked(sample)
			state := g.continuity.Snapshot()
			flight.applied = state.Available && !state.Exhausted
			flight.revision = state.Revision
		}
	}
	g.flight = nil
	close(flight.done)
}
