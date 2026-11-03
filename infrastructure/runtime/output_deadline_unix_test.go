//go:build darwin || linux

package runtime

import (
	"context"
	"errors"
	app "github.com/duck8823/traceary/application/runtime"
	"golang.org/x/sys/unix"
	"net"
	"os"
	"testing"
	"time"
)

// Context timer delivery can lag the socket's absolute deadline. Err may still
// be nil when the caller's budget has already expired; do not call that peer loss.
type laggingOutputDeadline struct {
	context.Context
	at time.Time
}

func (c laggingOutputDeadline) Deadline() (time.Time, bool) { return c.at, true }
func TestOutputStreamElapsedDeadlineIsNotPeerUnavailable(t *testing.T) {
	for _, mode := range []string{"already_expired", "expires_during_read", "peer_loss"} {
		t.Run(mode, func(t *testing.T) {
			pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
			if err != nil {
				t.Fatal(err)
			}
			file := os.NewFile(uintptr(pair[0]), "synthetic stream client")
			peer := os.NewFile(uintptr(pair[1]), "synthetic stream peer")
			defer func() { _ = peer.Close() }()
			connection, err := net.FileConn(file)
			_ = file.Close()
			if err != nil {
				t.Fatal(err)
			}
			conn, ok := connection.(*net.UnixConn)
			if !ok {
				_ = connection.Close()
				t.Fatal("not a Unix stream")
			}
			runtimeClientSlots <- struct{}{}
			stream := &outputStream{conn: conn, id: "test", selection: outputWireSelection(), started: true, done: make(chan struct{})}
			defer func() { _ = stream.Close() }()
			at := time.Now().Add(-time.Millisecond)
			if mode == "expires_during_read" {
				at = time.Now().Add(10 * time.Millisecond)
			}
			expected := error(context.DeadlineExceeded)
			if mode == "peer_loss" {
				at = time.Now().Add(time.Hour)
				_ = peer.Close()
				expected = app.ErrDaemonUnavailable
			}
			ctx := laggingOutputDeadline{Context: context.Background(), at: at}
			if _, err = stream.Next(ctx); !errors.Is(err, expected) {
				t.Fatalf("elapsed caller deadline misreported as peer loss: %v", err)
			}

		})
	}
}
