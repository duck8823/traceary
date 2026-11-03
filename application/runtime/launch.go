package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"golang.org/x/xerrors"
	"sort"
	"strings"
	"sync"
	"time"

	domain "github.com/duck8823/traceary/domain/runtime"
)

// ErrLaunchUnknown forbids replay when child creation/publication may have committed.
var ErrLaunchUnknown = errors.New("runtime launch outcome uncertain; inspect the original request without replay")

// TargetRegistry supplies immutable selections and fresh target checks, without OS DTOs.
type TargetRegistry interface {
	Resolve(context.Context, domain.WorktreeID, string) (domain.Target, error)
	Revalidate(context.Context, domain.Target, string) error
}

// ProfileCatalog supplies operator-owned policy, never request-provided commands.
type ProfileCatalog interface {
	LookupProfile(context.Context, domain.ProfileID) (domain.HostProfile, error)
}

// OwnedChild is a live child handle, not a saved PID. The adapter owns single Wait and output draining.
type OwnedChild interface {
	PID() int
	Wait() (int, error)
}

// ProcessLauncher may return an error only when no started child has escaped ownership.
// Once start commits, caller cancellation must never implicitly stop that child.
type ProcessLauncher interface {
	Start(context.Context, domain.LaunchDescriptor) (OwnedChild, error)
}

// LaunchJournal persists only safe launch metadata, independently of the core store.
type LaunchJournal interface {
	ReserveLaunch(context.Context, domain.RunSnapshot) error
	SaveLaunch(context.Context, domain.RunSnapshot) error
}

// LaunchRequest intentionally cannot select executable, argv, environment, cwd or store path.
type LaunchRequest struct {
	Epoch     string
	Worktree  domain.WorktreeID
	Profile   domain.ProfileID
	RequestID string
}
type launchTicket struct {
	request                                LaunchRequest
	ctx                                    context.Context
	cancel                                 context.CancelFunc
	done                                   chan struct{}
	waiters                                int
	starting, complete, aborted, abandoned bool
	descriptor                             domain.LaunchDescriptor
	result                                 domain.RunSnapshot
	err                                    error
}
type ownedRun struct {
	descriptor domain.LaunchDescriptor
	child      OwnedChild
	done       chan struct{}
	waitErr    error
}

// Runtime owns bounded launch reservations, immutable outcomes and live child handles.
// It has no core Session lifecycle or authority over externally started processes.
type Runtime struct {
	registry        TargetRegistry
	profiles        ProfileCatalog
	launcher        ProcessLauncher
	journal         LaunchJournal
	epoch           string
	mu              sync.Mutex
	tickets         map[string]*launchTicket
	runs            map[string]*ownedRun
	pending, active int
	disabled        bool
	revision        uint64
	changed         chan struct{}
}

// NewRuntime constructs an epoch-scoped launch owner with explicit ports.
func NewRuntime(registry TargetRegistry, profiles ProfileCatalog, launcher ProcessLauncher, journal LaunchJournal, epoch string) (*Runtime, error) {
	if registry == nil || profiles == nil || launcher == nil || journal == nil || !(domain.LaunchIdentity{Run: epoch, Generation: epoch, Epoch: epoch}).Valid() {
		return nil, domain.ErrInvalid
	}
	return &Runtime{registry: registry, profiles: profiles, launcher: launcher, journal: journal, epoch: epoch, tickets: make(map[string]*launchTicket), runs: make(map[string]*ownedRun), changed: make(chan struct{})}, nil
}
func validLaunchRequest(r LaunchRequest) bool {
	if len(r.RequestID) == 0 || len(r.RequestID) > 128 {
		return false
	}
	for _, c := range r.RequestID {
		if c < 33 || c > 126 {
			return false
		}
	}
	_, err := domain.ProfileIDFrom(string(r.Profile))
	return err == nil && len(r.Worktree) == 66 && strings.HasPrefix(string(r.Worktree), "w_")
}

