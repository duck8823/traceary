package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	apptypes "github.com/duck8823/traceary/application/types"
)

func TestAntigravityCurrentTranscriptSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	content := "" +
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","content":"first prompt"}` + "\n" +
		`{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","thinking":"reason one","content":"first answer"}` + "\n" +
		`{"step_index":2,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","content":"latest prompt"}` + "\n" +
		`{"step_index":3,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","thinking":"latest reasoning","content":"latest answer"}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	prompt, ok := extractAntigravityPrompt(path)
	if !ok || prompt != "latest prompt" {
		t.Fatalf("extractAntigravityPrompt() = %q, %v", prompt, ok)
	}
	blocks, ok := readLastAssistantTranscriptBlocksLenient(path)
	if !ok || len(blocks) != 2 {
		t.Fatalf("readLastAssistantTranscriptBlocksLenient() = %#v, %v", blocks, ok)
	}
	if blocks[0].Type != apptypes.EventBodyBlockTypeThinking || blocks[0].Text != "latest reasoning" {
		t.Fatalf("thinking block = %#v", blocks[0])
	}
	if blocks[1].Type != apptypes.EventBodyBlockTypeText || blocks[1].Text != "latest answer" {
		t.Fatalf("text block = %#v", blocks[1])
	}
}

func TestReadLastAntigravityCompletedTurn(t *testing.T) {
	tests := []struct {
		name       string
		lines      []string
		wantOK     bool
		wantPrompt string
		wantText   string
	}{
		{
			name: "uses latest completed prompt and response",
			lines: []string{
				`{"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","content":"first"}`,
				`{"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","content":"answer one"}`,
				`{"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","content":"latest"}`,
				`{"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","content":"answer two"}`,
			},
			wantOK: true, wantPrompt: "latest", wantText: "answer two",
		},
		{
			name: "uses last response within one prompt generation",
			lines: []string{
				`{"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","content":"prompt"}`,
				`{"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","content":"planning"}`,
				`{"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","content":"final"}`,
			},
			wantOK: true, wantPrompt: "prompt", wantText: "final",
		},
		{
			name: "does not pair unfinished latest prompt with stale response",
			lines: []string{
				`{"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","content":"first"}`,
				`{"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","content":"old answer"}`,
				`{"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","content":"unfinished"}`,
				`{"source":"MODEL","type":"PLANNER_RESPONSE","status":"ERROR","content":"failed"}`,
			},
			wantOK: false,
		},
		{
			name: "does not pair current prompt with legacy response row",
			lines: []string{
				`{"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","content":"unfinished"}`,
				`{"role":"assistant","content":"legacy stale response"}`,
			},
			wantOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "transcript.jsonl")
			if err := os.WriteFile(path, []byte(strings.Join(tt.lines, "\n")+"\n"), 0o600); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}
			turn, state := readLastAntigravityCompletedTurn(path)
			ok := state == antigravityTurnComplete
			if ok != tt.wantOK {
				t.Fatalf("state = %v, want completed=%v (turn=%#v)", state, tt.wantOK, turn)
			}
			if !tt.wantOK {
				return
			}
			if turn.Prompt != tt.wantPrompt {
				t.Fatalf("Prompt = %q, want %q", turn.Prompt, tt.wantPrompt)
			}
			if len(turn.Blocks) != 1 || turn.Blocks[0].Text != tt.wantText {
				t.Fatalf("Blocks = %#v, want text %q", turn.Blocks, tt.wantText)
			}
		})
	}
}

func TestExtractAntigravityTranscriptDoesNotFallBackAfterTurnResolution(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join([]string{
		`{"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","content":"unfinished"}`,
		`{"role":"assistant","content":"stale response"}`,
	}, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	payload := normalizeAntigravityPayload(antigravityNormalizeOptions{
		transcriptPath: path,
		turnResolved:   true,
	})
	if blocks, ok := extractAntigravityTranscript(payload); ok || len(blocks) != 0 {
		t.Fatalf("extractAntigravityTranscript() = %#v, %v; want no stale fallback", blocks, ok)
	}
}
