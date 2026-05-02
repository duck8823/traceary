package usecase

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
)

const codexActivationFileName = "traceary.md"

type memoryActivationUsecase struct {
	memoryQuery queryservice.MemoryQueryService
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

	existing, exists, err := readExistingActivationTarget(targetPath)
	if err != nil {
		return apptypes.MemoryActivationPlan{}, err
	}
	planned, _, err := mergeActivationTarget(existing, exists, exportResult.Markdown)
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

	existing, exists, err := readExistingActivationTarget(targetPath)
	if err != nil {
		return apptypes.MemoryActivationApplyResult{}, err
	}
	planned, action, err := mergeActivationTarget(existing, exists, exportResult.Markdown)
	if err != nil {
		return apptypes.MemoryActivationApplyResult{}, err
	}
	if action != apptypes.MemoryActivationApplyNoop {
		if err := writeActivationTargetAtomic(targetPath, planned, exists); err != nil {
			return apptypes.MemoryActivationApplyResult{}, err
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

	if _, _, err := inspectActivationTargetForWrite(targetPath); err != nil {
		result.State = apptypes.MemoryActivationStatusInvalid
		result.Message = err.Error()
		return result, nil
	}

	existing, exists, err := readExistingActivationTarget(targetPath)
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
	region, found, err := findMemoryBridgeBlockRegion(existing)
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

func resolveMemoryActivationTargetPath(criteria apptypes.MemoryActivationCriteria) (string, error) {
	if _, ok := apptypes.MemoryBridgeTargetOf(criteria.Target.String()); !ok {
		return "", xerrors.Errorf("unsupported memory activation target: %s", criteria.Target)
	}
	if criteria.Target != apptypes.MemoryBridgeTargetCodex {
		return "", xerrors.Errorf("memory activation target %s is not supported yet", criteria.Target)
	}
	if trimmed := strings.TrimSpace(criteria.Path); trimmed != "" {
		abs, err := filepath.Abs(trimmed)
		if err != nil {
			return "", xerrors.Errorf("failed to resolve activation path: %w", err)
		}
		return abs, nil
	}
	switch criteria.Target {
	case apptypes.MemoryBridgeTargetCodex:
		root := strings.TrimSpace(criteria.Root)
		if root == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", xerrors.Errorf("failed to resolve user home directory: %w", err)
			}
			root = filepath.Join(home, ".codex", "memories")
		}
		absRoot, err := filepath.Abs(root)
		if err != nil {
			return "", xerrors.Errorf("failed to resolve codex memory root: %w", err)
		}
		return filepath.Join(absRoot, codexActivationFileName), nil
	}
	return "", xerrors.Errorf("memory activation target %s is not supported yet", criteria.Target)
}

func readExistingActivationTarget(path string) (string, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, xerrors.Errorf("failed to read activation target %s: %w", path, err)
	}
	return string(data), true, nil
}

func mergeActivationTarget(existing string, exists bool, managedBlock string) (string, apptypes.MemoryActivationApplyAction, error) {
	if !exists {
		return managedBlock, apptypes.MemoryActivationApplyCreated, nil
	}
	region, found, err := findMemoryBridgeBlockRegion(existing)
	if err != nil {
		return "", "", err
	}
	if found {
		merged := existing[:region.start] + managedBlock + existing[region.end:]
		if merged == existing {
			return existing, apptypes.MemoryActivationApplyNoop, nil
		}
		return merged, apptypes.MemoryActivationApplyUpdated, nil
	}
	merged := appendManagedBlock(existing, managedBlock)
	if merged == existing {
		return existing, apptypes.MemoryActivationApplyNoop, nil
	}
	return merged, apptypes.MemoryActivationApplyUpdated, nil
}

type memoryBridgeBlockRegion struct {
	start int
	end   int
}

