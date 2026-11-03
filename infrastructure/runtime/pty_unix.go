//go:build darwin || linux

package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
	"golang.org/x/xerrors"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
)

const maxExecutableBytes int64 = 512 * 1024 * 1024

// PTYLauncher keeps trusted inherited environment private. Launch callers cannot override it.
type PTYLauncher struct {
	registry    app.TargetRegistry
	environment []string
}

// NewPTYLauncher snapshots trusted composition-time environment without persisting it.
func NewPTYLauncher(registry app.TargetRegistry, environment []string) (*PTYLauncher, error) {
	if registry == nil {
		return nil, domain.ErrInvalid
	}
	if environment == nil {
		environment = os.Environ()
	}
	return &PTYLauncher{registry: registry, environment: append([]string{}, environment...)}, nil
}

// InspectProfile is an explicit trusted-operator preflight. It fixes canonical executable
// and exact version; it does not certify a native interaction or recovery adapter.
func (l *PTYLauncher) InspectProfile(ctx context.Context, id, host, path, version string) (domain.HostProfile, error) {
	if !filepath.IsAbs(path) {
		return domain.HostProfile{}, domain.ErrInvalid
	}
	// Static invalid profiles must not even inspect a supplied filesystem path.
	candidate, err := domain.HostProfileFrom(domain.HostProfileState{ID: id, Host: host, Executable: filepath.Clean(path), ExecutableWitness: "pending", Version: version})
	if err != nil {
		return domain.HostProfile{}, xerrors.Errorf("validate registered host profile: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return domain.HostProfile{}, domain.ErrUnsafe
	}
	witness, err := executableWitness(ctx, canonical)
	if err != nil {
		return domain.HostProfile{}, xerrors.Errorf("inspect registered host profile: %w", err)
	}
	state := candidate.Snapshot()
	state.Executable = canonical
	state.ExecutableWitness = witness
	profile, err := domain.HostProfileFrom(state)
	if err != nil {
		return domain.HostProfile{}, xerrors.Errorf("inspect registered host profile: %w", err)
	}
	if err = l.verifyVersion(ctx, profile); err != nil {
		return domain.HostProfile{}, xerrors.Errorf("inspect registered host profile: %w", err)
	}
	return profile, nil
}
func (l *PTYLauncher) verifyVersion(ctx context.Context, profile domain.HostProfile) error {
	state := profile.Snapshot()
	before, err := executableWitness(ctx, state.Executable)
	if err != nil {
		return err
	}
	if before != state.ExecutableWitness {
		return domain.ErrChanged
	}
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	command := exec.CommandContext(probeCtx, state.Executable, "--version")
	command.Env = launchEnvironment(l.environment, "", domain.LaunchIdentity{})
	command.Dir = filepath.Dir(state.Executable)
	command.WaitDelay = 200 * time.Millisecond
	var output boundedVersionOutput
	command.Stdout = &output
	command.Stderr = &output
	if command.Run() != nil {
		return domain.ErrChanged
	}
	output.mu.Lock()
	version, overflow := strings.TrimSpace(string(output.data)), output.overflow
	output.mu.Unlock()
	if overflow || version != state.Version {
		return domain.ErrChanged
	}
	after, err := executableWitness(ctx, state.Executable)
	if err != nil {
		return err
	}
	if after != before {
		return domain.ErrChanged
	}
	return nil
}

type boundedVersionOutput struct {
	mu       sync.Mutex
	data     []byte
	overflow bool
}

func (w *boundedVersionOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	remaining := 4096 - len(w.data)
	if len(p) > remaining {
		w.overflow = true
		p = p[:remaining]
	}
	w.data = append(w.data, p...)
	return n, nil
}

// Start validates policy/target, directly starts the real executable and transfers live ownership.
func (l *PTYLauncher) Start(ctx context.Context, descriptor domain.LaunchDescriptor) (app.OwnedChild, error) {
	snapshot := descriptor.Snapshot()
	if snapshot.State != domain.RunReserved || !snapshot.Identity.Valid() {
		return nil, domain.ErrInvalid
	}
	if err := checkReapPolicy(ctx); err != nil {
		return nil, err
	}
	if err := l.verifyVersion(ctx, snapshot.Profile); err != nil {
		return nil, err
	}
	if err := l.registry.Revalidate(ctx, snapshot.Target, string(snapshot.Profile.ID())); err != nil {
		return nil, xerrors.Errorf("revalidate launch target: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, xerrors.Errorf("validate launch budget: %w", err)
	}
	if err := checkReapPolicy(ctx); err != nil {
		return nil, err
	}
	watch, err := prepareExitWatch(ctx)
	if err != nil {
		return nil, err
	}
	watchTransferred := false
	defer func() {
		if !watchTransferred {
			watch.close()
		}
	}()
	master, slave, err := pty.Open()
	if err != nil {
		return nil, domain.ErrUnsafe
	}
	// Re-wrap a nonblocking descriptor so Go's poller makes Close interrupt Read,
	// including on Darwin where creack/pty initially uses a blocking raw descriptor.
	fd, err := unix.FcntlInt(master.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err == nil {
		err = unix.SetNonblock(fd, true)
	}
	_ = master.Close()
	if err != nil {
		if fd >= 0 {
			_ = unix.Close(fd)
		}
		_ = slave.Close()
		return nil, domain.ErrUnsafe
	}
	// Keep this native nonblocking FD owned exclusively by the drain loop.
	// No os.File.Fd conversion may silently restore blocking mode.
	if err = pty.Setsize(slave, &pty.Winsize{Rows: 24, Cols: 80}); err != nil {
		_ = unix.Close(fd)
		_ = slave.Close()
		return nil, domain.ErrUnsafe
	}
	selection := app.OutputSelection{Identity: snapshot.Identity, Worktree: snapshot.Target.Worktree().ID(), Profile: snapshot.Profile.ID()}
	output, outputErr := app.NewOutput(selection, app.OutputLimits{})
	if outputErr == nil {
		outputErr = output.AcknowledgeResize(selection, 0, 24, 80)
	}
	if outputErr != nil {
		_ = unix.Close(fd)
		_ = slave.Close()
		return nil, xerrors.Errorf("initialize owned PTY output: %w", outputErr)
	}
	command := exec.Command(snapshot.Profile.Snapshot().Executable)
	command.Dir = snapshot.Target.Worktree().Path()
	command.Env = launchEnvironment(l.environment, snapshot.Target.Worktree().Store(), snapshot.Identity)
	command.Stdin = slave
	command.Stdout = slave
	command.Stderr = slave
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err = ctx.Err(); err != nil {
		_ = slave.Close()
		_ = unix.Close(fd)
		return nil, xerrors.Errorf("validate launch budget before start: %w", err)
	}
	if err = checkReapPolicy(ctx); err != nil {
		_ = slave.Close()
		_ = unix.Close(fd)
		return nil, err
	}
	err = command.Start()
	_ = slave.Close()
	if err != nil {
		_ = unix.Close(fd)
		return nil, domain.ErrUnsafe
	}
	watchErr := watch.bind(ctx, command.Process.Pid)
	child := &ptyChild{pid: command.Process.Pid, done: make(chan struct{}), output: output, fd: fd, launcher: l, target: snapshot.Target, profile: snapshot.Profile, selection: selection, controlsClosed: watchErr != nil, controlErr: watchErr, stopDrain: make(chan struct{}), closeDrain: make(chan struct{}), drained: make(chan struct{}), terminalClosed: make(chan struct{})}
	watchTransferred = true
	// The single native reader feeds only bounded volatile retention.
	go func() { drainPTY(fd, child.stopDrain, child.closeDrain, output); close(child.drained) }()
	go func() {
		watchErr := watch.wait()
		watch.close()
		child.exitObserved.Store(true)
		child.mu.Lock()
		child.controlsClosed = true
		if watchErr != nil && child.controlErr == nil {
			child.controlErr = watchErr
		}
		child.mu.Unlock()
		// Closed is permanent before the sole reap; never hold admission while
		// a fallback Wait or descendant-held PTY tail can block.
		waitErr := command.Wait()
		child.stopDrainOnce.Do(func() { close(child.stopDrain) })
		<-child.drained
		child.mu.Lock()
		_ = child.closeMasterLocked()
		child.mu.Unlock()
		if command.ProcessState != nil {
			child.code = command.ProcessState.ExitCode()
		}
		var exit *exec.ExitError
		if waitErr != nil && !errors.As(waitErr, &exit) {
			child.err = app.ErrLaunchUnknown
		}
		close(child.done)
	}()
	return child, nil
}

// drainPTY never shares a live descriptor with Close/reuse. Native reads are
// nonblocking and poll is bounded, so owned Wait cannot hang on a Go TTY poller.
func drainPTY(fd int, stopped, terminalClose <-chan struct{}, output *app.Output) {
	buffer := make([]byte, domain.MaxOutputChunk)
	var deadline time.Time
	remaining := domain.MaxOutputBytes
	finish := func(reason app.CaptureReason, unknown bool) { output.Finish(reason, unknown) }
	for {
		select {
		case <-terminalClose:
			finish(app.CaptureTerminalClosed, true)
			return
		default:
		}
		if deadline.IsZero() {
			select {
			case <-stopped:
				deadline = time.Now().Add(250 * time.Millisecond)
			default:
			}
		}
		if !deadline.IsZero() && remaining == 0 {
			// A nonblocking one-byte probe distinguishes exact-budget clean EOF
			// from an open/noisy descendant. Any discarded probe byte is unknown.
			var probe [1]byte
			n, err := unix.Read(fd, probe[:])
			if n == 0 && (err == nil || errors.Is(err, unix.EIO)) {
				finish(app.CaptureClosed, false)
			} else {
				finish(app.CaptureTailUnknown, true)
			}
			return
		}
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			finish(app.CaptureTailUnknown, true)
			return
		}
		chunk := buffer
		if !deadline.IsZero() {
			chunk = chunk[:min(len(chunk), remaining)]
		}
		n, err := unix.Read(fd, chunk)
		if n > 0 {
			if appendErr := output.Append(chunk[:n]); appendErr != nil {
				finish(app.CaptureReadError, true)
				return
			}
			if !deadline.IsZero() {
				remaining -= n
			}
			continue
		}
		if err == nil || errors.Is(err, unix.EIO) {
			finish(app.CaptureClosed, false)
			return
		}
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EWOULDBLOCK) {
			finish(app.CaptureReadError, true)
			return
		}
		polls := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		timeout := 25
		if !deadline.IsZero() {
			timeout = min(timeout, max(1, int(time.Until(deadline).Milliseconds())))
		}
		if _, err = unix.Poll(polls, timeout); err != nil && !errors.Is(err, unix.EINTR) {
			finish(app.CaptureReadError, true)
			return
		}
	}
}

type ptyChild struct {
	// These channels have distinct meanings: bounded post-exit tail versus
	// explicit live master loss. All FD closes follow the same reader join.
	stopDrain, closeDrain, drained, terminalClosed chan struct{}
	stopDrainOnce, terminalCloseOnce               sync.Once
	terminalClosing                                bool
	terminalCloseErr                               error
	mu                                             sync.Mutex
	fd                                             int
	controlsClosed                                 bool
	controlErr                                     error
	exitObserved                                   atomic.Bool
	launcher                                       *PTYLauncher
	target                                         domain.Target
	profile                                        domain.HostProfile
	selection                                      app.OutputSelection
	output                                         *app.Output
	pid, code                                      int
	err                                            error
	done                                           chan struct{}
}

func (c *ptyChild) SubscribeOutput(selection app.OutputSelection, cursor domain.ByteOffset) (*app.OutputSubscription, error) {
	sub, err := c.output.Subscribe(selection, cursor)
	if err != nil {
		return nil, xerrors.Errorf("subscribe owned PTY output: %w", err)
	}
	return sub, nil
}

func (c *ptyChild) PID() int           { return c.pid }
func (c *ptyChild) Wait() (int, error) { <-c.done; return c.code, c.err }
func launchEnvironment(source []string, store string, id domain.LaunchIdentity) []string {
	result := make([]string, 0, len(source)+5)
	for _, entry := range source {
		key, _, found := strings.Cut(entry, "=")
		if !found || strings.HasPrefix(key, "GIT_") || key == "TRACEARY_DB_PATH" || strings.HasPrefix(key, "TRACEARY_RUNTIME_") {
			continue
		}
		result = append(result, entry)
	}
	if store != "" {
		result = append(result, "TRACEARY_DB_PATH="+store, "TRACEARY_RUNTIME_RUN_ID="+id.Run, "TRACEARY_RUNTIME_GENERATION="+id.Generation, "TRACEARY_RUNTIME_EPOCH="+id.Epoch)
	}
	return result
}

// executableWitness opens no-follow through trusted ancestors, verifies before
// hashing and never executes the file. Pathname exec cannot isolate hostile same UID.
func executableWitness(ctx context.Context, path string) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", domain.ErrInvalid
	}
	dir, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", domain.ErrUnsafe
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for _, part := range parts[:len(parts)-1] {
		next, e := unix.Openat(dir, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		_ = unix.Close(dir)
		if e != nil {
			return "", domain.ErrUnsafe
		}
		dir = next
		var stat unix.Stat_t
		if unix.Fstat(dir, &stat) != nil || (stat.Uid != 0 && stat.Uid != uint32(os.Geteuid())) || (stat.Mode&0022 != 0 && (stat.Uid != 0 || stat.Mode&unix.S_ISVTX == 0)) {
			_ = unix.Close(dir)
			return "", domain.ErrUnsafe
		}
	}
	fd, err := unix.Openat(dir, parts[len(parts)-1], unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	_ = unix.Close(dir)
	if err != nil {
		return "", domain.ErrUnsafe
	}
	file := os.NewFile(uintptr(fd), "registered executable")
	defer func() { _ = file.Close() }()
	var before, after unix.Stat_t
	if unix.Fstat(fd, &before) != nil || before.Mode&unix.S_IFMT != unix.S_IFREG || (before.Uid != 0 && before.Uid != uint32(os.Geteuid())) || before.Mode&0022 != 0 || before.Mode&0111 == 0 {
		return "", domain.ErrUnsafe
	}
	if before.Size <= 0 || before.Size > maxExecutableBytes {
		return "", domain.ErrLimit
	}
	info, err := file.Stat()
	if err != nil {
		return "", domain.ErrUnsafe
	}
	digest := sha256.New()
	buffer := make([]byte, 64*1024)
	var total int64
	for {
		if err = ctx.Err(); err != nil {
			return "", xerrors.Errorf("inspect registered executable budget: %w", err)
		}
		n, readErr := file.Read(buffer)
		if n > 0 {
			total += int64(n)
			if total > maxExecutableBytes {
				return "", domain.ErrLimit
			}
			_, _ = digest.Write(buffer[:n])
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return "", domain.ErrUnsafe
		}
	}
	fresh, err := file.Stat()
	if err != nil || unix.Fstat(fd, &after) != nil || before.Dev != after.Dev || before.Ino != after.Ino || before.Mode != after.Mode || before.Uid != after.Uid || before.Size != after.Size || total != before.Size || !info.ModTime().Equal(fresh.ModTime()) {
		return "", domain.ErrChanged
	}
	witness := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%d:%d:%d:%d:%x", before.Uid, before.Dev, before.Ino, before.Mode, before.Size, info.ModTime().UnixNano(), digest.Sum(nil))))
	return hex.EncodeToString(witness[:]), nil
}
