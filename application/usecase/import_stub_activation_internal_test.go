package usecase

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	apptypes "github.com/duck8823/traceary/application/types"
)

// fakeActivationFileWriter is a controllable activationFileWriter used
// by the planner tests so they can simulate inspect / read / write
// failures without leaning on platform-specific filesystem behavior.
type fakeActivationFileWriter struct {
	files      map[string]string
	inspectErr map[string]error
	readErr    map[string]error
	writeErr   map[string]error
	writes     []fakeActivationWrite
}

type fakeActivationWrite struct {
	path    string
	content string
}

func newFakeActivationFileWriter() *fakeActivationFileWriter {
	return &fakeActivationFileWriter{
		files:      map[string]string{},
		inspectErr: map[string]error{},
		readErr:    map[string]error{},
		writeErr:   map[string]error{},
	}
}

func (f *fakeActivationFileWriter) Inspect(path string) (os.FileInfo, bool, error) {
	if err := f.inspectErr[path]; err != nil {
		return nil, false, err
	}
	if _, ok := f.files[path]; ok {
		return nil, true, nil
	}
	return nil, false, nil
}

func (f *fakeActivationFileWriter) ReadIfExists(path string) (string, bool, error) {
	if err := f.readErr[path]; err != nil {
		return "", false, err
	}
	if v, ok := f.files[path]; ok {
		return v, true, nil
	}
	return "", false, nil
}

func (f *fakeActivationFileWriter) WriteAtomic(path string, content string) error {
	f.writes = append(f.writes, fakeActivationWrite{path: path, content: content})
	if err := f.writeErr[path]; err != nil {
		return err
	}
	f.files[path] = content
	return nil
}

func mustExternalMemoryBlock() string {
	return MemoryBridgeMarkerBegin + "\n" + memoryBridgeWarning + "\n\n# Traceary-managed claude memories\n\n_No accepted durable memories matched the export scope._\n\n" + MemoryBridgeMarkerEnd + "\n"
}

func newPlannerWithWriter(writer activationFileWriter) *importStubActivationPlanner {
	return &importStubActivationPlanner{fileWriter: writer}
}

func planClaudeFixture(criteria importStubActivationCriteria) importStubActivationCriteria {
	if criteria.Target == "" {
		criteria.Target = apptypes.MemoryBridgeTargetClaude
	}
	if criteria.HostContextPath == "" {
		criteria.HostContextPath = "/repo/CLAUDE.md"
	}
	if criteria.ExternalMemoryPath == "" {
		criteria.ExternalMemoryPath = "/repo/.traceary/memories/claude.md"
	}
	if criteria.ImportPath == "" {
		criteria.ImportPath = "./.traceary/memories/claude.md"
	}
	if criteria.ExternalMarkdown == "" {
		criteria.ExternalMarkdown = mustExternalMemoryBlock()
	}
	return criteria
}

func TestImportStubActivationPlanner_PlanCreatesBothFilesWhenMissing(t *testing.T) {
	t.Parallel()

	writer := newFakeActivationFileWriter()
	plan := newPlannerWithWriter(writer).Plan(planClaudeFixture(importStubActivationCriteria{}))

	if plan.HostContext.Status != apptypes.MemoryActivationStatusMissing {
		t.Fatalf("HostContext.Status = %q, want missing", plan.HostContext.Status)
	}
	if plan.ExternalMemory.Status != apptypes.MemoryActivationStatusMissing {
		t.Fatalf("ExternalMemory.Status = %q, want missing", plan.ExternalMemory.Status)
	}
	if plan.HostContext.Action != apptypes.MemoryActivationApplyCreated {
		t.Fatalf("HostContext.Action = %q, want created", plan.HostContext.Action)
	}
	if plan.ExternalMemory.Action != apptypes.MemoryActivationApplyCreated {
		t.Fatalf("ExternalMemory.Action = %q, want created", plan.ExternalMemory.Action)
	}
	if plan.HostContext.Existing || plan.ExternalMemory.Existing {
		t.Fatalf("Existing = host=%v external=%v, want both false", plan.HostContext.Existing, plan.ExternalMemory.Existing)
	}
	if !strings.Contains(plan.HostContext.Markdown, "@./.traceary/memories/claude.md") {
		t.Fatalf("planned host stub missing import line: %q", plan.HostContext.Markdown)
	}
	if !strings.Contains(plan.ExternalMemory.Markdown, MemoryBridgeMarkerBegin) {
		t.Fatalf("planned external memory missing managed block: %q", plan.ExternalMemory.Markdown)
	}
	if len(writer.writes) != 0 {
		t.Fatalf("Plan must not write any files, got %d writes", len(writer.writes))
	}
}