// Launch single-flights identical requests. Pre-spawn work survives an individual
// waiter cancellation, but cancels if all waiters leave before Start is admitted.
func (s *Runtime) Launch(ctx context.Context, request LaunchRequest) (domain.RunSnapshot, error) {
	for {
		if err := ctx.Err(); err != nil {
			return domain.RunSnapshot{}, xerrors.Errorf("start owned runtime launch: %w", err)
		}
		if request.Epoch != s.epoch {
			return domain.RunSnapshot{}, domain.ErrChanged
		}
		if !validLaunchRequest(request) {
			return domain.RunSnapshot{}, domain.ErrInvalid
		}
		s.mu.Lock()
		ticket, exists := s.tickets[request.RequestID]
		if exists && ticket.request != request {
			s.mu.Unlock()
			return domain.RunSnapshot{}, domain.ErrChanged
		}
		if !exists {
			if s.disabled {
				s.mu.Unlock()
				return domain.RunSnapshot{}, ErrRuntimeBusy
			}
			if s.pending+s.active >= domain.MaxActiveRuns || len(s.tickets) >= domain.MaxLaunchRequests {
				s.mu.Unlock()
				return domain.RunSnapshot{}, domain.ErrLimit
			}
			coordinator, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			ticket = &launchTicket{request: request, ctx: coordinator, cancel: cancel, done: make(chan struct{})}
			s.tickets[request.RequestID] = ticket
			s.pending++
			s.changedLocked()
		}
		ticket.waiters++
		s.mu.Unlock()
		if !exists {
			go s.launch(ticket)
		}
		select {
		case <-ticket.done:
			s.mu.Lock()
			ticket.waiters--
			aborted, result, err := ticket.aborted, ticket.result, ticket.err
			s.mu.Unlock()
			if aborted {
				continue
			}
			return result, err
		case <-ctx.Done():
			s.mu.Lock()
			ticket.waiters--
			if ticket.complete && !ticket.aborted {
				result, err := ticket.result, ticket.err
				s.mu.Unlock()
				return result, err
			}
			if !ticket.starting {
				if ticket.waiters == 0 {
					ticket.abandoned = true
					ticket.cancel()
				}
				s.mu.Unlock()
				return domain.RunSnapshot{}, xerrors.Errorf("wait for owned runtime launch: %w", ctx.Err())
			}
			snapshot := ticket.descriptor.Snapshot()
			s.mu.Unlock()
			return snapshot, ErrLaunchUnknown
		}
	}
}
func (s *Runtime) launch(t *launchTicket) {
	defer t.cancel()
	target, err := s.registry.Resolve(t.ctx, t.request.Worktree, string(t.request.Profile))
	if err != nil {
		s.fail(t, err)
		return
	}
	profile, err := s.profiles.LookupProfile(t.ctx, t.request.Profile)
	if err != nil {
		s.fail(t, err)
		return
	}
	if target.Worktree().ID() != t.request.Worktree || profile.ID() != t.request.Profile {
		s.fail(t, domain.ErrChanged)
		return
	}
	id, err := newLaunchIdentity(s.epoch)
	if err != nil {
		s.fail(t, err)
		return
	}
	descriptor, err := domain.LaunchDescriptorOf(id, target, profile)
	if err != nil {
		s.fail(t, err)
		return
	}
	if err = s.registry.Revalidate(t.ctx, target, string(t.request.Profile)); err != nil {
		s.fail(t, err)
		return
	}
	s.mu.Lock()
	if err = t.ctx.Err(); err != nil {
		s.mu.Unlock()
		s.fail(t, err)
		return
	}
	t.starting = true
	t.descriptor = descriptor
	s.changedLocked()
	s.mu.Unlock()
	// Durable intent begins only after validated target and the non-abandonable
	// commit boundary. A late caller cancellation cannot erase/recreate it.
	if err = s.journal.ReserveLaunch(t.ctx, descriptor.Snapshot()); err != nil {
		s.fail(t, err)
		return
	}
	child, err := s.launcher.Start(t.ctx, descriptor)
	if err != nil {
		s.fail(t, err)
		return
	}
	// A successful adapter Start MUST transfer an actual owned child, even if the
	// caller has since canceled. Never drop or automatically kill that ownership.
	if child == nil {
		s.fail(t, ErrLaunchUnknown)
		return
	}
	err = descriptor.Started(child.PID())
	if err != nil {
		_ = descriptor.MarkUncertain()
	}
	run := &ownedRun{descriptor: descriptor, child: child, done: make(chan struct{})}
	s.mu.Lock()
	s.pending--
	s.active++
	s.runs[id.Run] = run
	s.changedLocked()
	t.descriptor = descriptor
	s.mu.Unlock()
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(t.ctx), 2*time.Second)
	journalErr := s.journal.SaveLaunch(persistCtx, descriptor.Snapshot())
	cancel()
	s.mu.Lock()
	if err != nil || journalErr != nil {
		_ = run.descriptor.MarkUncertain()
		t.err = ErrLaunchUnknown
	}
	t.result = run.descriptor.Snapshot()
	t.complete = true
	s.changedLocked()
	close(t.done)
	s.mu.Unlock()
	go s.reconcile(run)
}
func (s *Runtime) fail(t *launchTicket, err error) {
	s.mu.Lock()
	uncertain := t.starting && errors.Is(err, ErrLaunchUnknown)
	if t.starting {
		t.result = t.descriptor.Snapshot()
	}
	if uncertain {
		_ = t.descriptor.MarkUncertain()
		t.result = t.descriptor.Snapshot()
	} else {
		s.pending--
	}
	if !t.starting && t.abandoned {
		t.aborted = true
		delete(s.tickets, t.request.RequestID)
	} else {
		t.err = err
	}
	if uncertain {
		snapshot := t.result
		s.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = s.journal.SaveLaunch(ctx, snapshot)
		cancel()
		s.mu.Lock()
	}
	t.complete = true
	s.changedLocked()
	close(t.done)
	s.mu.Unlock()
}
func (s *Runtime) reconcile(run *ownedRun) {
	code, err := run.child.Wait()
	s.mu.Lock()
	if err == nil {
		err = run.descriptor.Exit(code)
	}
	if err != nil {
		_ = run.descriptor.MarkUncertain()
		run.waitErr = ErrLaunchUnknown
	}
	snapshot := run.descriptor.Snapshot()
	exitKnown := err == nil
	s.changedLocked()
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	journalErr := s.journal.SaveLaunch(ctx, snapshot)
	cancel()
	s.mu.Lock()
	if journalErr != nil {
		_ = run.descriptor.MarkUncertain()
	}
	if exitKnown {
		s.active--
	}
	s.changedLocked()
	close(run.done)
	s.mu.Unlock()
}

