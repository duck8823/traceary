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

// claudeHostContextFileName is the canonical CLAUDE.md filename Claude
// Code loads at session start. Traceary writes the import stub region
// inside this file when --target claude is applied.
const claudeHostContextFileName = "CLAUDE.md"

// claudeExternalMemoryRelDir / claudeExternalMemoryFileName describe the
// hidden directory layout Traceary owns under the activation root.
// `<root>/.traceary/memories/claude.md` is the v0.13 default external
// memory file, and the rendered import line is the relative form
// `./.traceary/memories/claude.md` so Claude resolves it relative to
// the host CLAUDE.md.
const (
	claudeExternalMemoryRelDir   = ".traceary/memories"
	claudeExternalMemoryFileName = "claude.md"
)

// geminiHostContextFileName is the canonical GEMINI.md filename Gemini
// CLI loads as hierarchical context. Traceary writes the import stub
// region inside this file when --target gemini is applied. The
// status/dry-run path renders the stub without mutating disk.
const geminiHostContextFileName = "GEMINI.md"

// geminiExternalMemoryRelDir / geminiExternalMemoryFileName describe
// the hidden directory layout Traceary owns under the activation root
// for Gemini. `<root>/.traceary/memories/gemini.md` is the v0.13
// default external memory file, and the rendered import line is the
// relative form `./.traceary/memories/gemini.md` so Gemini resolves it
// relative to the host GEMINI.md per its Memory Import Processor docs.
const (
	geminiExternalMemoryRelDir   = ".traceary/memories"
	geminiExternalMemoryFileName = "gemini.md"
)

// activationTargetResolution describes the file paths Traceary will
// inspect, read, or write for one activation criteria. Single-file
// targets (Codex) populate only HostContextPath. Two-file targets
// (Claude/Gemini) populate ExternalMemoryPath and ImportPath as well so
// the usecase can drive the v0.13 import-stub planner without branching
// on the target name.
type activationTargetResolution struct {
	// HostContextPath is the absolute path to the file Traceary owns the
	// managed region inside (single-file: the activation file itself;
	// two-file: the host context file such as CLAUDE.md).
	HostContextPath string
	// ExternalMemoryPath is the absolute path to the external memory
	// file that holds the rendered accepted memories. Empty for
	// single-file targets.
	ExternalMemoryPath string
	// ImportPath is the literal value rendered after `@` inside the
	// host context import stub. Empty for single-file targets.
	ImportPath string
}

// IsTwoFile reports whether the resolution describes a host-context +
// external-memory pair (Claude / Gemini) rather than a single Traceary
// managed file (Codex).
func (r activationTargetResolution) IsTwoFile() bool {
	return strings.TrimSpace(r.ExternalMemoryPath) != ""
}

// activationTarget resolves the host file Traceary will manage for one
// activation criteria. v0.13.0-2 shipped the Codex single-file resolver;
// v0.13.0-4 adds the Claude two-file resolver for read-only
// status/dry-run/diff. The descriptor is host-agnostic so future
// targets (Gemini in #894) plug in without rewriting the usecase.
type activationTarget interface {
	// Target returns the host this descriptor activates.
	Target() apptypes.MemoryBridgeTarget
	// Resolve returns the file paths Traceary will manage for the
	// criteria. Errors come from path resolution (e.g. inability to
	// locate $HOME for the Codex default), never from disk inspection.
	Resolve(criteria apptypes.MemoryActivationCriteria) (activationTargetResolution, error)
}

type codexActivationTarget struct{}

// Target returns MemoryBridgeTargetCodex.
func (codexActivationTarget) Target() apptypes.MemoryBridgeTarget {
	return apptypes.MemoryBridgeTargetCodex
}

// Resolve returns the absolute file path Codex activation manages.
// `criteria.Path` (when non-empty) wins over both `criteria.Root` and
// the default; `criteria.Root` overrides the default; otherwise the
// path is `<HOME>/.codex/memories/traceary.md`.
func (codexActivationTarget) Resolve(criteria apptypes.MemoryActivationCriteria) (activationTargetResolution, error) {
	if trimmed := strings.TrimSpace(criteria.Path); trimmed != "" {
		abs, err := filepath.Abs(trimmed)
		if err != nil {
			return activationTargetResolution{}, xerrors.Errorf("failed to resolve activation path: %w", err)
		}
		return activationTargetResolution{HostContextPath: abs}, nil
	}
	root := strings.TrimSpace(criteria.Root)
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return activationTargetResolution{}, xerrors.Errorf("failed to resolve user home directory: %w", err)
		}
		root = filepath.Join(home, ".codex", "memories")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return activationTargetResolution{}, xerrors.Errorf("failed to resolve codex memory root: %w", err)
	}
	return activationTargetResolution{HostContextPath: filepath.Join(absRoot, codexActivationFileName)}, nil
}

