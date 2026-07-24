package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

type codexHookCoverageFixture struct {
	HostVersion string `json:"host_version"`
	Probe       string `json:"probe"`
	Modes       []struct {
		Name            string   `json:"name"`
		Ephemeral       bool     `json:"ephemeral"`
		OutputModes     []string `json:"output_modes_tested"`
		EmittedHooks    []string `json:"emitted_hooks"`
		FinalTurnSource string   `json:"final_turn_source"`
		TranscriptPath  string   `json:"transcript_path"`
	} `json:"modes"`
}

func TestCodexHeadlessHookCoverageFixture(t *testing.T) {
	t.Parallel()
	payload, err := os.ReadFile("testdata/codex_hooks/v0.145.0/coverage.json")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var fixture codexHookCoverageFixture
	if err := json.Unmarshal(payload, &fixture); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if fixture.HostVersion != "0.145.0" || !strings.Contains(fixture.Probe, "no tools or history reads") {
		t.Fatalf("fixture provenance = version %q probe %q", fixture.HostVersion, fixture.Probe)
	}
	if len(fixture.Modes) != 3 {
		t.Fatalf("len(Modes) = %d, want 3", len(fixture.Modes))
	}

	for _, mode := range fixture.Modes {
		mode := mode
		name := mode.Name
		if mode.Ephemeral {
			name += "_ephemeral"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for _, hook := range []string{"SessionStart", "UserPromptSubmit", "Stop"} {
				if !slices.Contains(mode.EmittedHooks, hook) {
					t.Errorf("%s emitted hooks missing %s: %v", name, hook, mode.EmittedHooks)
				}
			}
			if mode.FinalTurnSource != "Stop.last_assistant_message" {
				t.Errorf("%s final turn source = %q", name, mode.FinalTurnSource)
			}
			if mode.Name == "headless" &&
				(!slices.Contains(mode.OutputModes, "text") || !slices.Contains(mode.OutputModes, "jsonl")) {
				t.Errorf("%s output modes = %v, want text and jsonl", name, mode.OutputModes)
			}
			if mode.Name == "headless" && mode.Ephemeral && mode.TranscriptPath != "absent" {
				t.Errorf("ephemeral transcript_path = %q, want absent", mode.TranscriptPath)
			}
			if mode.Name == "headless" && !mode.Ephemeral && mode.TranscriptPath != "present" {
				t.Errorf("non-ephemeral transcript_path = %q, want present", mode.TranscriptPath)
			}
		})
	}
}

func TestRootCLI_HookTranscriptCommand_PersistsCodexHeadlessMarkersFromStop(t *testing.T) {
	tests := []struct {
		name      string
		fixture   string
		sessionID string
		marker    string
	}{
		{
			name:      "ephemeral",
			fixture:   "headless_ephemeral_stop.json",
			sessionID: "codex-headless-ephemeral-fixture",
			marker:    "TRACEARY_HEADLESS_EPHEMERAL_MARKER",
		},
		{
			name:      "non-ephemeral",
			fixture:   "headless_persisted_stop.json",
			sessionID: "codex-headless-persisted-fixture",
			marker:    "TRACEARY_HEADLESS_PERSISTED_MARKER",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			// Durable hook replay is process-global by default. Isolate HOME so a
			// real pending delivery cannot be replayed through this event stub.
			t.Setenv("HOME", t.TempDir())
			t.Setenv("TRACEARY_WORKSPACE", "github.com/duck8823/traceary")
			payload, err := os.ReadFile(filepath.Join("testdata/codex_hooks/v0.145.0", tt.fixture))
			if err != nil {
				t.Fatalf("ReadFile() error = %v", err)
			}

			eventStub := &eventUsecaseStub{}
			rootCmd := newTestRootCLI(
				cli.WithStoreManagement(&storeManagementUsecaseStub{}),
				cli.WithEvent(eventStub),
			).Command()
			rootCmd.SetOut(&bytes.Buffer{})
			rootCmd.SetErr(&bytes.Buffer{})
			rootCmd.SetIn(bytes.NewReader(payload))
			rootCmd.SetArgs([]string{"hook", "transcript", "codex"})
			if err := rootCmd.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}

			if eventStub.logCall.kind != types.EventKindTranscript {
				t.Fatalf("kind = %q, want transcript", eventStub.logCall.kind)
			}
			if eventStub.logCall.sessionID.String() != tt.sessionID {
				t.Errorf("session ID = %q, want %q", eventStub.logCall.sessionID, tt.sessionID)
			}
			if eventStub.logCall.sourceHook != "stop" {
				t.Errorf("source hook = %q, want stop", eventStub.logCall.sourceHook)
			}
			blocks := apptypes.ParseEventBodyBlocks(eventStub.logCall.message)
			if len(blocks) != 1 || blocks[0].Text != tt.marker {
				t.Fatalf("transcript blocks = %+v, want marker %q", blocks, tt.marker)
			}
		})
	}
}
