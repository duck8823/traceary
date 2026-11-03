//go:build darwin || linux

package runtime

import (
	"context"
	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"golang.org/x/xerrors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

func (s *daemonServer) attach(conn *net.UnixConn, request runtimeRequest) {
	reply := runtimeResponse{Version: RuntimeProtocolVersion, ID: request.ID}
	if s.runtime == nil {
		reply.Code = "unsupported"
		_ = writeRuntimeFrame(conn, reply)
		return
	}
	selection, err := request.Attach.Selection.selection()
	cursor, offsetErr := domain.ParseByteOffset(request.Attach.Cursor)
	if err != nil || offsetErr != nil {
		reply.Code = "invalid"
		_ = writeRuntimeFrame(conn, reply)
		return
	}
	sub, err := s.runtime.Attach(selection, cursor)
	if err != nil {
		reply.Code = operationCode(err)
		_ = writeRuntimeFrame(conn, reply)
		return
	}
	defer sub.Close()
	if conn.SetReadDeadline(time.Time{}) != nil {
		return
	}
	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	// One-way after admission. EOF or any further client byte detaches only this reader.
	stopped := make(chan struct{})
	go func() { defer close(stopped); var b [1]byte; _, _ = conn.Read(b[:]); cancel() }()
	defer func() { _ = conn.Close(); <-stopped }()
	for {
		frame, err := sub.Next(ctx)
		if err != nil {
			return
		}
		reply.Code = "ok"
		reply.Output = recordOutput(frame)
		if conn.SetWriteDeadline(time.Now().Add(s.daemon.options.IOTimeout)) != nil {
			return
		}
		if writeRuntimeFrame(conn, reply) != nil {
			return
		}
		if frame.Kind == "end" || frame.Gap != nil && frame.Gap.Reason == "slow_consumer" {
			return
		}
	}
}

type outputStream struct {
	conn       *net.UnixConn
	id         string
	selection  app.OutputSelection
	cursor     domain.ByteOffset
	base, head domain.ByteOffset
	resize     app.ResizeEvidence
	started    bool
	cached     *app.OutputFrame
	reading    atomic.Bool
	once       sync.Once
	closed     atomic.Bool
	done       chan struct{}
}

func (s *outputStream) Close() error {
	s.once.Do(func() {
		s.closed.Store(true)
		close(s.done)
		_ = s.conn.Close()
		<-runtimeClientSlots
	})
	return nil
}
func (s *outputStream) Next(ctx context.Context) (app.OutputFrame, error) {
	if !s.reading.CompareAndSwap(false, true) {
		return app.OutputFrame{}, app.ErrRuntimeBusy
	}
	defer s.reading.Store(false)
	if err := ctx.Err(); err != nil {
		_ = s.Close()
		return app.OutputFrame{}, xerrors.Errorf("read runtime stream: %w", err)
	}
	if s.closed.Load() {
		return app.OutputFrame{}, io.EOF
	}
	if s.cached != nil {
		f := *s.cached
		s.cached = nil
		return f, nil
	}
	stop := context.AfterFunc(ctx, func() { _ = s.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	if s.conn.SetReadDeadline(deadline) != nil {
		_ = s.Close()
		return app.OutputFrame{}, app.ErrDaemonUnavailable
	}
	reply, err := readRuntimeResponse(s.conn, s.id)
	if err != nil {
		_ = s.Close()
		if ctx.Err() != nil {
			return app.OutputFrame{}, xerrors.Errorf("read runtime stream: %w", ctx.Err())
		}
		return app.OutputFrame{}, xerrors.Errorf("read runtime stream: %w", err)
	}
	if reply.Code != "ok" || reply.Output == nil || reply.Run != nil || reply.Page != nil || reply.Status != nil || reply.Role != "" {
		_ = s.Close()
		return app.OutputFrame{}, app.ErrRuntimeProtocol
	}
	f, err := reply.Output.frame()
	if err != nil || f.Info.Selection != s.selection || f.Info.Base < s.base || f.Info.Head < s.head || f.Info.Resize.Sequence < s.resize.Sequence || s.started && f.Info.Resize.Sequence == s.resize.Sequence && f.Info.Resize != s.resize || (!s.started && (f.Kind != "snapshot" || s.cursor > f.Info.Head)) || (s.started && f.Kind == "snapshot") {
		_ = s.Close()
		return app.OutputFrame{}, app.ErrRuntimeProtocol
	}
	switch f.Kind {
	case "data":
		if f.Start != s.cursor {
			_ = s.Close()
			return app.OutputFrame{}, app.ErrRuntimeProtocol
		}
		s.cursor = f.End
	case "gap":
		if f.Gap.From != s.cursor {
			_ = s.Close()
			return app.OutputFrame{}, app.ErrRuntimeProtocol
		}
		s.cursor = f.Gap.To
	case "end":
		if s.cursor != f.Info.Head {
			_ = s.Close()
			return app.OutputFrame{}, app.ErrRuntimeProtocol
		}
	}
	s.started = true
	s.base = f.Info.Base
	s.head = f.Info.Head
	s.resize = f.Info.Resize
	if f.Kind == "end" || f.Gap != nil && f.Gap.Reason == "slow_consumer" {
		_ = s.Close()
	}
	return f, nil
}

// Attach opens exactly one bounded management stream. It never launches, retries, or resumes a host.
func (d *LocalDaemon) Attach(parent context.Context, root string, selection app.OutputSelection, cursor domain.ByteOffset) (app.OutputStream, error) {
	if _, err := recordSelection(selection).selection(); err != nil {
		return nil, xerrors.Errorf("attach runtime stream: %w", err)
	}
	if err := parent.Err(); err != nil {
		return nil, xerrors.Errorf("attach runtime stream: %w", err)
	}
	select {
	case runtimeClientSlots <- struct{}{}:
	default:
		return nil, app.ErrRuntimeBusy
	}
	ctx, cancel := context.WithTimeout(parent, ManagementRuntimeBudget)
	defer cancel()
	type result struct {
		stream *outputStream
		err    error
	}
	results := make(chan result)
	go func() {
		conn, id, err := d.openRuntime(ctx, root, "attach")
		if err != nil {
			<-runtimeClientSlots
			select {
			case results <- result{err: err}:
			case <-ctx.Done():
			}
			return
		}
		stream := &outputStream{conn: conn, id: id + "-request", selection: selection, cursor: cursor, done: make(chan struct{})}
		stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
		err = writeRuntimeFrame(conn, runtimeRequest{Version: RuntimeProtocolVersion, ID: stream.id, Operation: "attach", Attach: &outputAttachRecord{recordSelection(selection), cursor.String()}})
		if err == nil {
			var first app.OutputFrame
			first, err = stream.Next(ctx)
			if err == nil {
				stream.cached = &first
			}
		}
		// Stop the admission cancellation callback before transferring connection lifetime.
		if !stop() && err == nil {
			err = app.ErrDaemonUnavailable
		}
		if err == nil {
			err = conn.SetDeadline(time.Time{})
		}
		if err == nil {
			err = parent.Err()
		}
		if err != nil {
			_ = stream.Close()
			select {
			case results <- result{err: err}:
			case <-ctx.Done():
			}
			return
		}
		select {
		case results <- result{stream: stream}:
		case <-ctx.Done():
			_ = stream.Close()
		}
	}()
	select {
	case <-ctx.Done():
		return nil, xerrors.Errorf("attach runtime stream: %w", ctx.Err())
	case r := <-results:
		if r.err != nil {
			return nil, r.err
		}
		go func() {
			select {
			case <-parent.Done():
				_ = r.stream.Close()
			case <-r.stream.done:
			}
		}()
		return r.stream, nil
	}
}
