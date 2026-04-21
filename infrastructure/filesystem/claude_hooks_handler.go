package filesystem

import (
	"path/filepath"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// claudeBuiltinToolsMatcher is the regular expression Claude Code
// evaluates as the PostToolUse matcher for Traceary's coverage of
// built-in tools (the ones that do not match `Bash` or `mcp__.*`).
//
// Coverage set (v0.8-6b, refreshed 2026-Q2):
//   - file I/O: Read, NotebookRead, Edit, MultiEdit, Write, NotebookEdit
//   - search: Grep, Glob
//   - agent / task: Agent, Task, TodoWrite
//   - web: WebFetch, WebSearch
//   - control flow: ExitPlanMode
//
// The default is an exhaustive list rather than `.*` so Traceary does
// not accidentally tail operator-irrelevant tool categories (for
// example, future per-plugin tools). The preset expansion that opts
// into `.*` lives in `traceary hooks install --matcher` (#632), which
// preserves this default.
const claudeBuiltinToolsMatcher = "Read|NotebookRead|Edit|MultiEdit|Write|NotebookEdit|Grep|Glob|Agent|Task|TodoWrite|WebFetch|WebSearch|ExitPlanMode"

// ClaudeMatcherPreset selects which matcher set Traceary emits for
// Claude Code's PostToolUse / PostToolUseFailure hooks. `default`
// preserves the v0.8-6b coverage (Bash + mcp__.* + exhaustive built-in
// list). `minimal` drops the built-in list for operators who only
// want shell / MCP coverage. `all` replaces the built-in list with
// `.*` so every tool kind is captured (e.g. for discovery during
// adoption), at the cost of heavier tail / timeline volume.
type ClaudeMatcherPreset string

const (
	// ClaudeMatcherPresetDefault is the baseline matcher set for
	// Traceary's `hooks install` / packaged plugin output.
	ClaudeMatcherPresetDefault ClaudeMatcherPreset = "default"
	// ClaudeMatcherPresetMinimal drops the built-in tool matcher row
	// so only Bash and mcp__.* are audited.
	ClaudeMatcherPresetMinimal ClaudeMatcherPreset = "minimal"
	// ClaudeMatcherPresetAll replaces the built-in list with `.*`.
	// Intentional opt-in only — the default install stays exhaustive
	// so operators do not accidentally drown their tail stream.
	ClaudeMatcherPresetAll ClaudeMatcherPreset = "all"
)

// String lets flag parsers introspect the preset value.
func (p ClaudeMatcherPreset) String() string { return string(p) }

// IsValid returns true for supported preset values. Unknown and empty
// strings are invalid — callers should either leave the preset unset
// (Build handles the default internally) or pass a known value.
func (p ClaudeMatcherPreset) IsValid() bool {
	switch p {
	case ClaudeMatcherPresetDefault, ClaudeMatcherPresetMinimal, ClaudeMatcherPresetAll:
		return true
	}
	return false
}

// ClaudeHooksHandler installs Traceary hooks for the Claude Code client.
type ClaudeHooksHandler struct{}

// NewClaudeHooksHandler constructs a ClaudeHooksHandler.
func NewClaudeHooksHandler() *ClaudeHooksHandler {
	return &ClaudeHooksHandler{}
}

// Name returns the canonical client identifier.
func (h *ClaudeHooksHandler) Name() string { return "claude" }

// Build returns the Hooks aggregate Traceary installs for Claude Code
// using the `default` matcher preset. It is a thin wrapper around
// BuildWithMatcher that preserves the historical single-argument
// signature shared with the Codex and Gemini handlers.
func (h *ClaudeHooksHandler) Build(tracearyBin string) model.Hooks {
	return h.BuildWithMatcher(tracearyBin, ClaudeMatcherPresetDefault)
}

// BuildWithMatcher returns the hook set shaped by the requested
// matcher preset. An empty preset value falls back to the default —
// callers that want to reject unknown strings should validate via
// ClaudeMatcherPreset.IsValid before calling this method.
func (h *ClaudeHooksHandler) BuildWithMatcher(tracearyBin string, preset ClaudeMatcherPreset) model.Hooks {
	sessionStartCommand := newHookRuntimeCommand(tracearyBin, "hook", "session", "claude", "start")
	sessionEndCommand := newHookRuntimeCommand(tracearyBin, "hook", "session", "claude", "end")
	auditCommand := newHookRuntimeCommand(tracearyBin, "hook", "audit", "claude")
	compactCommand := newHookRuntimeCommand(tracearyBin, "hook", "compact", "claude", "post-compact")
	compactResumeCommand := newHookRuntimeCommand(tracearyBin, "hook", "compact", "claude", "session-start-compact")
	promptCommand := newHookRuntimeCommand(tracearyBin, "hook", "prompt", "claude")
	transcriptCommand := newHookRuntimeCommand(tracearyBin, "hook", "transcript", "claude")

	// Build the PostToolUse / PostToolUseFailure entries according to
	// the selected preset. Bash and mcp__.* always stay — they are
	// the core surfaces that made Traceary useful before built-in
	// tool capture existed.
	postToolUseEntries := []model.HookEntry{
		model.HookEntryOf(types.Some("Bash"), []model.HookCommand{
			model.HookCommandOf("traceary-audit", "command", auditCommand, types.None[int](), "", managedKeyOf("traceary-audit.sh", "claude")),
		}),
		model.HookEntryOf(types.Some("mcp__.*"), []model.HookCommand{
			model.HookCommandOf("traceary-audit", "command", auditCommand, types.None[int](), "", managedKeyOf("traceary-audit.sh", "claude")),
		}),
	}
	switch preset {
	case ClaudeMatcherPresetMinimal:
		// No third matcher row.
	case ClaudeMatcherPresetAll:
		postToolUseEntries = append(postToolUseEntries, model.HookEntryOf(types.Some(".*"), []model.HookCommand{
			model.HookCommandOf("traceary-audit", "command", auditCommand, types.None[int](), "", managedKeyOf("traceary-audit.sh", "claude")),
		}))
	default:
		// ClaudeMatcherPresetDefault or empty: keep the exhaustive
		// built-in list (v0.8-6b set).
		postToolUseEntries = append(postToolUseEntries, model.HookEntryOf(types.Some(claudeBuiltinToolsMatcher), []model.HookCommand{
			model.HookCommandOf("traceary-audit", "command", auditCommand, types.None[int](), "", managedKeyOf("traceary-audit.sh", "claude")),
		}))
	}
	postToolUseFailureEntries := make([]model.HookEntry, len(postToolUseEntries))
	copy(postToolUseFailureEntries, postToolUseEntries)

	eventOrder := []string{
		"SessionStart",
		"SessionEnd",
		"Stop",
		"PostToolUse",
		"PostToolUseFailure",
		"PostCompact",
		"UserPromptSubmit",
	}
	events := map[string][]model.HookEntry{
		"SessionStart": {
			model.HookEntryOf(types.Some("*"), []model.HookCommand{
				model.HookCommandOf("traceary-session-start", "command", sessionStartCommand, types.None[int](), "", managedKeyOf("traceary-session.sh", "claude", "start")),
			}),
			model.HookEntryOf(types.Some("compact"), []model.HookCommand{
				model.HookCommandOf("traceary-compact-session-start", "command", compactResumeCommand, types.None[int](), "", managedKeyOf("traceary-compact.sh", "claude", "session-start-compact")),
			}),
		},
		"SessionEnd": {
			model.HookEntryOf(types.Some("*"), []model.HookCommand{
				model.HookCommandOf("traceary-session-end", "command", sessionEndCommand, types.None[int](), "", managedKeyOf("traceary-session.sh", "claude", "end")),
			}),
		},
		"Stop": {
			model.HookEntryOf(types.Some("*"), []model.HookCommand{
				model.HookCommandOf("traceary-transcript", "command", transcriptCommand, types.None[int](), "", managedKeyOf("traceary-transcript.sh", "claude")),
			}),
		},
		"PostToolUse":        postToolUseEntries,
		"PostToolUseFailure": postToolUseFailureEntries,
		"PostCompact": {
			model.HookEntryOf(types.Some("*"), []model.HookCommand{
				model.HookCommandOf("traceary-compact-post-compact", "command", compactCommand, types.None[int](), "", managedKeyOf("traceary-compact.sh", "claude", "post-compact")),
			}),
		},
		"UserPromptSubmit": {
			model.HookEntryOf(types.Some("*"), []model.HookCommand{
				model.HookCommandOf("traceary-prompt", "command", promptCommand, types.None[int](), "", managedKeyOf("traceary-prompt.sh", "claude")),
			}),
		},
	}

	return model.HooksOf(eventOrder, events)
}

// DefaultInstallPath returns the standard Claude Code settings path for the
// given project directory.
func (h *ClaudeHooksHandler) DefaultInstallPath(projectDir string) (string, error) {
	return filepath.Join(projectDir, ".claude", "settings.json"), nil
}