func TestImportStubActivationPlanner_PlanReportsBothInSyncWhenAlreadyApplied(t *testing.T) {
	t.Parallel()

	writer := newFakeActivationFileWriter()
	stub := renderImportStubBlock(apptypes.MemoryBridgeTargetClaude, "./.traceary/memories/claude.md")
	external := mustExternalMemoryBlock()
	writer.files["/repo/CLAUDE.md"] = "# user notes\n\n" + stub
	writer.files["/repo/.traceary/memories/claude.md"] = external

	plan := newPlannerWithWriter(writer).Plan(planClaudeFixture(importStubActivationCriteria{
		ExternalMarkdown: external,
	}))

	if plan.HostContext.Status != apptypes.MemoryActivationStatusInSync {
		t.Fatalf("HostContext.Status = %q, want in_sync", plan.HostContext.Status)
	}
	if plan.HostContext.Action != apptypes.MemoryActivationApplyNoop {
		t.Fatalf("HostContext.Action = %q, want noop", plan.HostContext.Action)
	}
	if plan.ExternalMemory.Status != apptypes.MemoryActivationStatusInSync {
		t.Fatalf("ExternalMemory.Status = %q, want in_sync", plan.ExternalMemory.Status)
	}
	if plan.ExternalMemory.Action != apptypes.MemoryActivationApplyNoop {
		t.Fatalf("ExternalMemory.Action = %q, want noop", plan.ExternalMemory.Action)
	}
}

func TestImportStubActivationPlanner_PlanReportsStaleStubAndInSyncExternal(t *testing.T) {
	t.Parallel()

	writer := newFakeActivationFileWriter()
	external := mustExternalMemoryBlock()
	writer.files["/repo/CLAUDE.md"] = "# user notes\n\n<!-- traceary-memory-import:begin:v1 -->\n<!-- DO NOT EDIT: this import is managed by Traceary. Run `traceary memory activate --target claude --dry-run --diff` before applying updates. -->\n@./old-path/claude.md\n<!-- traceary-memory-import:end -->\n"
	writer.files["/repo/.traceary/memories/claude.md"] = external

	plan := newPlannerWithWriter(writer).Plan(planClaudeFixture(importStubActivationCriteria{
		ExternalMarkdown: external,
	}))

	if plan.HostContext.Status != apptypes.MemoryActivationStatusStale {
		t.Fatalf("HostContext.Status = %q, want stale", plan.HostContext.Status)
	}
	if plan.HostContext.Action != apptypes.MemoryActivationApplyUpdated {
		t.Fatalf("HostContext.Action = %q, want updated", plan.HostContext.Action)
	}
	if plan.ExternalMemory.Status != apptypes.MemoryActivationStatusInSync {
		t.Fatalf("ExternalMemory.Status = %q, want in_sync", plan.ExternalMemory.Status)
	}
	if plan.ExternalMemory.Action != apptypes.MemoryActivationApplyNoop {
		t.Fatalf("ExternalMemory.Action = %q, want noop", plan.ExternalMemory.Action)
	}
	if !strings.Contains(plan.HostContext.Markdown, "# user notes") {
		t.Fatalf("planned host markdown lost user content: %q", plan.HostContext.Markdown)
	}
	if strings.Contains(plan.HostContext.Markdown, "./old-path/claude.md") {
		t.Fatalf("planned host markdown retained stale import path: %q", plan.HostContext.Markdown)
	}
}

