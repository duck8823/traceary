package runtime

import (
	"context"
	"golang.org/x/xerrors"
	"io"
	"strings"
	"sync"
	"sync/atomic"

	domain "github.com/duck8823/traceary/domain/runtime"
)

// OutputSelection binds a reader to one actual owned launch, never a saved PID.
type OutputSelection struct {
	Identity domain.LaunchIdentity
	Worktree domain.WorktreeID
	Profile  domain.ProfileID
}

// OutputLimits may tighten but never expand the product bounds; zero selects defaults.
type OutputLimits struct{ RetainedBytes, PendingBytes int }

// ResizeEvidence reports the last owner-applied geometry, not historical byte layout.
type ResizeEvidence struct {
	Sequence   uint64
	Rows, Cols uint16
}

// OutputInfo separates capture completion from owned process exit.
type OutputInfo struct {
	Selection  OutputSelection
	Base, Head domain.ByteOffset
	Ended      bool
	Resize     ResizeEvidence
	Reason     CaptureReason
}

// OutputGap never asserts remote receipt of output.
type OutputGap struct {
	From, To        domain.ByteOffset
	Reason          string
	DeliveryUnknown bool
}

// OutputFrame is a snapshot, byte chunk, explicit gap, or terminal capture marker.
type OutputFrame struct {
	Kind       string
	Info       OutputInfo
	Start, End domain.ByteOffset
	Data       []byte
	Gap        *OutputGap
}

// CaptureReason is independent of the process lifecycle.
type CaptureReason string

// Capture completion reasons do not assert owned process exit.
const (
	CaptureClosed      CaptureReason = "closed"
	CaptureTailUnknown CaptureReason = "tail_unknown"
	CaptureReadError   CaptureReason = "read_error"
)

// Output owns bounded volatile scrollback and constant-size subscriber cursors.
// No terminal content is persisted, interpreted, or delivered on the producer goroutine.
type Output struct {
	mu             sync.Mutex
	selection      OutputSelection
	log            *domain.ByteLog
	pending        int
	changed        chan struct{}
	subs           map[*OutputSubscription]struct{}
	ended, unknown bool
	reason         CaptureReason
	resize         ResizeEvidence
	resizeSet      bool
}

// NewOutput creates a bounded volatile capture owner.
func NewOutput(selection OutputSelection, limits OutputLimits) (*Output, error) {
	if !selection.Identity.Valid() || len(selection.Worktree) != 66 || !strings.HasPrefix(string(selection.Worktree), "w_") {
		return nil, domain.ErrInvalid
	}
	for _, c := range selection.Worktree[2:] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return nil, domain.ErrInvalid
		}
	}
	if _, err := domain.ProfileIDFrom(string(selection.Profile)); err != nil {
		return nil, xerrors.Errorf("initialize runtime output: %w", err)
	}
	if limits.RetainedBytes == 0 {
		limits.RetainedBytes = domain.MaxOutputBytes
	}
	if limits.PendingBytes == 0 {
		limits.PendingBytes = domain.MaxOutputPending
	}
	if limits.PendingBytes < 1 || limits.PendingBytes > domain.MaxOutputPending {
		return nil, domain.ErrLimit
	}
	log, err := domain.NewByteLog(limits.RetainedBytes)
	if err != nil {
		return nil, xerrors.Errorf("initialize runtime output: %w", err)
	}
	return &Output{selection: selection, log: log, pending: limits.PendingBytes, changed: make(chan struct{}), subs: make(map[*OutputSubscription]struct{})}, nil
}
func (o *Output) signalLocked() { close(o.changed); o.changed = make(chan struct{}) }
func (o *Output) infoLocked() OutputInfo {
	s := o.log.Snapshot()
	return OutputInfo{Selection: o.selection, Base: s.Base, Head: s.Head, Ended: o.ended, Resize: o.resize, Reason: o.reason}
}
func pendingCost(n uint64) uint64 {
	if n == 0 {
		return 0
	}
	return ((n+2)/3)*4 + 2048*((n+domain.MaxOutputChunk-1)/domain.MaxOutputChunk) + 2048
}

// Append never waits for any subscriber or socket; slow readers lose only their subscription.
func (o *Output) Append(p []byte) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.ended {
		return io.ErrClosedPipe
	}
	if _, err := o.log.Append(p); err != nil {
		return xerrors.Errorf("append runtime output: %w", err)
	}
	head := o.log.Snapshot().Head
	for s := range o.subs {
		live := head - max(s.cursor, s.attachHead)
		// Avoid overflowing wire-cost arithmetic for a theoretical uint64 lifetime.
		if live > domain.MaxOutputPending || pendingCost(uint64(live)) > uint64(o.pending) {
			s.detached = &OutputGap{From: s.cursor, To: head, Reason: "slow_consumer", DeliveryUnknown: true}
			delete(o.subs, s)
		}
	}
	o.signalLocked()
	return nil
}

// Finish is idempotent; unknown tails are explicit even when their byte range is unknowable.
func (o *Output) Finish(reason CaptureReason, unknown bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.ended {
		return
	}
	o.ended = true
	o.reason = reason
	o.unknown = unknown
	o.signalLocked()
}

