package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/presentation"
)

// readPreset is the resolved view of either a built-in preset or a
// user-defined preset from config.json. Both variants flow through the same
// application pipeline so the CLI caller does not have to branch.
type readPreset struct {
	name    string
	fields  []readFieldID
	filters readPresetFilters
	source  readPresetSource
}

type readPresetSource int

const (
	readPresetSourceBuiltin readPresetSource = iota
	readPresetSourceUser
)

// readPresetFilters holds the filter keys a preset contributes. Zero value
// means "do not apply" so the explicit-CLI-flag path stays untouched when a
// preset does not declare a given filter.
type readPresetFilters struct {
	kind         string
	kindSet      bool
	failures     bool
	failuresSet  bool
	workspace    string
	workspaceSet bool
	sessionID    string
	sessionIDSet bool
	client       string
	clientSet    bool
	agent        string
	agentSet     bool
}

// builtinReadPresets returns the curated preset catalog every Traceary
// install ships. The returned map is fresh on every call so callers can
// safely mutate it when merging user-defined entries.
func builtinReadPresets() map[string]readPreset {
	return map[string]readPreset{
		"failures": {
			name: "failures",
			// exit_code is pushed up front so failing command rows carry
			// their status alongside the timestamp, which is the scanning
			// order most useful when filtering for failures.
			fields: []readFieldID{
				readFieldTS,
				readFieldKind,
				readFieldExitCode,
				readFieldSession,
				readFieldWorkspace,
				readFieldMessage,
			},
			filters: readPresetFilters{failures: true, failuresSet: true},
			source:  readPresetSourceBuiltin,
		},
		"prompts-only": {
			name:    "prompts-only",
			filters: readPresetFilters{kind: "prompt", kindSet: true},
			source:  readPresetSourceBuiltin,
		},
		"compact-summaries": {
			name:    "compact-summaries",
			filters: readPresetFilters{kind: "compact_summary", kindSet: true},
			source:  readPresetSourceBuiltin,
		},
	}
}

