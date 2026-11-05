//go:build darwin || linux

package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/xerrors"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
)

// Roots are lifetime claims, bounded to 32 even if an injected consumer ignores
// cancellation. The OS lock separately excludes other processes.
var daemonRoots = struct {
	sync.Mutex
	owned map[string]bool
}{owned: make(map[string]bool)}
var runtimeClientSlots = make(chan struct{}, MaxRuntimeConnections)

func claimDaemonRoot(witness string) (func(), error) {
	daemonRoots.Lock()
	defer daemonRoots.Unlock()
	if daemonRoots.owned[witness] || len(daemonRoots.owned) >= MaxRuntimeConnections {
		return nil, app.ErrRuntimeBusy
	}
	daemonRoots.owned[witness] = true
	return func() { daemonRoots.Lock(); delete(daemonRoots.owned, witness); daemonRoots.Unlock() }, nil
}
func validRuntimeRoot(root string) bool {
	return filepath.IsAbs(root) && filepath.Clean(root) == root && !strings.ContainsAny(root, "\x00\r\n") && len(filepath.Join(root, "observations.sock")) <= runtimeSocketPathLimit
}
func runtimeSocketStat(s unix.Stat_t) bool {
	return s.Uid == uint32(os.Geteuid()) && s.Mode&unix.S_IFMT == unix.S_IFSOCK && s.Mode&0777 == 0600 && s.Nlink == 1
}
func sameSocket(a, b unix.Stat_t) bool {
	return runtimeSocketStat(a) && runtimeSocketStat(b) && sameSocketIdentity(a, b)
}

type daemonSocket struct {
	name, role string
	listener   *net.UnixListener
	stamp      unix.Stat_t
	slots      chan struct{}
}
type pendingObservation struct {
	selected *app.ObservationReceipt
	metadata app.ObservationMetadata
	bytes    int
	stamp    app.ObservationStamp
}
type daemonServer struct {
	history               *app.History
	historyHints          atomic.Uint64
	endpointMetadata      *RegistryStore
	previousEndpoints     *endpointReceipt
	staleEndpoints        map[string]unix.Stat_t
	daemon                *LocalDaemon
	ctx                   context.Context
	cancel                context.CancelFunc
	root, witness, epoch  string
	rootFD, leaseFD       int
	releaseClaim          func()
	leaseStamp            unix.Stat_t
	endpoints             []*daemonSocket
	connections           map[*net.UnixConn]struct{}
	connectionMu          sync.Mutex
	replyMu               sync.Mutex
	effectReplies         map[*net.UnixConn]chan struct{}
	effectAdmissionClosed bool
	network               sync.WaitGroup
	failure               chan error
	queueMu               sync.Mutex
	queue                 []pendingObservation
	queueBytes            int
	notify                chan struct{}
	workerDone            chan struct{}
	workerStarted         bool
	gate                  *app.ObservationGate
	powerDone             chan struct{}
	powerStarted          bool
	updateDone            chan struct{}
	updateStarted         bool
	degraded              atomic.Bool
	releaseOnce           sync.Once
	runtime               *app.Runtime
}