// AcknowledgeResize accepts only owner-supplied verified evidence; it never polls terminal text.
func (o *Output) AcknowledgeResize(selection OutputSelection, sequence uint64, rows, cols uint16) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if selection != o.selection {
		return domain.ErrChanged
	}
	if rows == 0 || cols == 0 {
		return domain.ErrInvalid
	}
	next := ResizeEvidence{sequence, rows, cols}
	if o.resizeSet {
		if next == o.resize {
			return nil
		}
		if sequence <= o.resize.Sequence {
			return domain.ErrChanged
		}
	} else if sequence != 0 || rows != 24 || cols != 80 {
		return domain.ErrChanged
	}
	o.resize = next
	o.resizeSet = true
	o.signalLocked()
	return nil
}

// OutputSubscription owns no child handle and closing it cannot terminate capture.
type OutputSubscription struct {
	owner                     *Output
	cursor, attachHead        domain.ByteOffset
	initial, closed, tailSent bool
	detached                  *OutputGap
	reading                   atomic.Bool
}

// Subscribe binds a cursor without granting any process-control capability.
func (o *Output) Subscribe(selection OutputSelection, cursor domain.ByteOffset) (*OutputSubscription, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if selection != o.selection {
		return nil, domain.ErrChanged
	}
	if cursor > o.log.Snapshot().Head {
		return nil, domain.ErrOutputCursor
	}
	if len(o.subs) >= domain.MaxOutputSubscribers {
		return nil, domain.ErrLimit
	}
	s := &OutputSubscription{owner: o, cursor: cursor, attachHead: o.log.Snapshot().Head}
	o.subs[s] = struct{}{}
	return s, nil
}

// Close releases only this subscriber and never stops its capture owner.
func (s *OutputSubscription) Close() {
	o := s.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	if !s.closed {
		s.closed = true
		delete(o.subs, s)
		o.signalLocked()
	}
}

// Next allows a single consumer and detaches on cancellation, never affecting its producer.
func (s *OutputSubscription) Next(ctx context.Context) (OutputFrame, error) {
	if !s.reading.CompareAndSwap(false, true) {
		return OutputFrame{}, ErrRuntimeBusy
	}
	defer s.reading.Store(false)
	o := s.owner
	for {
		if err := ctx.Err(); err != nil {
			s.Close()
			return OutputFrame{}, xerrors.Errorf("read runtime output: %w", err)
		}
		o.mu.Lock()
		if s.closed {
			o.mu.Unlock()
			return OutputFrame{}, io.EOF
		}
		info := o.infoLocked()
		frame, ready := s.nextLocked(info)
		changed := o.changed
		o.mu.Unlock()
		if ready {
			return frame, nil
		}
		select {
		case <-ctx.Done():
			s.Close()
			return OutputFrame{}, xerrors.Errorf("read runtime output: %w", ctx.Err())
		case <-changed:
		}
	}
}
func (s *OutputSubscription) nextLocked(info OutputInfo) (OutputFrame, bool) {
	o := s.owner
	if !s.initial {
		s.initial = true
		return OutputFrame{Kind: "snapshot", Info: info}, true
	}
	if s.detached != nil {
		gap := *s.detached
		s.detached = nil
		s.closed = true
		return OutputFrame{Kind: "gap", Info: info, Gap: &gap}, true
	}
	span, _ := o.log.Read(s.cursor, domain.MaxOutputChunk)
	if span.Gap {
		s.cursor = span.End
		return OutputFrame{Kind: "gap", Info: info, Gap: &OutputGap{From: span.Start, To: span.End, Reason: "evicted"}}, true
	}
	if len(span.Data) > 0 {
		s.cursor = span.End
		return OutputFrame{Kind: "data", Info: info, Start: span.Start, End: span.End, Data: span.Data}, true
	}
	if o.ended {
		if o.unknown && !s.tailSent {
			s.tailSent = true
			return OutputFrame{Kind: "gap", Info: info, Gap: &OutputGap{From: s.cursor, To: s.cursor, Reason: string(o.reason), DeliveryUnknown: true}}, true
		}
		s.closed = true
		delete(o.subs, s)
		return OutputFrame{Kind: "end", Info: info}, true
	}
	return OutputFrame{}, false
}

// OutputChild is an optional live capability, separate from the mandatory PID/Wait port.
type OutputChild interface {
	SubscribeOutput(OutputSelection, domain.ByteOffset) (*OutputSubscription, error)
}

// Attach validates all immutable references against this epoch's actual child ownership.
// Exited and publication-uncertain owned runs remain readable; untransferred tickets do not.
func (s *Runtime) Attach(selection OutputSelection, cursor domain.ByteOffset) (*OutputSubscription, error) {
	s.mu.Lock()
	run := s.runs[selection.Identity.Run]
	if run == nil {
		s.mu.Unlock()
		return nil, domain.ErrNotFound
	}
	snapshot := run.descriptor.Snapshot()
	expected := OutputSelection{Identity: snapshot.Identity, Worktree: snapshot.Target.Worktree().ID(), Profile: snapshot.Profile.ID()}
	child := run.child
	s.mu.Unlock()
	if selection != expected {
		return nil, domain.ErrChanged
	}
	source, ok := child.(OutputChild)
	if !ok {
		return nil, domain.ErrUnsupported
	}
	sub, err := source.SubscribeOutput(selection, cursor)
	if err != nil {
		return nil, xerrors.Errorf("attach owned runtime output: %w", err)
	}
	return sub, nil
}

// OutputStream reads bounded untrusted bytes; Close detaches without controlling a process.
type OutputStream interface {
	Next(context.Context) (OutputFrame, error)
	Close() error
}

// OutputClient adds optional streaming without widening launch-only clients.
type OutputClient interface {
	Attach(context.Context, string, OutputSelection, domain.ByteOffset) (OutputStream, error)
}
