package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	app "github.com/duck8823/traceary/application/runtime"
	"github.com/duck8823/traceary/presentation/cli"
)

type resumeClientFixture struct {
	runtimeClientFixture
	resumeCalls int
	request     app.ResumeRequest
	root        string
}

func (c *resumeClientFixture) Resume(_ context.Context, root string, r app.ResumeRequest) (app.ResumeResult, error) {
	c.resumeCalls++
	c.root = root
	c.request = r
	return app.ResumeResult{}, app.ErrLaunchUnknown
}
func resumeArgs() []string {
	return []string{"runtime", "resume", "--runtime-dir", "/private/runtime", "--epoch", strings.Repeat("e", 32), "--previous-epoch", strings.Repeat("1", 32), "--run", strings.Repeat("2", 32), "--generation", strings.Repeat("3", 32), "--worktree", "w_" + strings.Repeat("a", 64), "--profile", "claude", "--request-id", "original-resume"}
}
func TestRuntimeResumeCLIUnknownPreservesOriginalSelectionWithoutReadingInput(t *testing.T) {
	client := &resumeClientFixture{}
	cmd := cli.NewRootCLI(cli.WithRuntimeClient(client)).Command()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	input := bytes.NewReader([]byte("synthetic-private-pending-input"))
	cmd.SetIn(input)
	cmd.SetArgs(append(resumeArgs(), "--acknowledge-work-loss"))
	if err := cmd.Execute(); !errors.Is(err, app.ErrLaunchUnknown) {
		t.Fatalf("unknown outcome: %v", err)
	}
	if client.resumeCalls != 1 || client.root != "/private/runtime" || client.request.RequestID != "original-resume" || client.request.Epoch != strings.Repeat("e", 32) || client.request.Selection.Identity.Epoch != strings.Repeat("1", 32) || !client.request.WorkLossAcknowledged {
		t.Fatal("resume lost original request correlation or consent")
	}
	if input.Len() != len("synthetic-private-pending-input") {
		t.Fatal("resume read pending stdin")
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["request_id"] != "original-resume" || got["outcome"] != "unknown" || got["external_actors_coordinated"] != false || got["descendant_inactivity_proven"] != false || strings.Contains(out.String(), "synthetic-private") {
		t.Fatal("unknown response invented authority or exposed pending input")
	}
}
func TestRuntimeResumeCLIRejectsMissingConsentAndPayloadOverrides(t *testing.T) {
	for _, extra := range [][]string{nil, {"--acknowledge-work-loss=false"}, {"--acknowledge-work-loss", "private-payload"}, {"--acknowledge-work-loss", "--input", "private-payload"}, {"--acknowledge-work-loss", "--native-session-id", "arbitrary"}} {
		client := &resumeClientFixture{}
		cmd := cli.NewRootCLI(cli.WithRuntimeClient(client)).Command()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs(append(resumeArgs(), extra...))
		if err := cmd.Execute(); err == nil || client.resumeCalls != 0 {
			t.Fatalf("unsafe resume admitted: %v", err)
		}
	}
}
