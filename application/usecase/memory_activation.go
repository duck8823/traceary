package usecase

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
)

type memoryActivationUsecase struct {
	memoryQuery queryservice.MemoryQueryService
	// fileWriter is the safe-write adapter for the activation target
	// file. The zero value uses osActivationFileWriter so callers that
	// build the usecase inline (memoryUsecase delegations and tests)
	// keep the v0.12 filesystem semantics without explicit wiring.
	fileWriter activationFileWriter
}

func (u *memoryActivationUsecase) writer() activationFileWriter {
	if u.fileWriter == nil {
		return osActivationFileWriter{}
	}
	return u.fileWriter
}

func (u *memoryActivationUsecase) Plan(ctx context.Context, criteria apptypes.MemoryActivationCriteria) (apptypes.MemoryActivationPlan, error) {
	resolution, err := resolveMemoryActivationTargetResolution(criteria)
	if err != nil {
		return apptypes.MemoryActivationPlan{}, err
	}

	exportResult, err := u.renderActivationBlock(ctx, criteria)
	if err != nil {
		return apptypes.MemoryActivationPlan{}, err
	}

	if resolution.IsTwoFile() {
		return u.planTwoFile(criteria, resolution, exportResult), nil
	}

	writer := u.writer()
	existing, exists, err := writer.ReadIfExists(resolution.HostContextPath)
	if err != nil {
		return apptypes.MemoryActivationPlan{}, xerrors.Errorf("failed to load activation target for plan: %w", err)
	}
	planned, _, err := memoryBridgeBlockMarkers.replaceOrAppend(existing, exists, exportResult.Markdown)
	if err != nil {
		return apptypes.MemoryActivationPlan{}, err
	}
	diff := ""
	if criteria.Diff && exists && existing != planned {
		diff = renderActivationDiff(resolution.HostContextPath, existing, planned)
	}

	return apptypes.MemoryActivationPlan{
		Target:         criteria.Target,
		TargetPath:     resolution.HostContextPath,
		Scopes:         exportResult.Scopes,
		Markdown:       planned,
		Existing:       exists,
		Diff:           diff,
		ActivatedCount: exportResult.ExportedCount,
	}, nil
}

func (u *memoryActivationUsecase) Apply(ctx context.Context, criteria apptypes.MemoryActivationCriteria) (apptypes.MemoryActivationApplyResult, error) {
	resolution, err := resolveMemoryActivationTargetResolution(criteria)
	if err != nil {
		return apptypes.MemoryActivationApplyResult{}, err
	}

	if resolution.IsTwoFile() {
		return apptypes.MemoryActivationApplyResult{}, xerrors.Errorf("memory activation --apply is not supported yet for target %s", criteria.Target)
	}

	exportResult, err := u.renderActivationBlock(ctx, criteria)
	if err != nil {
		return apptypes.MemoryActivationApplyResult{}, err
	}

	writer := u.writer()
	existing, exists, err := writer.ReadIfExists(resolution.HostContextPath)
	if err != nil {
		return apptypes.MemoryActivationApplyResult{}, xerrors.Errorf("failed to load activation target before apply: %w", err)
	}
	planned, action, err := memoryBridgeBlockMarkers.replaceOrAppend(existing, exists, exportResult.Markdown)
	if err != nil {
		return apptypes.MemoryActivationApplyResult{}, err
	}
	if action != apptypes.MemoryActivationApplyNoop {
		if err := writer.WriteAtomic(resolution.HostContextPath, planned); err != nil {
			return apptypes.MemoryActivationApplyResult{}, xerrors.Errorf("failed to apply activation target: %w", err)
		}
	}

	return apptypes.MemoryActivationApplyResult{
		Target:         criteria.Target,
		TargetPath:     resolution.HostContextPath,
		Scopes:         exportResult.Scopes,
		Action:         action,
		Existing:       exists,
		ActivatedCount: exportResult.ExportedCount,
	}, nil
}

