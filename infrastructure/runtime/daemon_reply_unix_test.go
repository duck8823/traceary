//go:build darwin || linux

package runtime

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestEffectReplyDrainJoinsOnlyRegisteredRepliesAndClosesAdmission(t *testing.T) {
	server := &daemonServer{}
	first, late := &net.UnixConn{}, &net.UnixConn{}
	if !server.beginEffectReply(first) {
		t.Fatal("effect response tracking rejected")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- server.awaitEffectReplies(ctx) }()
	deadline := time.Now().Add(time.Second)
	for {
		server.replyMu.Lock()
		closed := server.effectAdmissionClosed
		server.replyMu.Unlock()
		if closed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("reply drain did not close admission")
		}
		time.Sleep(time.Millisecond)
	}
	if server.beginEffectReply(late) {
		t.Fatal("new effect raced final response drain")
	}
	select {
	case <-done:
		t.Fatal("drain abandoned in-flight response")
	default:
	}
	server.endEffectReply(first)
	server.endEffectReply(first)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestEffectReplyDrainIsBoundedAndDoesNotWaitForAcceptOrAttach(t *testing.T) {
	server := &daemonServer{}
	conn := &net.UnixConn{}
	if !server.beginEffectReply(conn) {
		t.Fatal("tracking failed")
	}
	// A blocked unrelated accept/attach is deliberately never joined here.
	server.network.Add(1)
	defer server.network.Done()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := server.awaitEffectReplies(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("response drain ignored deadline", err)
	}
	server.endEffectReply(conn)
}