// hostContextActivationConfig describes the canonical layout for a
// two-file host activation target (host context file + external memory
// file). Claude (CLAUDE.md / .traceary/memories/claude.md) and Gemini
// (GEMINI.md / .traceary/memories/gemini.md) share the same resolution
// algorithm and only differ in these constants, so the resolver is
// parameterised on this struct rather than duplicated per target.
type hostContextActivationConfig struct {
	// Target identifies the host activation target the config drives.
	// It is used purely for error messages so failures are
	// attributable to the right target.
	Target apptypes.MemoryBridgeTarget
	// HostFileName is the canonical instruction file the host loads at
	// session start (e.g. CLAUDE.md, GEMINI.md). Traceary writes the
	// import stub region inside this file.
	HostFileName string
	// ExternalMemoryRelDir is the directory under the activation root
	// that Traceary owns for this host's external memory file. The
	// path is rendered with forward slashes; the resolver converts it
	// to platform separators before joining.
	ExternalMemoryRelDir string
	// ExternalMemoryFileName is the file Traceary writes inside
	// ExternalMemoryRelDir to hold the rendered managed memory block.
	ExternalMemoryFileName string
}

// claudeActivationConfig is the v0.13 default layout for Claude Code.
var claudeActivationConfig = hostContextActivationConfig{
	Target:                 apptypes.MemoryBridgeTargetClaude,
	HostFileName:           claudeHostContextFileName,
	ExternalMemoryRelDir:   claudeExternalMemoryRelDir,
	ExternalMemoryFileName: claudeExternalMemoryFileName,
}

// geminiActivationConfig is the v0.13 default layout for Gemini CLI.
// The hidden `.traceary/memories/gemini.md` external file mirrors the
// Claude layout so Traceary's diff/status/apply story is identical for
// both hosts. The host instruction file is GEMINI.md per the Gemini
// CLI memory documentation; Traceary never modifies the
// `## Gemini Added Memories` section produced by `save_memory`.
var geminiActivationConfig = hostContextActivationConfig{
	Target:                 apptypes.MemoryBridgeTargetGemini,
	HostFileName:           geminiHostContextFileName,
	ExternalMemoryRelDir:   geminiExternalMemoryRelDir,
	ExternalMemoryFileName: geminiExternalMemoryFileName,
}

type claudeActivationTarget struct{}

// Target returns MemoryBridgeTargetClaude.
func (claudeActivationTarget) Target() apptypes.MemoryBridgeTarget {
	return apptypes.MemoryBridgeTargetClaude
}

// Resolve returns the host context (CLAUDE.md) and external memory
// file Traceary will manage for Claude activation. Path / Root /
// default-detection precedence follows the v0.13 ADR:
//
//   - --path picks the host context file; the external memory path is
//     derived as `<dir of path>/.traceary/memories/claude.md`.
//   - --root picks the activation root; the host context file is
//     `<root>/CLAUDE.md` and the external file is
//     `<root>/.traceary/memories/claude.md`.
//   - Otherwise the activation root is the nearest ancestor of the
//     command working directory that contains `.git`, falling back to
//     the working directory when no `.git` is found.
//
// The import path is always rendered relative to the host context file
// so the resulting `@./.traceary/memories/claude.md` expression matches
// the documented Claude import format. When the host context file and
// external memory file do not share a directory tree (a future custom
// override), the import path falls back to the absolute external path.
func (claudeActivationTarget) Resolve(criteria apptypes.MemoryActivationCriteria) (activationTargetResolution, error) {
	return resolveHostContextActivation(criteria, claudeActivationConfig)
}

type geminiActivationTarget struct{}

// Target returns MemoryBridgeTargetGemini.
func (geminiActivationTarget) Target() apptypes.MemoryBridgeTarget {
	return apptypes.MemoryBridgeTargetGemini
}

// Resolve returns the host context (GEMINI.md) and external memory
// file Traceary will manage for Gemini activation. The resolution
// rules and import-path semantics are identical to Claude — Path beats
// Root beats nearest-`.git` ancestor with cwd fallback — so Gemini
// reuses the shared host-context resolver. The rendered import line is
// `@./.traceary/memories/gemini.md`, matching the v0.13 ADR contract
// and the Gemini CLI Memory Import Processor's documented relative
// path resolution.
//
// Traceary never writes into Gemini's `## Gemini Added Memories`
// section regardless of mode — the import stub lives outside that
// section because the planner's safe append rule only attaches the
// managed region at end-of-file, after the user-authored content.
func (geminiActivationTarget) Resolve(criteria apptypes.MemoryActivationCriteria) (activationTargetResolution, error) {
	return resolveHostContextActivation(criteria, geminiActivationConfig)
}