func (u *memoryActivationUsecase) Status(ctx context.Context, criteria apptypes.MemoryActivationCriteria) (apptypes.MemoryActivationStatusResult, error) {
	resolution, err := resolveMemoryActivationTargetResolution(criteria)
	if err != nil {
		return apptypes.MemoryActivationStatusResult{}, err
	}

	exportResult, err := u.renderActivationBlock(ctx, criteria)
	if err != nil {
		return apptypes.MemoryActivationStatusResult{}, err
	}

	if resolution.IsTwoFile() {
		return u.statusTwoFile(criteria, resolution, exportResult), nil
	}

	result := apptypes.MemoryActivationStatusResult{
		Target:         criteria.Target,
		TargetPath:     resolution.HostContextPath,
		Scopes:         exportResult.Scopes,
		ActivatedCount: exportResult.ExportedCount,
	}

	writer := u.writer()
	if _, _, err := writer.Inspect(resolution.HostContextPath); err != nil {
		result.State = apptypes.MemoryActivationStatusInvalid
		result.Message = err.Error()
		return result, nil
	}

	existing, exists, err := writer.ReadIfExists(resolution.HostContextPath)
	if err != nil {
		result.State = apptypes.MemoryActivationStatusInvalid
		result.Message = err.Error()
		return result, nil
	}
	result.Existing = exists
	if !exists {
		result.State = apptypes.MemoryActivationStatusMissing
		result.Message = "activation target file is missing"
		return result, nil
	}
	region, found, err := memoryBridgeBlockMarkers.findRegion(existing)
	if err != nil {
		result.State = apptypes.MemoryActivationStatusInvalid
		result.Message = err.Error()
		return result, nil
	}
	if !found {
		result.State = apptypes.MemoryActivationStatusMissing
		result.Message = "Traceary managed memory block is missing"
		return result, nil
	}
	if existing[region.start:region.end] == exportResult.Markdown {
		result.State = apptypes.MemoryActivationStatusInSync
		result.Message = "activation target is in sync"
		return result, nil
	}
	result.State = apptypes.MemoryActivationStatusStale
	result.Message = "Traceary managed memory block differs from the current accepted memories"
	return result, nil
}

func (u *memoryActivationUsecase) planTwoFile(criteria apptypes.MemoryActivationCriteria, resolution activationTargetResolution, exportResult apptypes.MemoryExportResult) apptypes.MemoryActivationPlan {
	planner := &importStubActivationPlanner{fileWriter: u.fileWriter}
	pair := planner.Plan(importStubActivationCriteria{
		Target:             criteria.Target,
		HostContextPath:    resolution.HostContextPath,
		ExternalMemoryPath: resolution.ExternalMemoryPath,
		ImportPath:         resolution.ImportPath,
		ExternalMarkdown:   exportResult.Markdown,
		Scopes:             exportResult.Scopes,
		ActivatedCount:     exportResult.ExportedCount,
		Diff:               criteria.Diff,
	})
	host := componentFromPlan(pair.HostContext)
	external := componentFromPlan(pair.ExternalMemory)
	plan := apptypes.MemoryActivationPlan{
		Target:         criteria.Target,
		TargetPath:     resolution.HostContextPath,
		Scopes:         exportResult.Scopes,
		Markdown:       pair.HostContext.Markdown,
		Existing:       pair.HostContext.Existing,
		Diff:           strings.Join(pair.orderedDiffs(), "\n"),
		ActivatedCount: exportResult.ExportedCount,
		HostContext:    &host,
		ExternalMemory: &external,
	}
	return plan
}

func (u *memoryActivationUsecase) statusTwoFile(criteria apptypes.MemoryActivationCriteria, resolution activationTargetResolution, exportResult apptypes.MemoryExportResult) apptypes.MemoryActivationStatusResult {
	planner := &importStubActivationPlanner{fileWriter: u.fileWriter}
	pair := planner.Plan(importStubActivationCriteria{
		Target:             criteria.Target,
		HostContextPath:    resolution.HostContextPath,
		ExternalMemoryPath: resolution.ExternalMemoryPath,
		ImportPath:         resolution.ImportPath,
		ExternalMarkdown:   exportResult.Markdown,
		Scopes:             exportResult.Scopes,
		ActivatedCount:     exportResult.ExportedCount,
	})
	host := componentFromPlan(pair.HostContext)
	external := componentFromPlan(pair.ExternalMemory)
	state := aggregateTwoFileStatus(host.State, external.State)
	return apptypes.MemoryActivationStatusResult{
		Target:         criteria.Target,
		TargetPath:     resolution.HostContextPath,
		Scopes:         exportResult.Scopes,
		State:          state,
		Existing:       pair.HostContext.Existing,
		ActivatedCount: exportResult.ExportedCount,
		Message:        statusMessageForPair(state, host, external),
		HostContext:    &host,
		ExternalMemory: &external,
	}
}

