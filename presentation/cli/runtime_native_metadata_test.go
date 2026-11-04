package cli_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/duck8823/traceary/presentation/cli"
)

type nativeMetadataSink struct {
	called   bool
	root     string
	input    string
	deadline bool
}

func (s *nativeMetadataSink) Observe(ctx context.Context, root string, in io.Reader) error {
	s.called = true
	s.root = root
	data, err := io.ReadAll(in)
	if err != nil {
		return errors.New("synthetic native input read failed")
	}
	s.input = string(data)
	deadline, ok := ctx.Deadline()
	s.deadline = ok && time.Until(deadline) > 0 && time.Until(deadline) <= 5*time.Second
	return errors.New("synthetic borrowed input error must not be emitted")
}

func TestNativeSessionMetadataPlumbingIsSilentOptionalAndDoesNotOpenCoreStore(t *testing.T) {
	for _, configured := range []bool{false, true} {
		t.Run(map[bool]string{false: "unconfigured", true: "configured"}[configured], func(t *testing.T) {
			sink := &nativeMetadataSink{}
			options := []cli.RootCLIOption{}
			if configured {
				options = append(options, cli.WithNativeSessionMetadata(sink))
			}
			root := cli.NewRootCLI(options...).Command()
			var output bytes.Buffer
			root.SetOut(&output)
			root.SetErr(&output)
			root.SetIn(bytes.NewBufferString("synthetic native stdin"))
			root.SetArgs([]string{"runtime", "native-session-start", "--slot", "/not-opened-by-presentation"})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if output.Len() != 0 {
				t.Fatalf("optional metadata emitted output: %q", output.String())
			}
			if configured && (!sink.called || sink.root != "/not-opened-by-presentation" || sink.input != "synthetic native stdin" || !sink.deadline) {
				t.Fatalf("metadata boundary lost: %+v", sink)
			}
		})
	}
}

func TestNativeSessionMetadataPlumbingIsNotAdvertisedAsOperatorCommand(t *testing.T) {
	root := cli.NewRootCLI().Command()
	cmd, _, err := root.Find([]string{"runtime", "native-session-start"})
	if err != nil || cmd.Name() != "native-session-start" || !cmd.Hidden {
		t.Fatalf("internal plumbing exposed: %v", err)
	}
}