// supportedBuiltinPresetsLabel returns the comma-separated list of built-in
// preset names, used in error messages to help operators discover valid
// values without grepping through docs.
func supportedBuiltinPresetsLabel() string {
	presets := builtinReadPresets()
	names := make([]string, 0, len(presets))
	for name := range presets {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// resolveReadPreset merges the built-in catalog with user-defined presets,
// logs a collision warning via warnWriter (typically stderr) when a
// user-defined preset shadows a built-in name, validates the requested
// preset, and returns the resolved preset. The returned preset is zero-
// valued when name is empty (no preset requested).
func resolveReadPreset(name string, userDefined map[string]presentation.ReadPreset, warnWriter io.Writer) (readPreset, bool, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return readPreset{}, false, nil
	}

	merged := builtinReadPresets()

	// Warn for every user-defined preset that shadows a built-in name so
	// operators can discover the collision from either a collision-aware
	// command run or a doctor pass over the config. We collect first and
	// emit in a stable sorted order to keep the message deterministic.
	collisions := make([]string, 0)
	for presetName, user := range userDefined {
		if _, clash := merged[presetName]; clash {
			collisions = append(collisions, presetName)
		}
		converted, err := convertUserPreset(presetName, user)
		if err != nil {
			return readPreset{}, false, err
		}
		merged[presetName] = converted
	}
	if warnWriter != nil && len(collisions) > 0 {
		sort.Strings(collisions)
		for _, collided := range collisions {
			_, _ = fmt.Fprintf(
				warnWriter,
				Localize(
					"[WARN] user-defined preset %q shadows a built-in preset of the same name\n",
					"[WARN] ユーザー定義 preset %q が同名の built-in preset を上書きしています\n",
				),
				collided,
			)
		}
	}

	preset, ok := merged[trimmed]
	if !ok {
		return readPreset{}, false, xerrors.Errorf(
			Localize(
				"unknown preset %q (built-in presets: %s)",
				"preset %q が見つかりません (built-in: %s)",
			),
			trimmed,
			supportedBuiltinPresetsLabel(),
		)
	}
	return preset, true, nil
}

// convertUserPreset validates a user-defined preset against the closed
// field / kind registries shared with --fields and --kind.
func convertUserPreset(name string, user presentation.ReadPreset) (readPreset, error) {
	result := readPreset{
		name:   name,
		source: readPresetSourceUser,
	}

	if len(user.Fields) > 0 {
		fields, err := parseReadFields(user.Fields)
		if err != nil {
			return readPreset{}, xerrors.Errorf(
				Localize(
					"invalid fields in preset %q: %w",
					"preset %q の fields が不正です: %w",
				),
				name,
				err,
			)
		}
		result.fields = fields
	}

	filters := readPresetFilters{}
	if kind := strings.TrimSpace(user.Filters.Kind); kind != "" {
		if _, err := validateSearchKind(kind); err != nil {
			return readPreset{}, xerrors.Errorf(
				Localize(
					"invalid kind in preset %q: %w",
					"preset %q の kind が不正です: %w",
				),
				name,
				err,
			)
		}
		filters.kind = kind
		filters.kindSet = true
	}
	if user.Filters.Failures != nil {
		filters.failures = *user.Filters.Failures
		filters.failuresSet = true
	}
	if ws := strings.TrimSpace(user.Filters.Workspace); ws != "" {
		filters.workspace = ws
		filters.workspaceSet = true
	}
	if sid := strings.TrimSpace(user.Filters.SessionID); sid != "" {
		filters.sessionID = sid
		filters.sessionIDSet = true
	}
	if client := strings.TrimSpace(user.Filters.Client); client != "" {
		filters.client = client
		filters.clientSet = true
	}
	if agent := strings.TrimSpace(user.Filters.Agent); agent != "" {
		filters.agent = agent
		filters.agentSet = true
	}
	result.filters = filters
	return result, nil
}

// applyReadPresetToListInput fills filter fields on listCommandInput from
// the resolved preset, but only for fields the caller did not already set
// via an explicit CLI flag. Explicit flags always win.
func applyReadPresetToListInput(input *listCommandInput, preset readPreset) {
	if preset.filters.kindSet && !input.kindSet {
		input.kind = preset.filters.kind
	}
	if preset.filters.clientSet && !input.clientSet {
		input.client = preset.filters.client
	}
	if preset.filters.agentSet && !input.agentSet {
		input.agent = preset.filters.agent
	}
	if preset.filters.sessionIDSet && !input.sessionIDSet {
		input.sessionID = preset.filters.sessionID
	}
	if preset.filters.workspaceSet && !input.repoSet {
		input.repo = preset.filters.workspace
	}
	if preset.filters.failuresSet && !input.failuresOnlySet {
		input.failuresOnly = preset.filters.failures
	}
}

// applyReadPresetToSearchInput mirrors applyReadPresetToListInput for the
// search command. Keeping the two helpers separate avoids tying the two
// command inputs together just to share filter plumbing.
func applyReadPresetToSearchInput(input *searchCommandInput, preset readPreset) {
	if preset.filters.kindSet && !input.kindSet {
		input.kind = preset.filters.kind
	}
	if preset.filters.clientSet && !input.clientSet {
		input.client = preset.filters.client
	}
	if preset.filters.agentSet && !input.agentSet {
		input.agent = preset.filters.agent
	}
	if preset.filters.sessionIDSet && !input.sessionIDSet {
		input.sessionID = preset.filters.sessionID
	}
	if preset.filters.workspaceSet && !input.repoSet {
		input.repo = preset.filters.workspace
	}
	if preset.filters.failuresSet && !input.failuresOnlySet {
		input.failuresOnly = preset.filters.failures
	}
}

// applyReadPresetToTailInput mirrors the helpers above for tail.
func applyReadPresetToTailInput(input *tailCommandInput, preset readPreset) {
	if preset.filters.kindSet && !input.kindSet {
		input.kind = preset.filters.kind
	}
	if preset.filters.clientSet && !input.clientSet {
		input.client = preset.filters.client
	}
	if preset.filters.agentSet && !input.agentSet {
		input.agent = preset.filters.agent
	}
	if preset.filters.sessionIDSet && !input.sessionIDSet {
		input.sessionID = preset.filters.sessionID
	}
	if preset.filters.workspaceSet && !input.repoSet {
		input.repo = preset.filters.workspace
	}
	if preset.filters.failuresSet && !input.failuresOnlySet {
		input.failuresOnly = preset.filters.failures
	}
}

// readPresetsFlagUsage returns the shared --preset flag usage string for
// list / search / tail commands.
func readPresetsFlagUsage() string {
	return Localize(
		"apply a saved view preset (built-in: "+supportedBuiltinPresetsLabel()+"; user-defined entries in read.presets override built-in names)",
		"保存済みのビュー preset を適用する (built-in: "+supportedBuiltinPresetsLabel()+"; config.json の read.presets は同名の built-in を上書き)",
	)
}