// componentFromPlan maps the planner's per-file result onto the public
// MemoryActivationComponent type so callers outside the usecase package
// can consume the per-file diff and state without reaching into the
// internal planner type.
func componentFromPlan(plan activationComponentPlan) apptypes.MemoryActivationComponent {
	return apptypes.MemoryActivationComponent{
		Path:     plan.Path,
		Existing: plan.Existing,
		Markdown: plan.Markdown,
		Diff:     plan.Diff,
		Action:   plan.Action,
		State:    plan.Status,
		Message:  plan.Message,
	}
}

// aggregateTwoFileStatus collapses the per-file states into the pair
// state documented in the v0.13 ADR. The priority is invalid → missing
// → stale → in_sync; any in-sync value short-circuits only when both
// components are in sync.
func aggregateTwoFileStatus(host apptypes.MemoryActivationStatusState, external apptypes.MemoryActivationStatusState) apptypes.MemoryActivationStatusState {
	if host == apptypes.MemoryActivationStatusInvalid || external == apptypes.MemoryActivationStatusInvalid {
		return apptypes.MemoryActivationStatusInvalid
	}
	if host == apptypes.MemoryActivationStatusMissing || external == apptypes.MemoryActivationStatusMissing {
		return apptypes.MemoryActivationStatusMissing
	}
	if host == apptypes.MemoryActivationStatusStale || external == apptypes.MemoryActivationStatusStale {
		return apptypes.MemoryActivationStatusStale
	}
	return apptypes.MemoryActivationStatusInSync
}

func statusMessageForPair(state apptypes.MemoryActivationStatusState, host, external apptypes.MemoryActivationComponent) string {
	switch state {
	case apptypes.MemoryActivationStatusInSync:
		return "activation pair is in sync"
	case apptypes.MemoryActivationStatusInvalid:
		if external.Message != "" && external.State == apptypes.MemoryActivationStatusInvalid {
			return fmt.Sprintf("external memory file invalid: %s", external.Message)
		}
		if host.Message != "" {
			return fmt.Sprintf("host context file invalid: %s", host.Message)
		}
		return "activation pair is invalid"
	case apptypes.MemoryActivationStatusMissing:
		switch {
		case host.State == apptypes.MemoryActivationStatusMissing && external.State == apptypes.MemoryActivationStatusMissing:
			return "host context import stub and external memory file are missing"
		case host.State == apptypes.MemoryActivationStatusMissing:
			return "host context import stub is missing"
		case external.State == apptypes.MemoryActivationStatusMissing:
			return "external memory file is missing"
		default:
			return "activation pair is missing"
		}
	case apptypes.MemoryActivationStatusStale:
		switch {
		case host.State == apptypes.MemoryActivationStatusStale && external.State == apptypes.MemoryActivationStatusStale:
			return "host context import stub and external memory file are stale"
		case host.State == apptypes.MemoryActivationStatusStale:
			return "host context import stub is stale"
		case external.State == apptypes.MemoryActivationStatusStale:
			return "external memory file is stale"
		default:
			return "activation pair is stale"
		}
	}
	return ""
}

func (u *memoryActivationUsecase) renderActivationBlock(ctx context.Context, criteria apptypes.MemoryActivationCriteria) (apptypes.MemoryExportResult, error) {
	return (&memoryExportUsecase{memoryQuery: u.memoryQuery}).Export(ctx, apptypes.MemoryExportCriteria{
		Target:        criteria.Target,
		Scopes:        criteria.Scopes,
		IncludeGlobal: criteria.IncludeGlobal,
	})
}

// resolveMemoryActivationTargetResolution dispatches the criteria to the
// host-specific descriptor and returns the resolved file path(s). For
// single-file targets only HostContextPath is populated; two-file
// targets fill ExternalMemoryPath and ImportPath as well.
func resolveMemoryActivationTargetResolution(criteria apptypes.MemoryActivationCriteria) (activationTargetResolution, error) {
	target, err := resolveActivationTarget(criteria.Target)
	if err != nil {
		return activationTargetResolution{}, err
	}
	resolution, err := target.Resolve(criteria)
	if err != nil {
		return activationTargetResolution{}, xerrors.Errorf("failed to resolve %s activation target: %w", criteria.Target, err)
	}
	return resolution, nil
}

func renderActivationDiff(path string, existing string, planned string) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "--- %s\n", path)
	fmt.Fprintf(&builder, "+++ %s (planned)\n", path)
	for _, line := range splitDiffLines(existing) {
		fmt.Fprintf(&builder, "-%s\n", line)
	}
	for _, line := range splitDiffLines(planned) {
		fmt.Fprintf(&builder, "+%s\n", line)
	}
	return builder.String()
}

func splitDiffLines(value string) []string {
	trimmed := strings.TrimSuffix(value, "\n")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}