func TestImportStubActivationPlanner_PlanReportsInSyncStubAndStaleExternal(t *testing.T) {
	t.Parallel()

	writer := newFakeActivationFileWriter()
	stub := renderImportStubBlock(apptypes.MemoryBridgeTargetClaude, "./.traceary/memories/claude.md")
	staleExternal := MemoryBridgeMarkerBegin + "\nstale managed body\n" + MemoryBridgeMarkerEnd + "\n"
	writer.files["/repo/CLAUDE.md"] = "# user notes\n\n" + stub
	writer.files["/repo/.traceary/memories/claude.md"] = "preface\n\n" + staleExternal + "\nepilogue\n"

	plan := newPlannerWithWriter(writer).Plan(planClaudeFixture(importStubActivationCriteria{}))

	if plan.HostContext.Status != apptypes.MemoryActivationStatusInSync {
		t.Fatalf("HostContext.Status = %q, want in_sync", plan.HostContext.Status)
	}
	if plan.HostContext.Action != apptypes.MemoryActivationApplyNoop {
		t.Fatalf("HostContext.Action = %q, want noop", plan.HostContext.Action)
	}
	if plan.ExternalMemory.Status != apptypes.MemoryActivationStatusStale {
		t.Fatalf("ExternalMemory.Status = %q, want stale", plan.ExternalMemory.Status)
	}
	if plan.ExternalMemory.Action != apptypes.MemoryActivationApplyUpdated {
		t.Fatalf("ExternalMemory.Action = %q, want updated", plan.ExternalMemory.Action)
	}
	for _, want := range []string{"preface", "epilogue"} {
		if !strings.Contains(plan.ExternalMemory.Markdown, want) {
			t.Fatalf("planned external markdown lost %q: %q", want, plan.ExternalMemory.Markdown)
		}
	}
	if strings.Contains(plan.ExternalMemory.Markdown, "stale managed body") {
		t.Fatalf("planned external markdown retained stale body: %q", plan.ExternalMemory.Markdown)
	}
}

func TestImportStubActivationPlanner_PlanReportsHostMissingWhenContextLacksStub(t *testing.T) {
	t.Parallel()

	writer := newFakeActivationFileWriter()
	external := mustExternalMemoryBlock()
	writer.files["/repo/CLAUDE.md"] = "# user notes\n\n- a hand-written instruction\n"
	writer.files["/repo/.traceary/memories/claude.md"] = external

	plan := newPlannerWithWriter(writer).Plan(planClaudeFixture(importStubActivationCriteria{
		ExternalMarkdown: external,
	}))

	if plan.HostContext.Status != apptypes.MemoryActivationStatusMissing {
		t.Fatalf("HostContext.Status = %q, want missing (file exists, stub absent)", plan.HostContext.Status)
	}
	if plan.HostContext.Action != apptypes.MemoryActivationApplyUpdated {
		t.Fatalf("HostContext.Action = %q, want updated (append stub)", plan.HostContext.Action)
	}
	if !plan.HostContext.Existing {
		t.Fatalf("HostContext.Existing = false, want true (file is on disk)")
	}
	if !strings.Contains(plan.HostContext.Markdown, "# user notes\n\n- a hand-written instruction\n\n<!-- traceary-memory-import:begin:v1 -->") {
		t.Fatalf("planned host markdown did not append stub after preserving user content: %q", plan.HostContext.Markdown)
	}
}

