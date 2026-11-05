//go:build darwin || linux

package runtime

import (
	"bytes"
	"context"
	app "github.com/duck8823/traceary/application/runtime"
	"golang.org/x/sys/unix"
	"testing"
	"time"
)

// A pipe provides deterministic EOF/open-writer evidence independently of native
// controlling-terminal hangup semantics (Darwin may report EIO on leader exit).
func TestOutputPumpPostWaitTailAndOpenWriterBound(t *testing.T) {
	for _, open := range []bool{false, true} {
		t.Run(map[bool]string{false: "buffered_eof", true: "open_descendant_simulation"}[open], func(t *testing.T) {
			fds := make([]int, 2)
			if err := unix.Pipe(fds); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = unix.Close(fds[0]); _ = unix.Close(fds[1]) }()
			if err := unix.SetNonblock(fds[0], true); err != nil {
				t.Fatal(err)
			}
			payload := []byte("tail after owned wait")
			if n, err := unix.Write(fds[1], payload); err != nil || n != len(payload) {
				t.Fatal("write fixture tail")
			}
			if !open {
				_ = unix.Close(fds[1])
				fds[1] = -1
			}
			selection := outputWireSelection()
			output, err := app.NewOutput(selection, app.OutputLimits{})
			if err != nil {
				t.Fatal(err)
			}
			stopped := make(chan struct{})
			close(stopped)
			before := time.Now()
			drainPTY(fds[0], stopped, nil, output)
			if time.Since(before) > time.Second {
				t.Fatal("post-Wait drain exceeded bound")
			}
			sub, err := output.Subscribe(selection, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer sub.Close()
			_, _ = sub.Next(context.Background())
			frame, err := sub.Next(context.Background())
			if err != nil || !bytes.Equal(frame.Data, payload) {
				t.Fatal("buffered kernel tail discarded")
			}
			frame, err = sub.Next(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if open {
				if frame.Gap == nil || frame.Gap.Reason != "tail_unknown" || !frame.Gap.DeliveryUnknown || frame.Gap.From != frame.Gap.To {
					t.Fatal("open writer cutoff hidden")
				}
				frame, err = sub.Next(context.Background())
			}
			if err != nil || frame.Kind != "end" {
				t.Fatal("capture never completed")
			}
		})
	}
}