// Serve owns the two local endpoints. It starts no agent and performs no core-store I/O.
func (d *LocalDaemon) Serve(ctx context.Context, root string) error {
	if err := ctx.Err(); err != nil {
		return xerrors.Errorf("start local daemon: %w", err)
	}
	if !d.serving.CompareAndSwap(false, true) {
		return app.ErrRuntimeBusy
	}
	s, err := prepareDaemon(ctx, d, root)
	if err != nil {
		return err
	}
	defer s.shutdown()
	s.workerStarted = true
	go s.consume()
	s.powerStarted = true
	go s.watchContinuity()
	if s.runtime != nil {
		s.updateStarted = true
		go s.watchUpdateNotices()
	}
	for _, endpoint := range s.endpoints {
		s.network.Add(1)
		go s.accept(endpoint)
	}
	var confirmed <-chan struct{}
	if s.runtime != nil {
		accepted := make(chan struct{})
		confirmed = accepted
		go func() {
			if s.runtime.AwaitShutdownConfirmation(s.ctx) == nil {
				close(accepted)
			}
		}()
	}
	normalConfirmation := false
	select {
	case <-ctx.Done():
	case <-s.ctx.Done():
	case <-confirmed:
		normalConfirmation = true
	}
	if s.runtime != nil {
		s.runtime.DisableLaunches()
		// A caller/transport interruption is not permission to close active PTYs.
		// Keep owner/lease and surviving management endpoints until owned reconciliation.
		_ = s.runtime.AwaitIdle(context.Background())
		// OS logout/shutdown can interrupt this bounded best-effort publication.
		// Endpoints/lease remain until actual ownership has already joined.
		publishCtx, publishCancel := context.WithTimeout(context.Background(), 2*time.Second)
		publishErr := s.runtime.PublishCleanShutdown(publishCtx)
		publishCancel()
		if publishErr != nil && !errors.Is(publishErr, domain.ErrUnsupported) {
			return xerrors.Errorf("finish daemon lifecycle publication: %w", publishErr)
		}
	}
	if normalConfirmation {
		replyCtx, replyCancel := context.WithTimeout(context.Background(), s.daemon.options.IOTimeout)
		_ = s.awaitEffectReplies(replyCtx) // Best effort only; EOF/timeout remains unknown.
		replyCancel()
	}
	select {
	case err := <-s.failure:
		return err
	default:
		return nil
	}
}
func prepareDaemon(parent context.Context, d *LocalDaemon, root string) (*daemonServer, error) {
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	s := &daemonServer{daemon: d, ctx: ctx, cancel: cancel, root: root, rootFD: -1, leaseFD: -1, connections: make(map[*net.UnixConn]struct{}), failure: make(chan error, 1), notify: make(chan struct{}, 1), workerDone: make(chan struct{})}
	complete := false
	defer func() {
		if !complete {
			s.shutdown()
		}
	}()
	if !validRuntimeRoot(root) {
		return nil, app.ErrUnsafeRuntime
	}
	fd, err := privateRuntimeDir(root, true)
	if err != nil {
		return nil, app.ErrUnsafeRuntime
	}
	s.rootFD = fd
	s.witness, err = directoryWitness(fd)
	if err != nil {
		return nil, app.ErrUnsafeRuntime
	}
	s.releaseClaim, err = claimDaemonRoot(s.witness)
	if err != nil {
		return nil, err
	}
	lock, err := openPrivateLock(fd, "daemon.lock")
	if err != nil {
		return nil, app.ErrUnsafeRuntime
	}
	s.leaseFD = lock
	var stat unix.Stat_t
	if unix.Fstat(lock, &stat) != nil || !privateRegular(stat) {
		return nil, app.ErrUnsafeRuntime
	}
	s.leaseStamp = stat
	if err = unix.Flock(lock, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, app.ErrRuntimeBusy
		}
		return nil, app.ErrUnsafeRuntime
	}
	if !s.leaseCurrent() {
		return nil, app.ErrUnsafeRuntime
	}
	random := make([]byte, 16)
	if _, err = rand.Read(random); err != nil {
		return nil, app.ErrDaemonUnavailable
	}
	s.epoch = hex.EncodeToString(random)
	if err = s.preflightEndpoints(parent); err != nil {
		return nil, err
	}
	if d.options.HistoryFactory != nil {
		s.history, err = d.options.HistoryFactory(parent, root)
		if err != nil {
			return nil, err
		}
		if s.history == nil {
			return nil, app.ErrRuntimeUnsupported
		}
	}
	for _, endpoint := range []struct {
		name, role string
		limit      int
	}{{"daemon.sock", "management", d.options.ManagementConnections}, {"observations.sock", "observations", d.options.ObservationConnections}} {
		if err = parent.Err(); err != nil {
			return nil, xerrors.Errorf("start local endpoints: %w", err)
		}
		owned, err := s.listen(parent, endpoint.name, endpoint.role, endpoint.limit)
		if err != nil {
			return nil, err
		}
		s.endpoints = append(s.endpoints, owned)
	}
	if err = s.publishEndpointReceipt(parent); err != nil {
		return nil, err
	}
	if d.options.RuntimeFactory != nil {
		s.runtime, err = d.options.RuntimeFactory(parent, root, s.epoch)
		if err != nil {
			return nil, err
		}
		if s.runtime == nil {
			return nil, app.ErrRuntimeUnsupported
		}
	}
	s.gate, err = app.NewObservationGate(s.ctx, d.sampler, s.epoch)
	if err != nil {
		return nil, xerrors.Errorf("create daemon observation gate: %w", err)
	}
	s.powerDone = make(chan struct{})
	s.updateDone = make(chan struct{})
	complete = true
	return s, nil
}
func (s *daemonServer) listen(ctx context.Context, name, role string, limit int) (*daemonSocket, error) {
	if err := verifyRuntimeRoot(s.root, s.witness, s.rootFD); err != nil {
		return nil, app.ErrUnsafeRuntime
	}
	var existing unix.Stat_t
	err := unix.Fstatat(s.rootFD, name, &existing, unix.AT_SYMLINK_NOFOLLOW)
	if err == nil {
		if !runtimeSocketStat(existing) {
			return nil, app.ErrUnsafeRuntime
		}
		if err = s.reclaimEndpoint(ctx, name, existing); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, unix.ENOENT) {
		return nil, app.ErrUnsafeRuntime
	} else if _, wasPresent := s.staleEndpoints[name]; wasPresent {
		return nil, app.ErrUnsafeRuntime
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(s.root, name), Net: "unix"})
	if err != nil {
		if errors.Is(err, unix.EADDRINUSE) || errors.Is(err, unix.EEXIST) {
			return nil, app.ErrRuntimeBusy
		}
		return nil, app.ErrDaemonUnavailable
	}
	listener.SetUnlinkOnClose(false)
	endpoint := &daemonSocket{name: name, role: role, listener: listener, slots: make(chan struct{}, limit)}
	// Only a successfully observed socket in the pinned directory becomes owned.
	if unix.Fstatat(s.rootFD, name, &endpoint.stamp, unix.AT_SYMLINK_NOFOLLOW) != nil || endpoint.stamp.Mode&unix.S_IFMT != unix.S_IFSOCK || endpoint.stamp.Uid != uint32(os.Geteuid()) {
		_ = listener.Close()
		return nil, app.ErrUnsafeRuntime
	}
	if err = unix.Fchmodat(s.rootFD, name, 0600, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		_ = listener.Close()
		s.cleanupSocket(endpoint)
		return nil, app.ErrUnsafeRuntime
	}
	var current unix.Stat_t
	if unix.Fstatat(s.rootFD, name, &current, unix.AT_SYMLINK_NOFOLLOW) != nil || current.Dev != endpoint.stamp.Dev || current.Ino != endpoint.stamp.Ino || !runtimeSocketStat(current) || verifyRuntimeRoot(s.root, s.witness, s.rootFD) != nil {
		_ = listener.Close()
		s.cleanupSocket(endpoint)
		return nil, app.ErrUnsafeRuntime
	}
	endpoint.stamp = current
	return endpoint, nil
}
func (s *daemonServer) cleanupSocket(endpoint *daemonSocket) {
	if s.rootFD < 0 || verifyRuntimeRoot(s.root, s.witness, s.rootFD) != nil {
		return
	}
	var current unix.Stat_t
	if unix.Fstatat(s.rootFD, endpoint.name, &current, unix.AT_SYMLINK_NOFOLLOW) == nil && current.Uid == uint32(os.Geteuid()) && current.Mode&unix.S_IFMT == unix.S_IFSOCK && sameSocketIdentity(current, endpoint.stamp) {
		_ = unix.Unlinkat(s.rootFD, endpoint.name, 0)
	}
}
func (s *daemonServer) release() {
	s.releaseOnce.Do(func() {
		if s.leaseFD >= 0 {
			_ = unix.Flock(s.leaseFD, unix.LOCK_UN)
			_ = unix.Close(s.leaseFD)
		}
		if s.rootFD >= 0 {
			_ = unix.Close(s.rootFD)
		}
		if s.releaseClaim != nil {
			s.releaseClaim()
		}
		s.daemon.serving.Store(false)
	})
}
func (s *daemonServer) shutdown() {
	s.cancel()
	if s.gate != nil {
		s.gate.Close() // Never joins an uninterruptible native read.
	}
	for _, endpoint := range s.endpoints {
		_ = endpoint.listener.Close()
	}
	s.connectionMu.Lock()
	for conn := range s.connections {
		_ = conn.Close()
	}
	s.connectionMu.Unlock()
	s.network.Wait()
	if s.powerStarted {
		<-s.powerDone // Watcher's gate wait is bounded/cancellable, not the OS call.
	}
	if s.updateStarted {
		<-s.updateDone // Actual lifecycle coordinators were already joined by AwaitIdle.
	}
	for _, endpoint := range s.endpoints {
		s.cleanupSocket(endpoint)
	}
	if !s.workerStarted {
		close(s.workerDone)
	}
	select {
	case <-s.workerDone:
		s.release()
	case <-time.After(OptionalRuntimeBudget):
		go func() { <-s.workerDone; s.release() }()
	}
}
func (s *daemonServer) accept(endpoint *daemonSocket) {
	defer s.network.Done()
	for {
		conn, err := endpoint.listener.AcceptUnix()
		if err != nil {
			if s.ctx.Err() == nil {
				select {
				case s.failure <- app.ErrDaemonUnavailable:
				default:
				}
				s.cancel()
			}
			return
		}
		// Authenticate before even an admission-error reply; a busy endpoint
		// must not become an unchecked peer path.
		if verifyRuntimePeer(conn) != nil {
			_ = conn.Close()
			continue
		}
		select {
		case endpoint.slots <- struct{}{}:
		default:
			_ = conn.SetWriteDeadline(time.Now().Add(s.daemon.options.IOTimeout))
			_ = writeRuntimeFrame(conn, runtimeResponse{Version: RuntimeProtocolVersion, Code: "busy", Role: endpoint.role})
			_ = conn.Close()
			continue
		}
		s.connectionMu.Lock()
		s.connections[conn] = struct{}{}
		s.connectionMu.Unlock()
		s.network.Add(1)
		go func() {
			defer s.network.Done()
			defer func() {
				<-endpoint.slots
				s.connectionMu.Lock()
				delete(s.connections, conn)
				s.connectionMu.Unlock()
				_ = conn.Close()
			}()
			s.handle(conn, endpoint)
		}()
	}
}
func (s *daemonServer) leaseCurrent() bool {
	var current unix.Stat_t
	return s.leaseFD >= 0 && unix.Fstatat(s.rootFD, "daemon.lock", &current, unix.AT_SYMLINK_NOFOLLOW) == nil && privateRegular(current) && current.Dev == s.leaseStamp.Dev && current.Ino == s.leaseStamp.Ino
}

