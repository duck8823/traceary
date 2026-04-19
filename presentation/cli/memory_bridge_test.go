package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	apptypes "github.com/duck8823/traceary/application/types"
)

func TestMergeMemoryExportIntoExistingFile_PreservesHandWrittenContent(t *testing.T) {
	t.Parallel()

	existing := "# Project instructions\n\n" +
		"## Tech stack\n- Go\n\n" +
		usecaseMemoryBridgeMarkerBegin + "\n" +
		"stale block\n" +
		usecaseMemoryBridgeMarkerEnd + "\n\n" +
		"## Conventions\n- Conventional commits\n"

	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	generated := usecaseMemoryBridgeMarkerBegin + "\nfresh block\n" + usecaseMemoryBridgeMarkerEnd + "\n"
	merged, err := mergeMemoryExportIntoExistingFile(path, generated)
	if err != nil {
		t.Fatalf("mergeMemoryExportIntoExistingFile: %v", err)
	}

	if !strings.Contains(merged, "## Tech stack") || !strings.Contains(merged, "## Conventions") {
		t.Fatalf("merge dropped hand-written sections: %q", merged)
	}
	if strings.Contains(merged, "stale block") {
		t.Fatalf("merge kept old managed block content: %q", merged)
	}
	if !strings.Contains(merged, "fresh block") {
		t.Fatalf("merge dropped new managed block: %q", merged)
	}
	// Running the merge a second time on the merged output must be a no-op.
	second, err := mergeMemoryExportIntoExistingFile(path, generated)
	if err != nil {
		t.Fatalf("second merge: %v", err)
	}
	if err := os.WriteFile(path, []byte(merged), 0o644); err != nil {
		t.Fatalf("WriteFile second: %v", err)
	}
	third, err := mergeMemoryExportIntoExistingFile(path, generated)
	if err != nil {
		t.Fatalf("third merge: %v", err)
	}
	if second == "" || third == "" {
		t.Fatalf("merge returned empty string")
	}
	if merged != third {
		t.Fatalf("merge is not idempotent after write-back")
	}
}

func TestMergeMemoryExportIntoExistingFile_AppendsWhenNoMarkers(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(path, []byte("# Project\n- existing bullet\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	generated := usecaseMemoryBridgeMarkerBegin + "\nblock\n" + usecaseMemoryBridgeMarkerEnd + "\n"
	merged, err := mergeMemoryExportIntoExistingFile(path, generated)
	if err != nil {
		t.Fatalf("mergeMemoryExportIntoExistingFile: %v", err)
	}
	if !strings.Contains(merged, "# Project") {
		t.Fatalf("merge lost pre-existing content: %q", merged)
	}
	if !strings.Contains(merged, generated) {
		t.Fatalf("merge did not append generated block: %q", merged)
	}
}

func TestMergeMemoryExportIntoExistingFile_RefusesToOverwriteFutureVersion(t *testing.T) {
	t.Parallel()

	// A future Traceary build wrote the block as :v2. A v1 binary must
	// refuse to downgrade that block — otherwise the newer content is
	// silently overwritten and the operator only finds out when they
	// can no longer reproduce the newer snapshot.
	existing := "# Project\n\n" +
		"## Tech stack\n- Go\n\n" +
		"<!-- traceary-memories:begin:v2 -->\n" +
		"future block managed by a newer Traceary\n" +
		usecaseMemoryBridgeMarkerEnd + "\n\n" +
		"## Conventions\n- Conventional commits\n"

	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	generated := usecaseMemoryBridgeMarkerBegin + "\nfresh block\n" + usecaseMemoryBridgeMarkerEnd + "\n"

	_, err := mergeMemoryExportIntoExistingFile(path, generated)
	if err == nil {
		t.Fatalf("expected an error when the existing block is newer than this binary's version")
	}
	if !strings.Contains(err.Error(), ":v2") {
		t.Fatalf("error should mention the newer version, got %q", err.Error())
	}
}

func TestMergeMemoryExportIntoExistingFile_IgnoresProseMarkerMention(t *testing.T) {
	t.Parallel()

	// A line inside a prose paragraph that mentions the marker string
	// inline must not be mistaken for the managed block. The anchored
	// regex requires the marker to start at the beginning of a line.
	existing := "# Project instructions\n\n" +
		"The bridge marker looks like <!-- traceary-memories:begin:v1 --> when Traceary writes it.\n" +
		"## Conventions\n- Keep hand-written content.\n"

	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	generated := usecaseMemoryBridgeMarkerBegin + "\nblock\n" + usecaseMemoryBridgeMarkerEnd + "\n"

	merged, err := mergeMemoryExportIntoExistingFile(path, generated)
	if err != nil {
		t.Fatalf("mergeMemoryExportIntoExistingFile: %v", err)
	}
	if !strings.Contains(merged, "The bridge marker looks like <!-- traceary-memories:begin:v1 -->") {
		t.Fatalf("prose mention should have been preserved, got %q", merged)
	}
	if !strings.Contains(merged, "## Conventions") {
		t.Fatalf("hand-written section should have been preserved, got %q", merged)
	}
	if !strings.HasSuffix(merged, generated) {
		t.Fatalf("generated block should be appended at the end, got %q", merged)
	}
}

func TestMergeMemoryExportIntoExistingFile_MissingFileUsesGeneratedAsIs(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "GEMINI.md")
	generated := usecaseMemoryBridgeMarkerBegin + "\nonly block\n" + usecaseMemoryBridgeMarkerEnd + "\n"
	merged, err := mergeMemoryExportIntoExistingFile(path, generated)
	if err != nil {
		t.Fatalf("mergeMemoryExportIntoExistingFile: %v", err)
	}
	if merged != generated {
		t.Fatalf("missing-file merge must equal generated: %q", merged)
	}
	// No file was created; writer is the caller's responsibility.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("merge must not create the file, got stat err = %v", err)
	}
}

func TestWriteMemoryExportJSONSummary_EmitsTargetAndCount(t *testing.T) {
	t.Parallel()

	result := apptypes.MemoryExportResult{
		Target:        apptypes.MemoryBridgeTargetClaude,
		ExportedCount: 7,
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "summary.json")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer func() { _ = f.Close() }()
	if err := writeMemoryExportJSONSummary(f, result); err != nil {
		t.Fatalf("writeMemoryExportJSONSummary: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	body := string(data)
	if !strings.Contains(body, `"target": "claude"`) || !strings.Contains(body, `"exported_count": 7`) {
		t.Fatalf("unexpected JSON body: %q", body)
	}
}
