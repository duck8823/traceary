package usecase

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
)

// codexActivationFileName is the canonical filename Traceary writes when
// it activates accepted memories into Codex's native memory directory.
// The default activation root is `~/.codex/memories`, so the resulting
// path is `~/.codex/memories/traceary.md`.
const codexActivationFileName = "traceary.md"

// activationTarget resolves the host file Traceary will manage for one
// activation criteria. v0.13.0-2 only ships the Codex single-file
// resolver. The descriptor is host-agnostic so v0.13.0-3+ can plug in
// two-file Claude / Gemini resolvers (host-context import stub +
// external memory file) without rewriting memoryActivationUsecase.
type activationTarget interface {
	// Target returns the host this descriptor activates.
	Target() apptypes.MemoryBridgeTarget
	// ResolvePath returns the absolute path of the file that will be
	// inspected, read, or written by the activation usecase.
	ResolvePath(criteria apptypes.MemoryActivationCriteria) (string, error)
}

type codexActivationTarget struct{}

// Target returns MemoryBridgeTargetCodex.
func (codexActivationTarget) Target() apptypes.MemoryBridgeTarget {
	return apptypes.MemoryBridgeTargetCodex
}

// ResolvePath returns the absolute file path Codex activation manages.
// `criteria.Path` (when non-empty) wins over both `criteria.Root` and the
// default; `criteria.Root` overrides the default; otherwise the path is
// `<HOME>/.codex/memories/traceary.md`.
func (codexActivationTarget) ResolvePath(criteria apptypes.MemoryActivationCriteria) (string, error) {
	if trimmed := strings.TrimSpace(criteria.Path); trimmed != "" {
		abs, err := filepath.Abs(trimmed)
		if err != nil {
			return "", xerrors.Errorf("failed to resolve activation path: %w", err)
		}
		return abs, nil
	}
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

// resolveActivationTarget dispatches a MemoryBridgeTarget to its
// host-specific descriptor. Targets that are reserved by the contract
// but not yet wired through the activation usecase return a
// "not supported yet" error so the CLI surface stays in lockstep with
// the implementation work tracked in the v0.13.0 milestone.
func resolveActivationTarget(target apptypes.MemoryBridgeTarget) (activationTarget, error) {
	if _, ok := apptypes.MemoryBridgeTargetOf(target.String()); !ok {
		return nil, xerrors.Errorf("unsupported memory activation target: %s", target)
	}
	switch target {
	case apptypes.MemoryBridgeTargetCodex:
		return codexActivationTarget{}, nil
	}
	return nil, xerrors.Errorf("memory activation target %s is not supported yet", target)
}
