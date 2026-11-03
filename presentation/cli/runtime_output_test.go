package cli_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"github.com/duck8823/traceary/presentation/cli"
	"io"
	"strings"
	"testing"
)

type outputClientFixture struct {
	runtimeClientFixture
	selection app.OutputSelection
	cursor    domain.ByteOffset
	stream    *outputStreamFixture
}
type outputStreamFixture struct {
	frames []app.OutputFrame
	closed bool
}

func (s *outputStreamFixture) Next(context.Context) (app.OutputFrame, error) {
	if len(s.frames) == 0 {
		return app.OutputFrame{}, io.EOF
	}
	f := s.frames[0]
	s.frames = s.frames[1:]
	return f, nil
}
func (s *outputStreamFixture) Close() error { s.closed = true; return nil }
func (c *outputClientFixture) Attach(_ context.Context, root string, selection app.OutputSelection, cursor domain.ByteOffset) (app.OutputStream, error) {
	c.calls++
	c.root = root
	c.selection = selection
	c.cursor = cursor
	return c.stream, nil
}
func TestRuntimeAttachCLIEncodesUntrustedBytesAndExactDecimalCursor(t *testing.T) {
	raw := []byte("日本語🙂\x1b]52;c;ZmFrZQ==\a")
	selection := app.OutputSelection{Identity: domain.LaunchIdentity{Epoch: strings.Repeat("1", 32), Run: strings.Repeat("2", 32), Generation: strings.Repeat("3", 32)}, Worktree: domain.WorktreeID("w_" + strings.Repeat("a", 64)), Profile: "codex"}
	cursor := domain.ByteOffset(9007199254740993)
	info := app.OutputInfo{Selection: selection, Base: cursor, Head: cursor + domain.ByteOffset(len(raw)), Resize: app.ResizeEvidence{Rows: 24, Cols: 80}}
	done := info
	done.Ended = true
	done.Reason = app.CaptureClosed
	stream := &outputStreamFixture{frames: []app.OutputFrame{{Kind: "snapshot", Info: info}, {Kind: "data", Info: info, Start: cursor, End: info.Head, Data: raw}, {Kind: "end", Info: done}}}
	client := &outputClientFixture{stream: stream}
	cmd := cli.NewRootCLI(cli.WithRuntimeClient(client)).Command()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"runtime", "attach", "--runtime-dir", "/private/runtime", "--epoch", selection.Identity.Epoch, "--run", selection.Identity.Run, "--generation", selection.Identity.Generation, "--worktree", string(selection.Worktree), "--profile", string(selection.Profile), "--cursor", cursor.String()})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !stream.closed || client.calls != 1 || client.selection != selection || client.cursor != cursor {
		t.Fatal("attach binding, invocation count or close incorrect")
	}
	if bytes.Contains(out.Bytes(), []byte{0x1b}) || !strings.Contains(out.String(), base64.StdEncoding.EncodeToString(raw)) {
		t.Fatal("terminal controls rendered or bytes changed")
	}
	decoder := json.NewDecoder(&out)
	var first, data, last map[string]any
	if decoder.Decode(&first) != nil || decoder.Decode(&data) != nil || decoder.Decode(&last) != nil {
		t.Fatal("not JSON lines")
	}
	if data["start"] != cursor.String() || last["kind"] != "end" {
		t.Fatal("lossy counter or missing end")
	}
}