func (s *daemonServer) endpointCurrent(endpoint *daemonSocket) bool {
	if !s.leaseCurrent() {
		return false
	}
	if verifyRuntimeRoot(s.root, s.witness, s.rootFD) != nil {
		return false
	}
	var current unix.Stat_t
	return unix.Fstatat(s.rootFD, endpoint.name, &current, unix.AT_SYMLINK_NOFOLLOW) == nil && sameSocket(current, endpoint.stamp)
}
func (s *daemonServer) handle(conn *net.UnixConn, endpoint *daemonSocket) {
	stop := context.AfterFunc(s.ctx, func() { _ = conn.Close() })
	defer stop()
	defer s.endEffectReply(conn)
	if verifyRuntimePeer(conn) != nil || !s.endpointCurrent(endpoint) {
		return
	}
	seen := make(map[string]struct{})
	hello := true
	for {
		if conn.SetDeadline(time.Now().Add(s.daemon.options.IOTimeout)) != nil {
			return
		}
		data, err := readRuntimeFrame(conn)
		if err != nil {
			return
		}
		var request runtimeRequest
		err = decodeRuntimeJSON(data, &request)
		if err != nil {
			code := "invalid"
			if errors.Is(err, app.ErrRuntimeUnsupported) {
				code = "unsupported"
			}
			_ = writeRuntimeFrame(conn, runtimeResponse{Version: RuntimeProtocolVersion, Code: code})
			return
		}
		if !boundedRuntimeID(request.ID, false) || !boundedRuntimeID(request.Operation, false) || len(request.Operation) > 64 || !s.endpointCurrent(endpoint) {
			_ = writeRuntimeFrame(conn, runtimeResponse{Version: RuntimeProtocolVersion, Code: "invalid"})
			return
		}
		if _, duplicate := seen[request.ID]; duplicate || len(seen) >= 1024 {
			_ = writeRuntimeFrame(conn, runtimeResponse{Version: RuntimeProtocolVersion, ID: request.ID, Code: "invalid"})
			return
		}
		seen[request.ID] = struct{}{}
		reply := runtimeResponse{Version: RuntimeProtocolVersion, ID: request.ID}
		if request.Control != nil {
			clear(data)
		}
		if !request.singleArm() {
			reply.Code = "invalid"
			_ = writeRuntimeFrame(conn, reply)
			return
		}
		if hello {
			if request.Operation != "hello" || request.Role != endpoint.role || request != (runtimeRequest{Version: request.Version, ID: request.ID, Operation: request.Operation, Role: request.Role}) {
				reply.Code = "invalid"
				_ = writeRuntimeFrame(conn, reply)
				return
			}
			reply.Code = "ok"
			reply.Role = endpoint.role
			hello = false
		} else if request.Role != "" {
			reply.Code = "invalid"
		} else if request.SelectedObservation != nil || request.Operation == selectedObservationOperation {
			s.selectedObservationReply(&reply, request, endpoint, len(data))
		} else if request.History != nil {
			if endpoint.role != "management" {
				reply.Code = "unsupported"
			} else {
				s.historyReply(&reply, request.History)
			}
		} else if request.HistoryHint != nil {
			if endpoint.role != "observations" {
				reply.Code = "unsupported"
			} else {
				s.historyHintReply(&reply)
			}
		} else if request.Lifecycle != nil {
			clean := endpoint.role == "management" && request.Control == nil && request.Receipt == nil && request.Attach == nil && request.Observation == nil && request.Launch == nil && request.Page == nil
			if clean {
				admitted := true
				if request.Operation == "shutdown_confirm" || request.Operation == "update_handoff" {
					admitted = s.beginEffectReply(conn)
				}
				if admitted {
					s.lifecycleReply(&reply, request.Lifecycle, request.Operation)
				} else {
					reply.Code = "busy"
				}
			} else {
				reply.Code = "unsupported"
			}
		} else if request.Resume != nil || request.Operation == "resume" {
			if endpoint.role != "management" {
				reply.Code = "unsupported"
			} else if request.Resume == nil {
				reply.Code = "invalid"
			} else {
				s.resumeReply(&reply, request.Resume)
			}
		} else if request.Control != nil || request.Receipt != nil {
			clear(data)
			clean := request.Attach == nil && request.Observation == nil && request.Launch == nil && request.Page == nil && endpoint.role == "management"
			switch {
			case clean && request.Control != nil && request.Receipt == nil && (request.Operation == "input" || request.Operation == "resize" || request.Operation == "stop"):
				if request.Operation != "stop" || s.beginEffectReply(conn) {
					s.controlReply(&reply, request.Control, request.Operation)
				} else {
					reply.Code = "busy"
				}
			case clean && request.Receipt != nil && request.Control == nil && request.Operation == "receipt":
				s.receiptReply(&reply, request.Receipt)
			default:
				reply.Code = "unsupported"
			}
		} else {
			switch {
			case endpoint.role == "management" && request.Operation == "attach" && request.Attach != nil && request.Page == nil && request.Launch == nil && request.Observation == nil:
				s.attach(conn, request)
				return
			case endpoint.role == "management" && request.Attach == nil && request.Operation == "status" && request.Observation == nil && request.Launch == nil && request.Page == nil:
				delivery := "unavailable"
				capabilities := []string{"status"}
				if s.daemon.receiver != nil || s.daemon.options.CausalReceiver != nil {
					delivery = "configured"
					capabilities = append(capabilities, "observe_metadata")
					if s.degraded.Load() {
						delivery = "degraded"
					}
				}
				if s.history != nil {
					capabilities = append(capabilities, "history_page", "history_hint")
				}
				state := "listening"
				if s.runtime != nil {
					disabled := s.runtime.Disabled()
					if !disabled {
						capabilities = append(capabilities, selectedObservationOperation)
						delivery = "configured"
						if s.degraded.Load() {
							delivery = "degraded"
						}
					}
					capabilities = append(capabilities, "list_runs", "attach", "stop", "receipt", "shutdown_plan")
					if !s.runtime.ShutdownInProgress() {
						capabilities = append(capabilities, "input", "resize")
					}
					if disabled {
						state = "draining"
					} else {
						capabilities = append(capabilities, "launch")
						if s.runtime.SupportsResume() {
							capabilities = append(capabilities, "resume")
						}
					}
				}
				reply.Code = "ok"
				reply.Status = &runtimeStatusRecord{RuntimeProtocolVersion, s.epoch, state, capabilities, delivery}
			case endpoint.role == "observations" && request.Attach == nil && request.Operation == "observe" && request.Observation != nil && request.Launch == nil && request.Page == nil:
				metadata := request.Observation.metadata()
				if !validObservation(metadata) {
					reply.Code = "invalid"
				} else if s.daemon.receiver == nil && s.daemon.options.CausalReceiver == nil {
					reply.Code = "unsupported"
				} else if s.daemon.options.CausalReceiver != nil && metadata.Epoch != s.epoch {
					reply.Code = "changed"
				} else if !s.enqueue(metadata, len(data)) {
					reply.Code = "busy"
				} else {
					reply.Code = "queued"
				}
			case endpoint.role == "management" && request.Attach == nil && request.Operation == "launch" && request.Launch != nil && request.Page == nil && request.Observation == nil:
				s.launchReply(&reply, request.Launch)
			case endpoint.role == "management" && request.Attach == nil && request.Operation == "list_runs" && request.Page != nil && request.Launch == nil && request.Observation == nil:
				s.pageReply(&reply, request.Page)
			default:
				reply.Code = "unsupported"
			}
		}
		if conn.SetWriteDeadline(time.Now().Add(s.daemon.options.IOTimeout)) != nil {
			return
		}
		if writeRuntimeFrame(conn, reply) != nil {
			return
		}
		s.endEffectReply(conn)
		if reply.Code != "ok" && reply.Code != "queued" {
			return
		}
	}
}
func (s *daemonServer) enqueue(metadata app.ObservationMetadata, size int) bool {
	var stamp app.ObservationStamp
	if s.daemon.options.CausalReceiver != nil {
		var err error
		stamp, err = s.gate.Stamp(s.ctx) // Intake, before queue lock; NEVER dequeue.
		if err != nil {
			s.degraded.Store(true)
			return false
		}
	}
	return s.enqueueItem(pendingObservation{metadata: metadata, bytes: size, stamp: stamp})
}
func (s *daemonServer) enqueueItem(item pendingObservation) bool {
	size := item.bytes
	s.queueMu.Lock()
	if s.ctx.Err() != nil || len(s.queue) >= s.daemon.options.ObservationFrames || size > s.daemon.options.ObservationBytes-s.queueBytes {
		s.queueMu.Unlock()
		s.degraded.Store(true)
		if s.gate != nil {
			s.gate.InvalidateGap() // No queue/gate lock nesting.
		}
		return false
	}
	s.queue = append(s.queue, item)
	s.queueBytes += size
	s.queueMu.Unlock()
	select {
	case s.notify <- struct{}{}:
	default:
	}
	return true
}
func (s *daemonServer) consume() {
	defer close(s.workerDone)
	defer func() { s.queueMu.Lock(); clear(s.queue); s.queue = nil; s.queueBytes = 0; s.queueMu.Unlock() }()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.notify:
		}
		for {
			s.queueMu.Lock()
			if len(s.queue) == 0 {
				s.queueMu.Unlock()
				break
			}
			item := s.queue[0]
			copy(s.queue, s.queue[1:])
			s.queue[len(s.queue)-1] = pendingObservation{}
			s.queue = s.queue[:len(s.queue)-1]
			s.queueBytes -= item.bytes
			s.queueMu.Unlock()
			if s.ctx.Err() != nil {
				return
			}
			if item.selected != nil {
				if s.runtime == nil {
					s.degraded.Store(true)
					continue
				}
				ctx, cancel := context.WithTimeout(s.ctx, ManagementRuntimeBudget)
				err := s.runtime.ConsumeObservation(ctx, *item.selected)
				cancel()
				if err != nil {
					s.degraded.Store(true)
				}
			} else if s.daemon.options.CausalReceiver != nil {
				if item.stamp.Context().Err() != nil {
					s.degraded.Store(true)
					continue // Old queued evidence cannot acquire a new intake stamp.
				}
				if s.daemon.options.CausalReceiver.ConsumeCausalObservation(item.stamp.Context(), item.metadata, item.stamp) != nil {
					s.degraded.Store(true)
					s.gate.InvalidateGap()
				}
			} else if s.daemon.receiver != nil {
				if s.daemon.receiver.ConsumeObservation(s.ctx, item.metadata) != nil {
					s.degraded.Store(true)
				}
			}
		}
	}
}