func TestImportStubActivationPlanner_PlanReportsInvalidWhenStubBeginIsOrphan(t *testing.T) {
	t.Parallel()

	writer := newFakeActivationFileWriter()
	writer.files["/repo/CLAUDE.md"] = "<!-- traceary-memory-import:begin:v1 -->\n@./.traceary/memories/claude.md\n"
	writer.files["/repo/.traceary/memories/claude.md"] = mustExternalMemoryBlock()

	plan := newPlannerWithWriter(writer).Plan(planClaudeFixture(importStubActivationCriteria{}))

	if plan.HostContext.Status != apptypes.MemoryActivationStatusInvalid {
		t.Fatalf("HostContext.Status = %q, want invalid", plan.HostContext.Status)
	}
	if !strings.Contains(plan.HostContext.Message, "without end marker") {
		t.Fatalf("HostContext.Message = %q, want orphan-begin reason", plan.HostContext.Message)
	}
	if plan.ExternalMemory.Status != apptypes.MemoryActivationStatusInSync {
		t.Fatalf("ExternalMemory.Status = %q, want unaffected in_sync", plan.ExternalMemory.Status)
	}
}

func TestImportStubActivationPlanner_PlanReportsInvalidWhenExternalHasDuplicateBegin(t *testing.T) {
	t.Parallel()

	writer := newFakeActivationFileWriter()
	stub := renderImportStubBlock(apptypes.MemoryBridgeTargetClaude, "./.traceary/memories/claude.md")
	writer.files["/repo/CLAUDE.md"] = stub
	external := MemoryBridgeMarkerBegin + "\nbody1\n" + MemoryBridgeMarkerEnd + "\n" + MemoryBridgeMarkerBegin + "\nbody2\n" + MemoryBridgeMarkerEnd + "\n"
	writer.files["/repo/.traceary/memories/claude.md"] = external

	plan := newPlannerWithWriter(writer).Plan(planClaudeFixture(importStubActivationCriteria{}))

	if plan.ExternalMemory.Status != apptypes.MemoryActivationStatusInvalid {
		t.Fatalf("ExternalMemory.Status = %q, want invalid", plan.ExternalMemory.Status)
	}
	if !strings.Contains(plan.ExternalMemory.Message, "multiple Traceary managed memory blocks") {
		t.Fatalf("ExternalMemory.Message = %q, want duplicate-begin reason", plan.ExternalMemory.Message)
	}
}

func TestImportStubActivationPlanner_PlanReportsInvalidWhenStubMarkerVersionIsNewer(t *testing.T) {
	t.Parallel()

	writer := newFakeActivationFileWriter()
	writer.files["/repo/CLAUDE.md"] = "<!-- traceary-memory-import:begin:v9 -->\n@./.traceary/memories/claude.md\n<!-- traceary-memory-import:end -->\n"
	writer.files["/repo/.traceary/memories/claude.md"] = mustExternalMemoryBlock()

	plan := newPlannerWithWriter(writer).Plan(planClaudeFixture(importStubActivationCriteria{}))

	if plan.HostContext.Status != apptypes.MemoryActivationStatusInvalid {
		t.Fatalf("HostContext.Status = %q, want invalid", plan.HostContext.Status)
	}
	if !strings.Contains(plan.HostContext.Message, "refusing to overwrite newer Traceary managed block version v9") {
		t.Fatalf("HostContext.Message = %q, want newer-version refusal", plan.HostContext.Message)
	}
}

func TestImportStubActivationPlanner_PlanReportsInvalidWhenExternalMarkerVersionIsNewer(t *testing.T) {
	t.Parallel()

	writer := newFakeActivationFileWriter()
	stub := renderImportStubBlock(apptypes.MemoryBridgeTargetClaude, "./.traceary/memories/claude.md")
	writer.files["/repo/CLAUDE.md"] = stub
	writer.files["/repo/.traceary/memories/claude.md"] = "<!-- traceary-memories:begin:v99 -->\nfuture\n" + MemoryBridgeMarkerEnd + "\n"

	plan := newPlannerWithWriter(writer).Plan(planClaudeFixture(importStubActivationCriteria{}))

	if plan.ExternalMemory.Status != apptypes.MemoryActivationStatusInvalid {
		t.Fatalf("ExternalMemory.Status = %q, want invalid", plan.ExternalMemory.Status)
	}
	if !strings.Contains(plan.ExternalMemory.Message, "refusing to overwrite newer Traceary managed block version v99") {
		t.Fatalf("ExternalMemory.Message = %q, want newer-version refusal", plan.ExternalMemory.Message)
	}
}

