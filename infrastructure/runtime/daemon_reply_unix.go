//go:build darwin || linux

package runtime

import (
	"context"
	"net"

	"golang.org/x/xerrors"
)

// Register BEFORE invoking a final effect coordinator. Per-connection slots cap
// this map; no WaitGroup.Add/Wait zero-counter race or unbounded request history.
func (s *daemonServer) beginEffectReply(conn *net.UnixConn) bool {
	s.replyMu.Lock()
	defer s.replyMu.Unlock()
	if conn == nil || s.effectAdmissionClosed || len(s.effectReplies) >= MaxRuntimeConnections {
		return false
	}
	if _, exists := s.effectReplies[conn]; exists {
		return false
	}
	if s.effectReplies == nil {
		s.effectReplies = make(map[*net.UnixConn]chan struct{})
	}
	s.effectReplies[conn] = make(chan struct{})
	return true
}

// Complete after frame write OR connection exit. Completion means the write
// attempt ended, not that the client received/accepted it; lost ACK stays unknown.
func (s *daemonServer) endEffectReply(conn *net.UnixConn) {
	s.replyMu.Lock()
	defer s.replyMu.Unlock()
	if done, exists := s.effectReplies[conn]; exists {
		delete(s.effectReplies, conn)
		close(done)
	}
}

func (s *daemonServer) awaitEffectReplies(ctx context.Context) error {
	s.replyMu.Lock()
	s.effectAdmissionClosed = true
	pending := make([]<-chan struct{}, 0, len(s.effectReplies))
	for _, done := range s.effectReplies {
		pending = append(pending, done)
	}
	s.replyMu.Unlock()
	// Do not wait on network: accept and long-lived attach require connection
	// closure to finish. Only these already-registered effect writes are joined.
	for _, done := range pending {
		select {
		case <-done:
		case <-ctx.Done():
			return xerrors.Errorf("await final daemon responses: %w", ctx.Err())
		}
	}
	return nil
}