// List returns bounded independent snapshots, never terminal bytes or child handles.
func (s *Runtime) List() []domain.RunSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listLocked()
}
func (s *Runtime) listLocked() []domain.RunSnapshot {
	result := make([]domain.RunSnapshot, 0, len(s.runs))
	for _, run := range s.runs {
		result = append(result, run.descriptor.Snapshot())
	}
	// Preserve visibility of bounded uncertain Start reservations even when a
	// broken adapter could not transfer a usable child handle.
	for _, ticket := range s.tickets {
		snapshot := ticket.result
		if ticket.starting && !ticket.complete {
			snapshot = ticket.descriptor.Snapshot()
		}
		if snapshot.Identity.Run != "" && (snapshot.PublicationUncertain || ticket.starting && !ticket.complete) {
			if _, exists := s.runs[snapshot.Identity.Run]; !exists {
				result = append(result, snapshot)
			}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Identity.Run < result[j].Identity.Run })
	return result
}

// HasActive includes pending and uncertain ownership reservations.
func (s *Runtime) HasActive() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.pending+s.active > 0 }

// DisableLaunches stops NEW admissions without killing children or evicting receipts.
func (s *Runtime) DisableLaunches() {
	s.mu.Lock()
	if !s.disabled {
		s.disabled = true
		s.changedLocked()
	}
	s.mu.Unlock()
}

