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
	targetPath, err := resolveMemoryActivationTargetPath(criteria)
	if err != nil {
		return apptypes.MemoryActivationPlan{}, err
	}

	exportResult, err := u.renderActivationBlock(ctx, criteria)
	if err != nil {
		return apptypes.MemoryActivationPlan{}, err
	}

	writer := u.writer()
	existing, exists, err := writer.ReadIfExists(targetPath)
	if err != nil {
		return apptypes.MemoryActivationPlan{}, xerrors.Errorf("failed to load activation target for plan: %w", err)
	}
	planned, _, err := memoryBridgeBlockMarkers.replaceOrAppend(existing, exists, exportResult.Markdown)
	if err != nil {
		return apptypes.MemoryActivationPlan{}, err
	}
	diff := ""
	if criteria.Diff && exists && existing != planned {
		diff = renderActivationDiff(targetPath, existing, planned)
	}

	return apptypes.MemoryActivationPlan{
		Target:         criteria.Target,
		TargetPath:     targetPath,
		Scopes:         exportResult.Scopes,
		Markdown:       planned,
		Existing:       exists,
		Diff:           diff,
		ActivatedCount: exportResult.ExportedCount,
	}, nil
}

func (u *memoryActivationUsecase) Apply(ctx context.Context, criteria apptypes.MemoryActivationCriteria) (apptypes.MemoryActivationApplyResult, error) {
	targetPath, err := resolveMemoryActivationTargetPath(criteria)
	if err != nil {
		return apptypes.MemoryActivationApplyResult{}, err
	}

	exportResult, err := u.renderActivationBlock(ctx, criteria)
	if err != nil {
		return apptypes.MemoryActivationApplyResult{}, err
	}

	writer := u.writer()
	existing, exists, err := writer.ReadIfExists(targetPath)
	if err != nil {
		return apptypes.MemoryActivationApplyResult{}, xerrors.Errorf("failed to load activation target before apply: %w", err)
	}
	planned, action, err := memoryBridgeBlockMarkers.replaceOrAppend(existing, exists, exportResult.Markdown)
	if err != nil {
		return apptypes.MemoryActivationApplyResult{}, err
	}
	if action != apptypes.MemoryActivationApplyNoop {
		if err := writer.WriteAtomic(targetPath, planned); err != nil {
			return apptypes.MemoryActivationApplyResult{}, xerrors.Errorf("failed to apply activation target: %w", err)
		}
	}

	return apptypes.MemoryActivationApplyResult{
		Target:         criteria.Target,
		TargetPath:     targetPath,
		Scopes:         exportResult.Scopes,
		Action:         action,
		Existing:       exists,
		ActivatedCount: exportResult.ExportedCount,
	}, nil
}

func (u *memoryActivationUsecase) Status(ctx context.Context, criteria apptypes.MemoryActivationCriteria) (apptypes.MemoryActivationStatusResult, error) {
	targetPath, err := resolveMemoryActivationTargetPath(criteria)
	if err != nil {
		return apptypes.MemoryActivationStatusResult{}, err
	}

	exportResult, err := u.renderActivationBlock(ctx, criteria)
	if err != nil {
		return apptypes.MemoryActivationStatusResult{}, err
	}
	result := apptypes.MemoryActivationStatusResult{
		Target:         criteria.Target,
		TargetPath:     targetPath,
		Scopes:         exportResult.Scopes,
		ActivatedCount: exportResult.ExportedCount,
	}

	writer := u.writer()
	if _, _, err := writer.Inspect(targetPath); err != nil {
		result.State = apptypes.MemoryActivationStatusInvalid
		result.Message = err.Error()
		return result, nil
	}

	existing, exists, err := writer.ReadIfExists(targetPath)
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

func (u *memoryActivationUsecase) renderActivationBlock(ctx context.Context, criteria apptypes.MemoryActivationCriteria) (apptypes.MemoryExportResult, error) {
	return (&memoryExportUsecase{memoryQuery: u.memoryQuery}).Export(ctx, apptypes.MemoryExportCriteria{
		Target:        criteria.Target,
		Scopes:        criteria.Scopes,
		IncludeGlobal: criteria.IncludeGlobal,
	})
}

// resolveMemoryActivationTargetPath dispatches the criteria to the
// host-specific descriptor and returns the absolute file path the
// activation usecase will manage.
func resolveMemoryActivationTargetPath(criteria apptypes.MemoryActivationCriteria) (string, error) {
	target, err := resolveActivationTarget(criteria.Target)
	if err != nil {
		return "", err
	}
	path, err := target.ResolvePath(criteria)
	if err != nil {
		return "", xerrors.Errorf("failed to resolve %s activation target path: %w", criteria.Target, err)
	}
	return path, nil
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