// resolveHostContextActivation runs the shared two-file path
// resolution algorithm for one host config. The function is the only
// caller that knows the activation root precedence (Path > Root >
// nearest-`.git` ancestor > cwd) so adding a third host (or
// renaming a path) does not require touching the per-target Resolve
// methods.
func resolveHostContextActivation(criteria apptypes.MemoryActivationCriteria, config hostContextActivationConfig) (activationTargetResolution, error) {
	hostPath, err := resolveHostContextPath(criteria, config)
	if err != nil {
		return activationTargetResolution{}, err
	}
	externalMemoryPath := hostExternalMemoryPath(hostPath, config)
	importPath, err := hostImportPath(hostPath, externalMemoryPath, config)
	if err != nil {
		return activationTargetResolution{}, err
	}
	return activationTargetResolution{
		HostContextPath:    hostPath,
		ExternalMemoryPath: externalMemoryPath,
		ImportPath:         importPath,
	}, nil
}

func resolveHostContextPath(criteria apptypes.MemoryActivationCriteria, config hostContextActivationConfig) (string, error) {
	if trimmed := strings.TrimSpace(criteria.Path); trimmed != "" {
		abs, err := filepath.Abs(trimmed)
		if err != nil {
			return "", xerrors.Errorf("failed to resolve %s host context path: %w", config.Target, err)
		}
		return abs, nil
	}
	if trimmed := strings.TrimSpace(criteria.Root); trimmed != "" {
		absRoot, err := filepath.Abs(trimmed)
		if err != nil {
			return "", xerrors.Errorf("failed to resolve %s activation root: %w", config.Target, err)
		}
		return filepath.Join(absRoot, config.HostFileName), nil
	}
	root, err := detectHostActivationRoot(config.Target)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, config.HostFileName), nil
}

func hostExternalMemoryPath(hostContextPath string, config hostContextActivationConfig) string {
	hostDir := filepath.Dir(hostContextPath)
	return filepath.Join(hostDir, filepath.FromSlash(config.ExternalMemoryRelDir), config.ExternalMemoryFileName)
}

// hostImportPath renders the literal text Traceary writes after `@`
// inside the host context import stub. Both Claude and Gemini resolve
// relative imports against the file containing the import, so the
// renderer always prefers the relative form when the external file
// lives below the host context directory. When a future override puts
// the external file outside that subtree, the function returns the
// absolute path so the generated stub still resolves cleanly.
func hostImportPath(hostContextPath, externalMemoryPath string, config hostContextActivationConfig) (string, error) {
	hostDir := filepath.Dir(hostContextPath)
	rel, err := filepath.Rel(hostDir, externalMemoryPath)
	if err != nil {
		return "", xerrors.Errorf("failed to compute relative %s import path: %w", config.Target, err)
	}
	relSlash := filepath.ToSlash(rel)
	if strings.HasPrefix(relSlash, "../") || relSlash == ".." {
		return externalMemoryPath, nil
	}
	if strings.HasPrefix(relSlash, "./") || relSlash == "." {
		return relSlash, nil
	}
	return "./" + relSlash, nil
}

// detectHostActivationRoot walks the command working directory
// upwards searching for the nearest ancestor that contains a `.git`
// entry. When no `.git` is found, the current working directory is
// returned per the v0.13 ADR. Symlink and stat errors propagate so a
// broken filesystem cannot silently move the activation root. The
// target name is used purely so error messages name the right host.
func detectHostActivationRoot(target apptypes.MemoryBridgeTarget) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", xerrors.Errorf("failed to resolve %s activation working directory: %w", target, err)
	}
	current, err := filepath.Abs(cwd)
	if err != nil {
		return "", xerrors.Errorf("failed to resolve %s activation absolute working directory: %w", target, err)
	}
	for {
		gitPath := filepath.Join(current, ".git")
		if _, statErr := os.Lstat(gitPath); statErr == nil {
			return current, nil
		} else if !os.IsNotExist(statErr) {
			return "", xerrors.Errorf("failed to inspect git root candidate %s: %w", gitPath, statErr)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return cwd, nil
		}
		current = parent
	}
}

// resolveActivationTarget dispatches a MemoryBridgeTarget to its
// host-specific descriptor. Targets reserved by the contract but not
// yet wired through the activation usecase return a "not supported
// yet" error so the CLI surface stays in lockstep with the
// implementation work tracked in the v0.13.0 milestone.
func resolveActivationTarget(target apptypes.MemoryBridgeTarget) (activationTarget, error) {
	if _, ok := apptypes.MemoryBridgeTargetOf(target.String()); !ok {
		return nil, xerrors.Errorf("unsupported memory activation target: %s", target)
	}
	switch target {
	case apptypes.MemoryBridgeTargetCodex:
		return codexActivationTarget{}, nil
	case apptypes.MemoryBridgeTargetClaude:
		return claudeActivationTarget{}, nil
	case apptypes.MemoryBridgeTargetGemini:
		return geminiActivationTarget{}, nil
	}
	return nil, xerrors.Errorf("memory activation target %s is not supported yet", target)
}