// Disabled reports launch admission, not native interaction state.
func (s *Runtime) Disabled() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.disabled }
func (s *Runtime) changedLocked() { s.revision++; close(s.changed); s.changed = make(chan struct{}) }

// AwaitIdle retains ownership through actual Wait and journal reconciliation; it never signals a child.
func (s *Runtime) AwaitIdle(ctx context.Context) error {
	for {
		s.mu.Lock()
		if s.pending+s.active == 0 {
			s.mu.Unlock()
			return nil
		}
		changed := s.changed
		s.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return xerrors.Errorf("await owned runtime reconciliation: %w", ctx.Err())
		}
	}
}

// Page rejects mixed-revision continuations and bounds metadata to two runs.
func (s *Runtime) Page(request RunPageRequest) (RunPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if request.Epoch != s.epoch {
		return RunPage{}, domain.ErrChanged
	}
	if request.Revision != 0 && request.Revision != s.revision {
		return RunPage{}, domain.ErrChanged
	}
	runs := s.listLocked()
	start := 0
	if request.Cursor != "" {
		if request.Revision == 0 {
			return RunPage{}, domain.ErrInvalid
		}
		index := sort.Search(len(runs), func(i int) bool { return runs[i].Identity.Run >= request.Cursor })
		if index == len(runs) || runs[index].Identity.Run != request.Cursor {
			return RunPage{}, domain.ErrChanged
		}
		start = index + 1
	}
	end := min(start+2, len(runs))
	page := RunPage{Epoch: s.epoch, Revision: s.revision, Runs: append([]domain.RunSnapshot{}, runs[start:end]...)}
	if end < len(runs) {
		page.Next = runs[end-1].Identity.Run
	}
	return page, nil
}

// AwaitExit validates the immutable generation; no PID lookup or control occurs.
func (s *Runtime) AwaitExit(ctx context.Context, id domain.LaunchIdentity) (domain.RunSnapshot, error) {
	for {
		s.mu.Lock()
		run, ok := s.runs[id.Run]
		if ok {
			if run.descriptor.Identity() != id {
				s.mu.Unlock()
				return domain.RunSnapshot{}, domain.ErrChanged
			}
			done := run.done
			s.mu.Unlock()
			select {
			case <-ctx.Done():
				return domain.RunSnapshot{}, xerrors.Errorf("wait for owned runtime exit: %w", ctx.Err())
			case <-done:
				s.mu.Lock()
				defer s.mu.Unlock()
				return run.descriptor.Snapshot(), run.waitErr
			}
		}
		var pending *launchTicket
		for _, ticket := range s.tickets {
			if ticket.starting && ticket.descriptor.Identity() == id {
				pending = ticket
				break
			}
		}
		if pending == nil {
			s.mu.Unlock()
			return domain.RunSnapshot{}, domain.ErrChanged
		}
		if pending.complete {
			result, err := pending.result, pending.err
			s.mu.Unlock()
			if err == nil {
				err = ErrLaunchUnknown
			}
			return result, err
		}
		done := pending.done
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return domain.RunSnapshot{}, xerrors.Errorf("wait for committed runtime launch: %w", ctx.Err())
		case <-done:
		}
	}
}
func newLaunchIdentity(epoch string) (domain.LaunchIdentity, error) {
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return domain.LaunchIdentity{}, domain.ErrUnsafe
	}
	return domain.LaunchIdentity{Run: hex.EncodeToString(random[:16]), Generation: hex.EncodeToString(random[16:]), Epoch: epoch}, nil
}
