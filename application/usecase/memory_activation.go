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

	exportResult, err := (&memoryExportUsecase{memoryQuery: u.memoryQuery}).Export(ctx, apptypes.MemoryExportCriteria{
		Target:        criteria.Target,
		Scopes:        criteria.Scopes,
		IncludeGlobal: criteria.IncludeGlobal,
	})
	if err != nil {
		return apptypes.MemoryActivationPlan{}, err
	}

	existing, exists, err := readExistingActivationTarget(targetPath)
	if err != nil {
		return apptypes.MemoryActivationPlan{}, err
	}
	diff := ""
	if criteria.Diff && exists && existing != exportResult.Markdown {
		diff = renderActivationDiff(targetPath, existing, exportResult.Markdown)
	}

	return apptypes.MemoryActivationPlan{
		Target:     criteria.Target,
		TargetPath: targetPath,
		Scopes:     exportResult.Scopes,
		Markdown:   exportResult.Markdown,
		Existing:   exists,
		Diff:       diff,
	}, nil
}

func resolveMemoryActivationTargetPath(criteria apptypes.MemoryActivationCriteria) (string, error) {
	if _, ok := apptypes.MemoryBridgeTargetOf(criteria.Target.String()); !ok {
		return "", xerrors.Errorf("unsupported memory activation target: %s", criteria.Target)
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
	default:
		return "", xerrors.Errorf("memory activation target %s is not supported yet", criteria.Target)
	}
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
