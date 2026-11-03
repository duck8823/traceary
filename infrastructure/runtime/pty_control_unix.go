//go:build darwin || linux

package runtime

import (
	"context"
	"errors"
	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"golang.org/x/sys/unix"
	"golang.org/x/xerrors"
)

func (c *ptyChild) controlOpenLocked() error {
	if c.controlsClosed || c.exitObserved.Load() {
		if c.controlErr != nil {
			return c.controlErr
		}
		return domain.ErrChanged
	}
	return nil
}
func (c *ptyChild) validateShapeLocked(intent domain.ControlIntent) error {
	if err := c.controlOpenLocked(); err != nil {
		return err
	}
	if _, err := domain.ControlIntentFrom(intent.Snapshot()); err != nil {
		return xerrors.Errorf("validate owned terminal intent: %w", err)
	}
	state := intent.Snapshot()
	if state.Kind != domain.ControlStop && (c.terminalClosing || c.fd < 0) {
		return domain.ErrChanged
	}
	if state.Kind == domain.ControlResize {
		if err := c.output.ValidateResize(c.selection, state.ResizeSequence, state.Rows, state.Cols); err != nil {
			return xerrors.Errorf("validate owned resize evidence: %w", err)
		}
	}
	return nil
}

// ValidateControl checks fresh target/executable/sequence evidence before any journal reservation.
func (c *ptyChild) ValidateControl(ctx context.Context, intent domain.ControlIntent) error {
	c.mu.Lock()
	err := c.validateShapeLocked(intent)
	c.mu.Unlock()
	if err != nil {
		return err
	}
	if err = checkReapPolicy(ctx); err != nil {
		return err
	}
	if err = c.validateTargetExecutable(ctx); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.validateShapeLocked(intent)
}

// ApplyControl holds the actual FD/group admission barrier for one native effect.
// No write is retried, no PID is reopened, and the sole reaper first closes this barrier.
func (c *ptyChild) ApplyControl(ctx context.Context, intent domain.ControlIntent, data []byte) (app.ControlEffect, error) {
	unknown := app.ControlEffect{Outcome: domain.ControlUnknown}
	if err := c.ValidateControl(ctx, intent); err != nil {
		return unknown, err
	}
	state := intent.Snapshot()
	if len(data) != state.InputBytes {
		return unknown, domain.ErrInvalid
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.validateShapeLocked(intent); err != nil {
		return unknown, err
	}
	if err := ctx.Err(); err != nil {
		return unknown, xerrors.Errorf("check owned terminal effect budget: %w", err)
	}
	if err := checkReapPolicy(ctx); err != nil {
		c.controlsClosed = true
		c.controlErr = err
		return unknown, err
	}
	switch state.Kind {
	case domain.ControlInput:
		return writeControlOnce(func(p []byte) (int, error) { return unix.Write(c.fd, p) }, data)
	case domain.ControlResize:
		if err := unix.IoctlSetWinsize(c.fd, unix.TIOCSWINSZ, &unix.Winsize{Row: state.Rows, Col: state.Cols}); err != nil {
			return unknown, app.ErrControlUnknown
		}
		if err := c.output.AcknowledgeResize(c.selection, state.ResizeSequence, state.Rows, state.Cols); err != nil {
			return unknown, app.ErrControlUnknown
		}
		return app.ControlEffect{Outcome: domain.ControlDelivered}, nil
	case domain.ControlStop:
		if err := unix.Kill(-c.pid, unix.SIGTERM); err != nil {
			if errors.Is(err, unix.ESRCH) {
				c.controlsClosed = true
				return unknown, domain.ErrChanged
			}
			return unknown, app.ErrControlUnknown
		}
		return app.ControlEffect{Outcome: domain.ControlDelivered}, nil
	default:
		return unknown, domain.ErrInvalid
	}
}

// writeControlOnce is the native fault boundary: a short/interrupted write is
// unknown, never a reason to replay a prefix or retry the remaining bytes.
func writeControlOnce(write func([]byte) (int, error), data []byte) (app.ControlEffect, error) {
	n, err := write(data)
	if n < 0 || n > len(data) {
		return app.ControlEffect{Outcome: domain.ControlUnknown}, app.ErrControlUnknown
	}
	effect := app.ControlEffect{Outcome: domain.ControlUnknown, WrittenBytes: n}
	if err != nil || n != len(data) {
		return effect, app.ErrControlUnknown
	}
	effect.Outcome = domain.ControlDelivered
	return effect, nil
}

// validateTargetExecutable reads current registered witnesses without executing
// a version probe or accepting client-supplied paths/environment.
func (c *ptyChild) validateTargetExecutable(ctx context.Context) error {
	if err := c.launcher.registry.Revalidate(ctx, c.target, string(c.profile.ID())); err != nil {
		return xerrors.Errorf("revalidate owned control target: %w", err)
	}
	witness, err := executableWitness(ctx, c.profile.Snapshot().Executable)
	if err != nil {
		return xerrors.Errorf("revalidate owned executable: %w", err)
	}
	if witness != c.profile.Snapshot().ExecutableWitness {
		return domain.ErrChanged
	}
	return nil
}
