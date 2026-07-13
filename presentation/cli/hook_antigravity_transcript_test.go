package cli

import (
	"os"
	"path/filepath"
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
