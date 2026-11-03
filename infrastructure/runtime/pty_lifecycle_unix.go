//go:build darwin || linux

package runtime

import (
	"context"
	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"golang.org/x/sys/unix"
	"golang.org/x/xerrors"
)

// ValidateTerminalClose checks the actual master capability and unchanged target;
// unlike Stop, descriptor close never reconstructs or signals a numeric PID.
func (c *ptyChild) ValidateTerminalClose(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return xerrors.Errorf("validate owned terminal closure: %w", err)
	}
	c.mu.Lock()
	capable := c.closeDrain != nil && c.drained != nil && c.terminalClosed != nil
	closed := c.fd < 0
	c.mu.Unlock()
	if !capable {
		return domain.ErrUnsupported
	}
	if closed {
		return nil
	}
	if err := c.validateTargetExecutable(ctx); err != nil {
		return err
	}
	return nil
}

// CloseTerminal permanently gates terminal operations, joins the single reader,
// and closes only its still-owned FD. Kernel SIGHUP/work loss is possible; this
// sends no explicit signal and cannot assert exit or descendant exclusivity.
func (c *ptyChild) CloseTerminal(ctx context.Context) error {
	if err := c.ValidateTerminalClose(ctx); err != nil {
		return xerrors.Errorf("close owned terminal: %w", err)
	}
	c.mu.Lock()
	if c.fd < 0 {
		err := c.terminalCloseErr
		c.mu.Unlock()
		return err
	}
	if err := ctx.Err(); err != nil {
		c.mu.Unlock()
		return xerrors.Errorf("admit owned terminal closure: %w", err)
	}
	c.terminalCloseOnce.Do(func() {
		c.terminalClosing = true
		close(c.closeDrain)
		// Once admitted, caller loss cannot strand the finalizer or abandon the FD.
		// It never races reader FD reuse: drained closes before this lock/close.
		go func() {
			<-c.drained
			c.mu.Lock()
			_ = c.closeMasterLocked()
			c.mu.Unlock()
			close(c.terminalClosed)
		}()
	})
	c.mu.Unlock()
	select {
	case <-c.terminalClosed:
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.terminalCloseErr
	case <-ctx.Done():
		return app.ErrShutdownUnknown
	}
}

// closeMasterLocked is the only post-spawn FD close site, shared with the sole
// reaper after the reader join. Never retry close(EINTR) on a possibly reused FD.
func (c *ptyChild) closeMasterLocked() error {
	c.terminalClosing = true
	if c.fd >= 0 {
		fd := c.fd
		c.fd = -1
		if unix.Close(fd) != nil {
			c.terminalCloseErr = app.ErrShutdownUnknown
		}
	}
	return c.terminalCloseErr
}