func TestImportStubActivationPlanner_PlanReportsInvalidWhenInspectFailsOnHost(t *testing.T) {
	t.Parallel()

	writer := newFakeActivationFileWriter()
	writer.inspectErr["/repo/CLAUDE.md"] = errors.New("activation target symlinks are not supported: /repo/CLAUDE.md")
	writer.files["/repo/.traceary/memories/claude.md"] = mustExternalMemoryBlock()

	plan := newPlannerWithWriter(writer).Plan(planClaudeFixture(importStubActivationCriteria{}))

	if plan.HostContext.Status != apptypes.MemoryActivationStatusInvalid {
		t.Fatalf("HostContext.Status = %q, want invalid", plan.HostContext.Status)
	}
	if !strings.Contains(plan.HostContext.Message, "symlinks are not supported") {
		t.Fatalf("HostContext.Message = %q, want symlink reason", plan.HostContext.Message)
	}
	if plan.ExternalMemory.Status != apptypes.MemoryActivationStatusInSync {
		t.Fatalf("ExternalMemory.Status = %q, want unaffected in_sync", plan.ExternalMemory.Status)
	}
}

func TestImportStubActivationPlanner_PlanRejectsHostSymlinkOnRealFilesystem(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	hostPath := filepath.Join(dir, "CLAUDE.md")
	missingTarget := filepath.Join(dir, "missing.md")
	if err := os.Symlink(missingTarget, hostPath); err != nil {
		t.Skipf("symlink unsupported on this platform: %v", err)
	}
	externalPath := filepath.Join(dir, "external.md")
	if err := os.WriteFile(externalPath, []byte(mustExternalMemoryBlock()), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	plan := (&importStubActivationPlanner{}).Plan(importStubActivationCriteria{
		Target:             apptypes.MemoryBridgeTargetClaude,
		HostContextPath:    hostPath,
		ExternalMemoryPath: externalPath,
		ImportPath:         "./external.md",
		ExternalMarkdown:   mustExternalMemoryBlock(),
	})

	if plan.HostContext.Status != apptypes.MemoryActivationStatusInvalid {
		t.Fatalf("HostContext.Status = %q, want invalid", plan.HostContext.Status)
	}
	if !strings.Contains(plan.HostContext.Message, "symlinks are not supported") {
		t.Fatalf("HostContext.Message = %q, want symlink rejection", plan.HostContext.Message)
	}
	info, statErr := os.Lstat(hostPath)
	if statErr != nil {
		t.Fatalf("Lstat: %v", statErr)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("plan must not replace dangling symlink, got mode %s", info.Mode())
	}
}

func TestImportStubActivationPlanner_PlanRendersDiffsForChangedComponentsOnly(t *testing.T) {
	t.Parallel()

	writer := newFakeActivationFileWriter()
	stub := renderImportStubBlock(apptypes.MemoryBridgeTargetClaude, "./.traceary/memories/claude.md")
	external := mustExternalMemoryBlock()
	// host context is in_sync; only external is stale, so only the
	// external diff should be populated.
	writer.files["/repo/CLAUDE.md"] = stub
	writer.files["/repo/.traceary/memories/claude.md"] = MemoryBridgeMarkerBegin + "\nold body\n" + MemoryBridgeMarkerEnd + "\n"

	plan := newPlannerWithWriter(writer).Plan(planClaudeFixture(importStubActivationCriteria{
		ExternalMarkdown: external,
		Diff:             true,
	}))

	if plan.HostContext.Diff != "" {
		t.Fatalf("HostContext.Diff = %q, want empty for in_sync component", plan.HostContext.Diff)
	}
	if plan.ExternalMemory.Diff == "" {
		t.Fatalf("ExternalMemory.Diff is empty, want diff for stale component")
	}
	if !strings.Contains(plan.ExternalMemory.Diff, "--- /repo/.traceary/memories/claude.md") {
		t.Fatalf("ExternalMemory.Diff missing path header: %q", plan.ExternalMemory.Diff)
	}
	got := plan.orderedDiffs()
	if len(got) != 1 || got[0] != plan.ExternalMemory.Diff {
		t.Fatalf("orderedDiffs = %v, want only external diff", got)
	}
}

func TestImportStubActivationPlanner_PlanOrderedDiffsPutsExternalFirst(t *testing.T) {
	t.Parallel()

	writer := newFakeActivationFileWriter()
	external := mustExternalMemoryBlock()
	writer.files["/repo/CLAUDE.md"] = "<!-- traceary-memory-import:begin:v1 -->\n<!-- DO NOT EDIT: this import is managed by Traceary. Run `traceary memory activate --target claude --dry-run --diff` before applying updates. -->\n@./old.md\n<!-- traceary-memory-import:end -->\n"
	writer.files["/repo/.traceary/memories/claude.md"] = MemoryBridgeMarkerBegin + "\nold body\n" + MemoryBridgeMarkerEnd + "\n"

	plan := newPlannerWithWriter(writer).Plan(planClaudeFixture(importStubActivationCriteria{
		ExternalMarkdown: external,
		Diff:             true,
	}))

	got := plan.orderedDiffs()
	if len(got) != 2 {
		t.Fatalf("orderedDiffs len = %d, want 2", len(got))
	}
	if got[0] != plan.ExternalMemory.Diff {
		t.Fatalf("orderedDiffs[0] != ExternalMemory.Diff (apply order requires external first)")
	}
	if got[1] != plan.HostContext.Diff {
		t.Fatalf("orderedDiffs[1] != HostContext.Diff (apply order requires host second)")
	}
}

func TestImportStubActivationPlanner_ApplyWritesBothFilesInExternalThenHostOrder(t *testing.T) {
	t.Parallel()

	writer := newFakeActivationFileWriter()
	planner := newPlannerWithWriter(writer)
	plan := planner.Plan(planClaudeFixture(importStubActivationCriteria{}))

	result, err := planner.Apply(plan)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if result.HostContext.Action != apptypes.MemoryActivationApplyCreated {
		t.Fatalf("result HostContext.Action = %q, want created", result.HostContext.Action)
	}
	if result.ExternalMemory.Action != apptypes.MemoryActivationApplyCreated {
		t.Fatalf("result ExternalMemory.Action = %q, want created", result.ExternalMemory.Action)
	}
	if result.HostContext.Status != apptypes.MemoryActivationStatusInSync {
		t.Fatalf("result HostContext.Status = %q, want in_sync after apply", result.HostContext.Status)
	}
	if result.ExternalMemory.Status != apptypes.MemoryActivationStatusInSync {
		t.Fatalf("result ExternalMemory.Status = %q, want in_sync after apply", result.ExternalMemory.Status)
	}
	if len(writer.writes) != 2 {
		t.Fatalf("writes = %d, want 2", len(writer.writes))
	}
	if writer.writes[0].path != "/repo/.traceary/memories/claude.md" {
		t.Fatalf("first write = %q, want external memory file (apply order: external first)", writer.writes[0].path)
	}
	if writer.writes[1].path != "/repo/CLAUDE.md" {
		t.Fatalf("second write = %q, want host context file (apply order: host second)", writer.writes[1].path)
	}
}

func TestImportStubActivationPlanner_ApplyOnlyWritesNonNoopComponents(t *testing.T) {
	t.Parallel()

	writer := newFakeActivationFileWriter()
	stub := renderImportStubBlock(apptypes.MemoryBridgeTargetClaude, "./.traceary/memories/claude.md")
	external := mustExternalMemoryBlock()
	// Stub is already in sync; only the external file is stale.
	writer.files["/repo/CLAUDE.md"] = stub
	writer.files["/repo/.traceary/memories/claude.md"] = MemoryBridgeMarkerBegin + "\nold body\n" + MemoryBridgeMarkerEnd + "\n"

	planner := newPlannerWithWriter(writer)
	plan := planner.Plan(planClaudeFixture(importStubActivationCriteria{
		ExternalMarkdown: external,
	}))
	if _, err := planner.Apply(plan); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if len(writer.writes) != 1 || writer.writes[0].path != "/repo/.traceary/memories/claude.md" {
		t.Fatalf("writes = %+v, want exactly the external memory write", writer.writes)
	}
}

func TestImportStubActivationPlanner_ApplyIsIdempotentAcrossRuns(t *testing.T) {
	t.Parallel()

	writer := newFakeActivationFileWriter()
	planner := newPlannerWithWriter(writer)

	first, err := planner.Apply(planner.Plan(planClaudeFixture(importStubActivationCriteria{})))
	if err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	if first.HostContext.Action != apptypes.MemoryActivationApplyCreated || first.ExternalMemory.Action != apptypes.MemoryActivationApplyCreated {
		t.Fatalf("first apply actions = host=%q external=%q, want both created", first.HostContext.Action, first.ExternalMemory.Action)
	}

	secondPlan := planner.Plan(planClaudeFixture(importStubActivationCriteria{}))
	if secondPlan.HostContext.Status != apptypes.MemoryActivationStatusInSync {
		t.Fatalf("second plan HostContext.Status = %q, want in_sync after apply", secondPlan.HostContext.Status)
	}
	if secondPlan.ExternalMemory.Status != apptypes.MemoryActivationStatusInSync {
		t.Fatalf("second plan ExternalMemory.Status = %q, want in_sync after apply", secondPlan.ExternalMemory.Status)
	}

	writesBefore := len(writer.writes)
	second, err := planner.Apply(secondPlan)
	if err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	if second.HostContext.Action != apptypes.MemoryActivationApplyNoop || second.ExternalMemory.Action != apptypes.MemoryActivationApplyNoop {
		t.Fatalf("second apply actions = host=%q external=%q, want both noop", second.HostContext.Action, second.ExternalMemory.Action)
	}
	if len(writer.writes) != writesBefore {
		t.Fatalf("second apply emitted %d additional writes, want zero (noop)", len(writer.writes)-writesBefore)
	}
}

func TestImportStubActivationPlanner_ApplyRefusesWhenExternalIsInvalid(t *testing.T) {
	t.Parallel()

	writer := newFakeActivationFileWriter()
	stub := renderImportStubBlock(apptypes.MemoryBridgeTargetClaude, "./.traceary/memories/claude.md")
	writer.files["/repo/CLAUDE.md"] = stub
	writer.files["/repo/.traceary/memories/claude.md"] = MemoryBridgeMarkerBegin + "\nbody1\n" + MemoryBridgeMarkerEnd + "\n" + MemoryBridgeMarkerBegin + "\nbody2\n" + MemoryBridgeMarkerEnd + "\n"

	planner := newPlannerWithWriter(writer)
	plan := planner.Plan(planClaudeFixture(importStubActivationCriteria{}))
	if _, err := planner.Apply(plan); err == nil || !strings.Contains(err.Error(), "refusing to apply invalid external memory file") {
		t.Fatalf("Apply err = %v, want invalid-external refusal", err)
	}
	if len(writer.writes) != 0 {
		t.Fatalf("Apply must not write when external is invalid, got writes = %+v", writer.writes)
	}
}

func TestImportStubActivationPlanner_ApplyRefusesWhenHostIsInvalid(t *testing.T) {
	t.Parallel()

	writer := newFakeActivationFileWriter()
	external := mustExternalMemoryBlock()
	writer.files["/repo/CLAUDE.md"] = "<!-- traceary-memory-import:begin:v1 -->\n@./.traceary/memories/claude.md\n"
	writer.files["/repo/.traceary/memories/claude.md"] = external

	planner := newPlannerWithWriter(writer)
	plan := planner.Plan(planClaudeFixture(importStubActivationCriteria{
		ExternalMarkdown: external,
	}))
	if _, err := planner.Apply(plan); err == nil || !strings.Contains(err.Error(), "refusing to apply invalid host context stub") {
		t.Fatalf("Apply err = %v, want invalid-host refusal", err)
	}
	if len(writer.writes) != 0 {
		t.Fatalf("Apply must not write when host is invalid, got writes = %+v", writer.writes)
	}
}

func TestImportStubActivationPlanner_ApplySurfacesPermissionFailureOnExternalWrite(t *testing.T) {
	t.Parallel()

	writer := newFakeActivationFileWriter()
	writer.writeErr["/repo/.traceary/memories/claude.md"] = errors.New("simulated EACCES on external memory file")

	planner := newPlannerWithWriter(writer)
	plan := planner.Plan(planClaudeFixture(importStubActivationCriteria{}))
	result, err := planner.Apply(plan)
	if err == nil {
		t.Fatalf("Apply err = nil, want permission failure on external write")
	}
	if !strings.Contains(err.Error(), "simulated EACCES on external memory file") {
		t.Fatalf("Apply err = %v, want wrapped permission failure", err)
	}
	if len(writer.writes) != 1 {
		t.Fatalf("writes = %d, want 1 (only external attempted before host)", len(writer.writes))
	}
	if writer.writes[0].path != "/repo/.traceary/memories/claude.md" {
		t.Fatalf("attempted write path = %q, want external", writer.writes[0].path)
	}
	if result.ExternalMemory.Action != "" {
		t.Fatalf("result.ExternalMemory.Action = %q, want empty because the external write failed", result.ExternalMemory.Action)
	}
	if result.HostContext.Action != "" {
		t.Fatalf("result.HostContext.Action = %q, want empty because host write was never attempted", result.HostContext.Action)
	}
}

func TestImportStubActivationPlanner_ApplySurfacesPermissionFailureOnHostWriteAfterExternalSucceeds(t *testing.T) {
	t.Parallel()

	writer := newFakeActivationFileWriter()
	writer.writeErr["/repo/CLAUDE.md"] = errors.New("simulated EACCES on host context file")

	planner := newPlannerWithWriter(writer)
	plan := planner.Plan(planClaudeFixture(importStubActivationCriteria{}))
	result, err := planner.Apply(plan)
	if err == nil {
		t.Fatalf("Apply err = nil, want permission failure on host write")
	}
	if !strings.Contains(err.Error(), "simulated EACCES on host context file") {
		t.Fatalf("Apply err = %v, want wrapped host permission failure", err)
	}
	if len(writer.writes) != 2 {
		t.Fatalf("writes = %d, want 2 (external succeeded, host attempted)", len(writer.writes))
	}
	if got, ok := writer.files["/repo/.traceary/memories/claude.md"]; !ok {
		t.Fatalf("external memory file should have been written before host failure, got files=%v", writer.files)
	} else if got == "" {
		t.Fatalf("external memory file content empty after partial apply")
	}
	if _, present := writer.files["/repo/CLAUDE.md"]; present {
		t.Fatalf("host context file must not be persisted on permission failure")
	}
	if result.ExternalMemory.Action != apptypes.MemoryActivationApplyCreated {
		t.Fatalf("result.ExternalMemory.Action = %q, want created because external write succeeded", result.ExternalMemory.Action)
	}
	if result.ExternalMemory.Status != apptypes.MemoryActivationStatusInSync {
		t.Fatalf("result.ExternalMemory.Status = %q, want in_sync because external write succeeded", result.ExternalMemory.Status)
	}
	if result.HostContext.Action != "" {
		t.Fatalf("result.HostContext.Action = %q, want empty because host write failed", result.HostContext.Action)
	}
}