func findMemoryBridgeBlockRegion(content string) (memoryBridgeBlockRegion, bool, error) {
	offset := 0
	beginStart := -1
	endOffset := -1
	for _, line := range splitContentLines(content) {
		trimmed := strings.TrimSpace(line.text)
		if version, ok := MatchMemoryBridgeBeginLine(trimmed); ok {
			if version > MemoryBridgeCurrentVersion {
				return memoryBridgeBlockRegion{}, false, xerrors.Errorf("refusing to overwrite newer Traceary managed block version v%d (current v%d)", version, MemoryBridgeCurrentVersion)
			}
			if beginStart >= 0 {
				return memoryBridgeBlockRegion{}, false, xerrors.Errorf("multiple Traceary managed memory blocks found")
			}
			beginStart = offset
		}
		if trimmed == MemoryBridgeMarkerEnd {
			if beginStart < 0 {
				offset += len(line.raw)
				continue
			}
			if endOffset >= 0 {
				return memoryBridgeBlockRegion{}, false, xerrors.Errorf("multiple Traceary managed memory end markers found")
			}
			endOffset = offset + len(line.raw)
		}
		offset += len(line.raw)
	}
	if beginStart < 0 {
		return memoryBridgeBlockRegion{}, false, nil
	}
	if endOffset < 0 {
		return memoryBridgeBlockRegion{}, false, xerrors.Errorf("Traceary managed memory begin marker found without end marker")
	}
	return memoryBridgeBlockRegion{start: beginStart, end: endOffset}, true, nil
}

type contentLine struct {
	raw  string
	text string
}

func splitContentLines(content string) []contentLine {
	if content == "" {
		return nil
	}
	lines := make([]contentLine, 0, strings.Count(content, "\n")+1)
	for len(content) > 0 {
		next := strings.IndexByte(content, '\n')
		if next < 0 {
			lines = append(lines, contentLine{raw: content, text: strings.TrimSuffix(content, "\r")})
			break
		}
		raw := content[:next+1]
		text := strings.TrimSuffix(strings.TrimSuffix(raw, "\n"), "\r")
		lines = append(lines, contentLine{raw: raw, text: text})
		content = content[next+1:]
	}
	return lines
}

func appendManagedBlock(existing string, managedBlock string) string {
	if existing == "" {
		return managedBlock
	}
	if !strings.HasSuffix(existing, "\n") {
		return existing + "\n\n" + managedBlock
	}
	if !strings.HasSuffix(existing, "\n\n") {
		return existing + "\n" + managedBlock
	}
	return existing + managedBlock
}

func writeActivationTargetAtomic(path string, content string, existed bool) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return xerrors.Errorf("failed to create activation target directory %s: %w", dir, err)
	}
	perm := os.FileMode(0o600)
	info, statExists, err := inspectActivationTargetForWrite(path)
	if err != nil {
		return err
	}
	if statExists {
		if mode := info.Mode().Perm(); mode != 0 {
			perm = mode
		}
	}
	tmp, err := os.CreateTemp(dir, ".traceary-"+filepath.Base(path)+".*.tmp")
	if err != nil {
		return xerrors.Errorf("failed to create temporary activation target in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return xerrors.Errorf("failed to chmod temporary activation target %s: %w", tmpPath, err)
	}
	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return xerrors.Errorf("failed to write temporary activation target %s: %w", tmpPath, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return xerrors.Errorf("failed to sync temporary activation target %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		return xerrors.Errorf("failed to close temporary activation target %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return xerrors.Errorf("failed to replace activation target %s: %w", path, err)
	}
	cleanup = false
	return nil
}

func inspectActivationTargetForWrite(path string) (os.FileInfo, bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, xerrors.Errorf("failed to stat activation target %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, true, xerrors.Errorf("activation target symlinks are not supported: %s", path)
	}
	if info.IsDir() {
		return nil, true, xerrors.Errorf("activation target is a directory: %s", path)
	}
	return info, true, nil
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
