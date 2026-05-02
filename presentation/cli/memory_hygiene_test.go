package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

// TestWriteMemoryHygieneScanResult_LowQualityCandidateJSON pins the
// low_quality_candidate JSON shape so consumers can rely on the new
// fields (#864). The fixture mirrors the wire output an MCP host would
// see, and the assertions check both the count and the per-suggestion
// fields.
func TestWriteMemoryHygieneScanResult_LowQualityCandidateJSON(t *testing.T) {
	t.Parallel()

	workspace, err := domtypes.WorkspaceFrom("github.com/example/repo")
	if err != nil {
		t.Fatalf("WorkspaceFrom: %v", err)
	}
	scope := domtypes.WorkspaceScopeOf(workspace)
	updatedAt := time.Date(2026, 5, 1, 9, 30, 0, 0, time.UTC)

	suggestion := apptypes.MemoryHygieneSuggestion{
		MemoryID:       domtypes.MemoryID("mem-noise"),
		Kind:           apptypes.MemoryHygieneSuggestionLowQualityCandidate,
		Reason:         "low-quality extraction: diff_fragment",
		Fact:           "+def _required_env(name):",
		Scope:          scope,
		UpdatedAt:      updatedAt,
		Status:         domtypes.MemoryStatusCandidate,
		Source:         domtypes.MemorySourceExtracted,
		QualityReasons: []string{"diff_fragment"},
	}
	result := apptypes.MemoryHygieneScanResult{
		Suggestions:              []apptypes.MemoryHygieneSuggestion{suggestion},
		LowQualityCandidateCount: 1,
	}

	var buf bytes.Buffer
	if err := writeMemoryHygieneScanResult(&buf, result, true); err != nil {
		t.Fatalf("writeMemoryHygieneScanResult: %v", err)
	}

	var payload struct {
		LowQualityCandidateCount int `json:"low_quality_candidate_count"`
		Suggestions              []struct {
			MemoryID       string   `json:"memory_id"`
			Kind           string   `json:"kind"`
			Reason         string   `json:"reason"`
			Fact           string   `json:"fact"`
			Status         string   `json:"status"`
			Source         string   `json:"source"`
			QualityReasons []string `json:"quality_reasons"`
		} `json:"suggestions"`
	}
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v\noutput=%s", err, buf.String())
	}
	if payload.LowQualityCandidateCount != 1 {
		t.Fatalf("low_quality_candidate_count = %d, want 1", payload.LowQualityCandidateCount)
	}
	if len(payload.Suggestions) != 1 {
		t.Fatalf("suggestions = %d, want 1", len(payload.Suggestions))
	}
	got := payload.Suggestions[0]
	if got.Kind != "low_quality_candidate" {
		t.Fatalf("kind = %q, want low_quality_candidate", got.Kind)
	}
	if got.Status != "candidate" {
		t.Fatalf("status = %q, want candidate", got.Status)
	}
	if got.Source != "extracted" {
		t.Fatalf("source = %q, want extracted", got.Source)
	}
	if len(got.QualityReasons) != 1 || got.QualityReasons[0] != "diff_fragment" {
		t.Fatalf("quality_reasons = %v, want [diff_fragment]", got.QualityReasons)
	}
	if got.Reason == "" {
		t.Fatalf("reason must not be empty")
	}
	if got.Fact != "+def _required_env(name):" {
		t.Fatalf("fact = %q, want diff fragment", got.Fact)
	}
}

func TestWriteMemoryHygieneScanResult_LowQualityCandidateText(t *testing.T) {
	t.Parallel()

	workspace, err := domtypes.WorkspaceFrom("github.com/example/repo")
	if err != nil {
		t.Fatalf("WorkspaceFrom: %v", err)
	}
	scope := domtypes.WorkspaceScopeOf(workspace)

	suggestion := apptypes.MemoryHygieneSuggestion{
		MemoryID:       domtypes.MemoryID("mem-noise"),
		Kind:           apptypes.MemoryHygieneSuggestionLowQualityCandidate,
		Reason:         "low-quality extraction: standalone_command",
		Fact:           "git pull --ff-only origin main",
		Scope:          scope,
		UpdatedAt:      time.Date(2026, 5, 1, 9, 30, 0, 0, time.UTC),
		Status:         domtypes.MemoryStatusCandidate,
		Source:         domtypes.MemorySourceExtracted,
		QualityReasons: []string{"standalone_command"},
	}
	result := apptypes.MemoryHygieneScanResult{
		Suggestions:              []apptypes.MemoryHygieneSuggestion{suggestion},
		LowQualityCandidateCount: 1,
	}

	var buf bytes.Buffer
	if err := writeMemoryHygieneScanResult(&buf, result, false); err != nil {
		t.Fatalf("writeMemoryHygieneScanResult: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"low_quality_candidate",
		"mem-noise",
		"status=candidate",
		"source=extracted",
		"git pull --ff-only origin main",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("text output missing %q\noutput=%s", want, out)
		}
	}
	if !strings.Contains(out, "低品質") && !strings.Contains(out, "low_quality_candidates=1") {
		t.Fatalf("summary line missing low-quality counter: %s", out)
	}
}
