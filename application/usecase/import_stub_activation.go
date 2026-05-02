package usecase

import (
	"slices"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

// importStubActivationCriteria carries the inputs the two-file planner
// needs to compute a managed import-stub activation plan. The two paths
// are typically anchored to the same activation root, but the planner
// itself does not enforce a layout — the host-specific resolver in
// #892/#894 picks where each file lives.
type importStubActivationCriteria struct {
	// Target identifies the host (claude/gemini). It is reserved for
	// stub warning text and downstream telemetry; the planner does not
	// branch on it.
	Target apptypes.MemoryBridgeTarget
	// HostContextPath is the absolute path to the host context file
	// (CLAUDE.md / GEMINI.md). The planner inspects, reads, and writes
	// only this file's managed stub region.
	HostContextPath string
	// ExternalMemoryPath is the absolute path to the Traceary-managed
	// external memory file (e.g. .traceary/memories/<host>.md).
	ExternalMemoryPath string
	// ImportPath is the literal value rendered after the `@` inside the
	// stub. Callers choose relative-vs-absolute encoding before passing
	// it in.
	ImportPath string
	// ExternalMarkdown is the rendered managed memory block that will
	// occupy the external file's managed region. The planner does not
	// re-render the export; the caller (memoryActivationUsecase) is
	// responsible for invoking memory_export.
	ExternalMarkdown string
	// Scopes / ActivatedCount are pass-through bookkeeping so the
	// planner output stays self-describing for callers that build the
	// final user-facing report.
	Scopes         []domtypes.MemoryScope
	ActivatedCount int
	// Diff requests per-component diff rendering. When true, the
	// planner populates activationComponentPlan.Diff for components
	// whose existing content differs from the planned content.
	Diff bool
}

// activationComponentPlan is the per-file plan inside a two-file
// activation pair. Each file carries its own status and action so
// callers can report the host context stub and the external memory file
// independently. Markdown is the planned full file content, ready for
// activationFileWriter.WriteAtomic.
type activationComponentPlan struct {
	Path     string
	Existing bool
	Markdown string
	Action   apptypes.MemoryActivationApplyAction
	Status   apptypes.MemoryActivationStatusState
	Diff     string
	Message  string
}

// importStubActivationPlan is the two-file activation plan emitted by
// importStubActivationPlanner.Plan. v0.13.0-3 keeps the type internal to
// the usecase package; #892/#894 wire it through resolveActivationTarget
// when adding --target claude / gemini.
//
// Callers rendering both diffs to operators should use orderedDiffs() to
// preserve the canonical apply order documented in the v0.13 ADR.
type importStubActivationPlan struct {
	Target         apptypes.MemoryBridgeTarget
	HostContext    activationComponentPlan
	ExternalMemory activationComponentPlan
	Scopes         []domtypes.MemoryScope
	ActivatedCount int
}

// orderedDiffs returns the per-component diffs in the canonical apply
// order: external memory file first, host context stub second. This
// mirrors the ADR's apply ordering so dry-run output and the eventual
// apply behave the same way.
func (p importStubActivationPlan) orderedDiffs() []string {
	out := make([]string, 0, 2)
	if p.ExternalMemory.Diff != "" {
		out = append(out, p.ExternalMemory.Diff)
	}
	if p.HostContext.Diff != "" {
		out = append(out, p.HostContext.Diff)
	}
	return out
}

// importStubActivationApplyResult mirrors importStubActivationPlan and
// carries the post-write per-component state. Each component still
// carries its observable Action so callers can produce a precise audit
// log even when one file was a noop.
type importStubActivationApplyResult struct {
	Target         apptypes.MemoryBridgeTarget
	HostContext    activationComponentPlan
	ExternalMemory activationComponentPlan
	Scopes         []domtypes.MemoryScope
	ActivatedCount int
}

// importStubActivationPlanner computes and applies two-file activation
// plans (host context stub + external memory file). It reuses the v0.12
// managed-block parser and activation file writer so symlink / directory
// / newer-version refusals stay consistent with single-file Codex
// activation.
type importStubActivationPlanner struct {
	// fileWriter is the safe-write adapter used for both files. The
	// zero value falls back to osActivationFileWriter so tests and the
	// future memoryActivationUsecase wiring keep the v0.12 filesystem
	// semantics without explicit configuration.
	fileWriter activationFileWriter
}

func (p *importStubActivationPlanner) writer() activationFileWriter {
	if p.fileWriter == nil {
		return osActivationFileWriter{}
	}
	return p.fileWriter
}

// Plan computes a read-only two-file activation plan. It does not
// mutate disk. Per-component Status absorbs marker / writer errors so
// callers see both component states even when only one file is unsafe.
func (p *importStubActivationPlanner) Plan(criteria importStubActivationCriteria) importStubActivationPlan {
	stub := renderImportStubBlock(criteria.Target, criteria.ImportPath)
	host := p.planComponent(criteria.HostContextPath, importStubBlockMarkers, stub, criteria.Diff)
	external := p.planComponent(criteria.ExternalMemoryPath, memoryBridgeBlockMarkers, criteria.ExternalMarkdown, criteria.Diff)
	return importStubActivationPlan{
		Target:         criteria.Target,
		HostContext:    host,
		ExternalMemory: external,
		Scopes:         slices.Clone(criteria.Scopes),
		ActivatedCount: criteria.ActivatedCount,
	}
}

// Apply writes the external memory file first and the host context stub
// second, matching the v0.13 ADR's apply ordering. Apply refuses to
// write when either component is Invalid; otherwise it skips writes for
// components whose Action is Noop. When the external write succeeds and
// the host write fails, the caller must rerun the plan; partial writes
// converge under the v0.12 managed-block contract because each managed
// region is written idempotently.
func (p *importStubActivationPlanner) Apply(plan importStubActivationPlan) (importStubActivationApplyResult, error) {
	if plan.ExternalMemory.Status == apptypes.MemoryActivationStatusInvalid {
		return importStubActivationApplyResult{}, xerrors.Errorf("refusing to apply invalid external memory file %s: %s", plan.ExternalMemory.Path, plan.ExternalMemory.Message)
	}
	if plan.HostContext.Status == apptypes.MemoryActivationStatusInvalid {
		return importStubActivationApplyResult{}, xerrors.Errorf("refusing to apply invalid host context stub %s: %s", plan.HostContext.Path, plan.HostContext.Message)
	}
	writer := p.writer()
	result := importStubActivationApplyResult{
		Target:         plan.Target,
		Scopes:         slices.Clone(plan.Scopes),
		ActivatedCount: plan.ActivatedCount,
	}
	if plan.ExternalMemory.Action != apptypes.MemoryActivationApplyNoop {
		if err := writer.WriteAtomic(plan.ExternalMemory.Path, plan.ExternalMemory.Markdown); err != nil {
			return result, xerrors.Errorf("failed to apply external memory file %s: %w", plan.ExternalMemory.Path, err)
		}
	}
	result.ExternalMemory = appliedActivationComponent(plan.ExternalMemory)
	if plan.HostContext.Action != apptypes.MemoryActivationApplyNoop {
		if err := writer.WriteAtomic(plan.HostContext.Path, plan.HostContext.Markdown); err != nil {
			return result, xerrors.Errorf("failed to apply host context stub %s: %w", plan.HostContext.Path, err)
		}
	}
	result.HostContext = appliedActivationComponent(plan.HostContext)
	return result, nil
}

func appliedActivationComponent(plan activationComponentPlan) activationComponentPlan {
	plan.Status = apptypes.MemoryActivationStatusInSync
	plan.Message = ""
	return plan
}

// planComponent computes the planned content / action / status for one
// file in a two-file activation pair. Errors from inspect / read /
// region parsing land in Status=invalid and Message so callers can
// surface them per-file without the whole plan failing.
func (p *importStubActivationPlanner) planComponent(path string, markers managedBlockMarkers, managedBlock string, withDiff bool) activationComponentPlan {
	plan := activationComponentPlan{Path: path}
	writer := p.writer()
	if _, _, err := writer.Inspect(path); err != nil {
		plan.Status = apptypes.MemoryActivationStatusInvalid
		plan.Message = err.Error()
		return plan
	}
	existing, exists, err := writer.ReadIfExists(path)
	if err != nil {
		plan.Status = apptypes.MemoryActivationStatusInvalid
		plan.Message = err.Error()
		return plan
	}
	plan.Existing = exists
	planned, action, mergeErr := markers.replaceOrAppend(existing, exists, managedBlock)
	if mergeErr != nil {
		plan.Status = apptypes.MemoryActivationStatusInvalid
		plan.Message = mergeErr.Error()
		return plan
	}
	plan.Markdown = planned
	plan.Action = action
	plan.Status = computeComponentStatus(existing, exists, managedBlock, markers)
	if withDiff && exists && existing != planned {
		plan.Diff = renderActivationDiff(path, existing, planned)
	}
	return plan
}

// computeComponentStatus turns the existing-file vs planned-block
// comparison into the four status values used by --status. It mirrors
// memoryActivationUsecase.Status for a single file so v0.13.0-3 ships
// identical wording for orphaned / unterminated regions.
func computeComponentStatus(existing string, exists bool, managedBlock string, markers managedBlockMarkers) apptypes.MemoryActivationStatusState {
	if !exists {
		return apptypes.MemoryActivationStatusMissing
	}
	region, found, err := markers.findRegion(existing)
	if err != nil {
		return apptypes.MemoryActivationStatusInvalid
	}
	if !found {
		return apptypes.MemoryActivationStatusMissing
	}
	if existing[region.start:region.end] == managedBlock {
		return apptypes.MemoryActivationStatusInSync
	}
	return apptypes.MemoryActivationStatusStale
}