func (s *daemonServer) watchContinuity() {
	defer close(s.powerDone)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if _, err := s.gate.Stamp(s.ctx); err != nil && s.ctx.Err() == nil && s.daemon.options.CausalReceiver != nil {
			s.degraded.Store(true)
		}
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Metadata publication has its OWN loop: even a slow journal cannot stall the
// native power watcher. This publishes a private notice, not OS/human delivery.
func (s *daemonServer) watchUpdateNotices() {
	defer close(s.updateDone)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := s.runtime.PublishDueUpdateNotice(s.ctx); errors.Is(err, domain.ErrUnsupported) {
			return // Legacy/custom factories are not silently lifecycle-enrolled.
		}
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

type runtimeCallResult struct {
	reply runtimeResponse
	err   error
}

// Status queries readiness only. It never creates a directory or starts a daemon.
func (d *LocalDaemon) Status(ctx context.Context, root string) (app.DaemonStatus, error) {
	reply, _, err := d.exchange(ctx, root, runtimeRequest{Operation: "status"}, OptionalRuntimeBudget)
	if err != nil {
		return app.DaemonStatus{}, err
	}
	if reply.Lifecycle != nil || reply.Resume != nil || reply.History != nil || reply.HistoryHint != nil || reply.Control != nil || reply.Output != nil || reply.Code != "ok" || reply.Status == nil || reply.Role != "" || reply.Run != nil || reply.Page != nil {
		return app.DaemonStatus{}, app.ErrRuntimeProtocol
	}
	return decodeRuntimeStatus(reply)
}

// Observe acknowledges transport queue admission only, never native-state acceptance.
func (d *LocalDaemon) Observe(ctx context.Context, root string, metadata app.ObservationMetadata) error {
	bounded, cancel := context.WithTimeout(ctx, OptionalRuntimeBudget)
	defer cancel()
	if !validObservation(metadata) {
		return app.ErrRuntimeProtocol
	}
	reply, _, err := d.exchange(bounded, root, runtimeRequest{Operation: "observe", Observation: recordObservation(metadata)}, OptionalRuntimeBudget)
	if err != nil {
		return err
	}
	if reply.Code != "queued" || !reply.acknowledgmentOnly() {
		return app.ErrRuntimeProtocol
	}
	return nil
}
func (d *LocalDaemon) exchange(parent context.Context, root string, request runtimeRequest, budget time.Duration) (runtimeResponse, bool, error) {
	ctx, cancel := context.WithTimeout(parent, budget)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return runtimeResponse{}, false, xerrors.Errorf("local runtime call: %w", err)
	}
	select {
	case runtimeClientSlots <- struct{}{}:
	default:
		return runtimeResponse{}, false, app.ErrRuntimeBusy
	}
	result := make(chan runtimeCallResult, 1)
	var phase atomic.Int32
	go func() {
		defer func() { <-runtimeClientSlots }()
		reply, err := d.performExchange(ctx, root, request, &phase)
		result <- runtimeCallResult{reply, err}
	}()
	select {
	case <-ctx.Done():
		phase.CompareAndSwap(0, 2)
		return runtimeResponse{}, phase.Load() == 1, xerrors.Errorf("local runtime call: %w", ctx.Err())
	case value := <-result:
		return value.reply, phase.Load() == 1, value.err
	}
}
func (d *LocalDaemon) openRuntime(ctx context.Context, root, operation string) (*net.UnixConn, string, error) {
	if !validRuntimeRoot(root) {
		return nil, "", app.ErrUnsafeRuntime
	}
	if _, err := os.Lstat(root); errors.Is(err, os.ErrNotExist) {
		return nil, "", app.ErrDaemonUnavailable
	}
	rootFD, err := privateRuntimeDir(root, false)
	if err != nil {
		return nil, "", app.ErrUnsafeRuntime
	}
	defer func() { _ = unix.Close(rootFD) }()
	witness, err := directoryWitness(rootFD)
	if err != nil {
		return nil, "", app.ErrUnsafeRuntime
	}
	name, role := "daemon.sock", "management"
	if operation == "observe" || operation == "history_hint" || operation == selectedObservationOperation {
		name, role = "observations.sock", "observations"
	}
	var before unix.Stat_t
	err = unix.Fstatat(rootFD, name, &before, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return nil, "", app.ErrDaemonUnavailable
	}
	if err != nil || !runtimeSocketStat(before) {
		return nil, "", app.ErrUnsafeRuntime
	}
	if err = ctx.Err(); err != nil {
		return nil, "", xerrors.Errorf("inspect local endpoint: %w", err)
	}
	connection, err := (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(root, name))
	if err != nil {
		return nil, "", app.ErrDaemonUnavailable
	}
	conn, ok := connection.(*net.UnixConn)
	if !ok {
		_ = connection.Close()
		return nil, "", app.ErrUnsafeRuntime
	}
	success := false
	defer func() {
		if !success {
			_ = conn.Close()
		}
	}()
	deadline, ok := ctx.Deadline()
	if !ok || conn.SetDeadline(deadline) != nil {
		return nil, "", app.ErrDaemonUnavailable
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	var after unix.Stat_t
	if verifyRuntimePeer(conn) != nil || verifyRuntimeRoot(root, witness, rootFD) != nil || unix.Fstatat(rootFD, name, &after, unix.AT_SYMLINK_NOFOLLOW) != nil || !sameSocket(before, after) {
		return nil, "", app.ErrUnsafeRuntime
	}
	random := make([]byte, 16)
	if _, err = rand.Read(random); err != nil {
		return nil, "", app.ErrDaemonUnavailable
	}
	id := hex.EncodeToString(random)
	if err = writeRuntimeFrame(conn, runtimeRequest{Version: RuntimeProtocolVersion, ID: id, Operation: "hello", Role: role}); err != nil {
		return nil, "", app.ErrDaemonUnavailable
	}
	hello, err := readRuntimeResponse(conn, id)
	if err != nil {
		return nil, "", err
	}
	if hello.Lifecycle != nil || hello.Resume != nil || hello.History != nil || hello.HistoryHint != nil || hello.Control != nil || hello.Output != nil || hello.Code != "ok" || hello.Role != role || hello.Status != nil || hello.Run != nil || hello.Page != nil {
		return nil, "", app.ErrRuntimeProtocol
	}
	success = true
	return conn, id, nil
}
func (d *LocalDaemon) performExchange(ctx context.Context, root string, request runtimeRequest, phase *atomic.Int32) (runtimeResponse, error) {
	conn, id, err := d.openRuntime(ctx, root, request.Operation)
	if err != nil {
		return runtimeResponse{}, err
	}
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	id += "-request"
	request.Version = RuntimeProtocolVersion
	request.ID = id
	if err = ctx.Err(); err != nil {
		return runtimeResponse{}, xerrors.Errorf("check management send budget: %w", err)
	}
	if !phase.CompareAndSwap(0, 1) {
		return runtimeResponse{}, context.Canceled
	}
	if err = writeRuntimeFrame(conn, request); err != nil {
		return runtimeResponse{}, app.ErrDaemonUnavailable
	}
	return readRuntimeResponse(conn, id)
}
func decodeRuntimeStatus(reply runtimeResponse) (app.DaemonStatus, error) {
	v := reply.Status
	if v.Version != RuntimeProtocolVersion || len(v.Epoch) != 32 || (v.State != "listening" && v.State != "draining") || len(v.Capabilities) < 1 || len(v.Capabilities) > 16 {
		return app.DaemonStatus{}, app.ErrRuntimeProtocol
	}
	if _, err := hex.DecodeString(v.Epoch); err != nil {
		return app.DaemonStatus{}, app.ErrRuntimeProtocol
	}
	for _, capability := range v.Capabilities {
		if !boundedRuntimeID(capability, false) {
			return app.DaemonStatus{}, app.ErrRuntimeProtocol
		}
	}
	switch v.ObservationDelivery {
	case "unavailable", "configured", "degraded":
	default:
		return app.DaemonStatus{}, app.ErrRuntimeProtocol
	}
	return app.DaemonStatus{ProtocolVersion: v.Version, Epoch: v.Epoch, State: v.State, Capabilities: append([]string(nil), v.Capabilities...), ObservationDelivery: v.ObservationDelivery}, nil
}
func readRuntimeResponse(conn *net.UnixConn, id string) (runtimeResponse, error) {
	data, err := readRuntimeFrame(conn)
	if err != nil {
		return runtimeResponse{}, app.ErrDaemonUnavailable
	}
	var reply runtimeResponse
	if err = decodeRuntimeJSON(data, &reply); err != nil {
		return runtimeResponse{}, err
	}
	if reply.Code == "busy" && reply.ID == "" {
		return runtimeResponse{}, app.ErrRuntimeBusy
	}
	if reply.ID != id {
		return runtimeResponse{}, app.ErrRuntimeProtocol
	}
	if reply.Code != "ok" && reply.Code != "queued" {
		return reply, responseError(reply.Code)
	}
	return reply, nil
}
