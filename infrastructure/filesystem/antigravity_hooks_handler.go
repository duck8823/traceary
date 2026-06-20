package filesystem

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
)

// antigravityHookTimeoutSeconds is the per-hook timeout written into the
// packaged Antigravity hooks.json. The Antigravity default is 30s; Traceary's
// runtime entrypoints are fail-soft and quick, so 10s keeps a slow disk from
// stalling the host loop while leaving ample headroom.
const antigravityHookTimeoutSeconds = 10

// antigravityManagedGroup is the top-level hooks.json key Traceary owns. The
// merge path replaces only this group and preserves every other group.
const antigravityManagedGroup = "traceary"

// AntigravityHooksHandler installs Traceary hooks for Antigravity (the
// Antigravity 2.0 app, the Antigravity IDE, and the `agy` CLI). Antigravity's
// hooks.json uses a top-level map of hook-group name to event configs that is
// incompatible with the `{"hooks": {...}}` document the other clients share, so
// this handler renders and merges its own document shape via the
// rawHookDocumentHandler interface the orchestrator dispatches to.
type AntigravityHooksHandler struct{}

// NewAntigravityHooksHandler constructs an AntigravityHooksHandler.
func NewAntigravityHooksHandler() *AntigravityHooksHandler {
	return &AntigravityHooksHandler{}
}

// Name returns the canonical client identifier.
func (h *AntigravityHooksHandler) Name() string { return "antigravity" }

// Build satisfies application.HooksClientHandler. Antigravity's document is
// rendered by renderDocument (the official shape is incompatible with the
// shared model.Hooks marshaller), so this returns an empty aggregate and is
// never used for Antigravity generation.
func (h *AntigravityHooksHandler) Build(_ string) model.Hooks {
	return model.HooksOf(nil, nil)
}

// DefaultInstallPath returns the workspace-level Antigravity hooks config path.
// Global installs target ~/.gemini/config/hooks.json and are routed through the
// CLI's --global resolver, not this method.
func (h *AntigravityHooksHandler) DefaultInstallPath(projectDir string) (string, error) {
	return filepath.Join(projectDir, ".agents", "hooks.json"), nil
}

// antigravityHookCommand mirrors a single Antigravity hook handler entry.
type antigravityHookCommand struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

// antigravityMatcherEntry is the `{matcher, hooks:[…]}` wrapper used by the
// PreToolUse / PostToolUse events.
type antigravityMatcherEntry struct {
	Matcher string                   `json:"matcher"`
	Hooks   []antigravityHookCommand `json:"hooks"`
}

// buildAntigravityGroup renders the ordered Traceary hook group for the given
// binary. PreInvocation / Stop list handlers directly (matcher ignored);
// PreToolUse / PostToolUse wrap handlers in a run_command matcher entry. The
// event keys are emitted in a deterministic order via orderedJSONObject.
func buildAntigravityGroup(tracearyBin string) (json.RawMessage, error) {
	preInvocation := newHookRuntimeCommand(tracearyBin, "hook", "antigravity", "pre-invocation")
	preToolUse := newHookRuntimeCommand(tracearyBin, "hook", "antigravity", "pre-tool-use")
	postToolUse := newHookRuntimeCommand(tracearyBin, "hook", "antigravity", "post-tool-use")
	stop := newHookRuntimeCommand(tracearyBin, "hook", "antigravity", "stop")

	directHandlers := func(command string) []antigravityHookCommand {
		return []antigravityHookCommand{{Type: "command", Command: command, Timeout: antigravityHookTimeoutSeconds}}
	}
	matcherHandlers := func(command string) []antigravityMatcherEntry {
		return []antigravityMatcherEntry{{
			Matcher: "run_command",
			Hooks:   directHandlers(command),
		}}
	}

	return marshalOrderedJSONObject([]orderedJSONField{
		{Key: "PreInvocation", Value: directHandlers(preInvocation)},
		{Key: "PreToolUse", Value: matcherHandlers(preToolUse)},
		{Key: "PostToolUse", Value: matcherHandlers(postToolUse)},
		{Key: "Stop", Value: directHandlers(stop)},
	})
}

// renderDocument renders a fresh Antigravity hooks.json containing only the
// Traceary group. It satisfies the package-internal rawHookDocumentHandler
// interface the orchestrator dispatches to.
func (h *AntigravityHooksHandler) renderDocument(tracearyBin string) ([]byte, error) {
	group, err := buildAntigravityGroup(tracearyBin)
	if err != nil {
		return nil, err
	}
	return marshalOrderedJSONObject([]orderedJSONField{
		{Key: antigravityManagedGroup, Value: group},
	})
}

// mergeDocument replaces the Traceary group in an existing Antigravity
// hooks.json while preserving every other top-level hook group verbatim and in
// its original document order. A nil or whitespace-only existing document
// renders a fresh file. A newly introduced Traceary group is appended after the
// existing groups. The diff reports per-event Added / Refreshed / Preserved /
// Removed for the Traceary group. It satisfies the package-internal
// rawHookDocumentHandler interface.
func (h *AntigravityHooksHandler) mergeDocument(existing []byte, tracearyBin string) ([]byte, hookMergeDiff, error) {
	group, err := buildAntigravityGroup(tracearyBin)
	if err != nil {
		return nil, hookMergeDiff{}, err
	}

	if len(strings.TrimSpace(string(existing))) == 0 {
		encoded, err := marshalOrderedJSONObject([]orderedJSONField{
			{Key: antigravityManagedGroup, Value: group},
		})
		if err != nil {
			return nil, hookMergeDiff{}, err
		}
		diff := hookMergeDiff{AddedEvents: antigravityGroupEventNames(group)}
		return encoded, diff, nil
	}

	root := map[string]json.RawMessage{}
	if err := json.Unmarshal(existing, &root); err != nil {
		return nil, hookMergeDiff{}, xerrors.Errorf("existing Antigravity hooks file must contain a JSON object: %w", err)
	}

	diff := antigravityGroupDiff(root[antigravityManagedGroup], group)
	_, hadManagedGroup := root[antigravityManagedGroup]
	root[antigravityManagedGroup] = group

	// Preserve the original top-level group order so foreign hook groups stay
	// where the user put them. A newly introduced Traceary group is appended.
	order, err := orderedTopLevelKeys(existing)
	if err != nil {
		return nil, hookMergeDiff{}, err
	}
	if !hadManagedGroup {
		order = append(order, antigravityManagedGroup)
	}
	fields := make([]orderedJSONField, 0, len(order))
	for _, key := range order {
		fields = append(fields, orderedJSONField{Key: key, Value: root[key]})
	}
	encoded, err := marshalOrderedJSONObject(fields)
	if err != nil {
		return nil, hookMergeDiff{}, err
	}
	return encoded, diff, nil
}

// orderedTopLevelKeys returns the keys of a JSON object in document order. It
// streams the top-level tokens rather than decoding into a map so the original
// group ordering is preserved. An empty document yields no keys.
func orderedTopLevelKeys(document []byte) ([]string, error) {
	decoder := json.NewDecoder(bytes.NewReader(document))
	openBrace, err := decoder.Token()
	if err != nil {
		return nil, xerrors.Errorf("failed to read Antigravity hooks document: %w", err)
	}
	if delim, ok := openBrace.(json.Delim); !ok || delim != '{' {
		return nil, xerrors.Errorf("existing Antigravity hooks file must contain a JSON object")
	}
	var keys []string
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, xerrors.Errorf("failed to read Antigravity hooks key: %w", err)
		}
		key, ok := token.(string)
		if !ok {
			return nil, xerrors.Errorf("Antigravity hooks document key must be a string")
		}
		keys = append(keys, key)
		// Skip the value associated with this key (object/array/scalar).
		if err := skipJSONValue(decoder); err != nil {
			return nil, err
		}
	}
	return keys, nil
}

// skipJSONValue consumes exactly one JSON value (scalar, object, or array) from
// the decoder, descending through nested delimiters until balanced.
func skipJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return xerrors.Errorf("failed to read Antigravity hooks value: %w", err)
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		// Object: each entry is a key token followed by its value.
		for decoder.More() {
			if _, err := decoder.Token(); err != nil {
				return xerrors.Errorf("failed to read Antigravity hooks key: %w", err)
			}
			if err := skipJSONValue(decoder); err != nil {
				return err
			}
		}
	case '[':
		// Array: each entry is a bare value.
		for decoder.More() {
			if err := skipJSONValue(decoder); err != nil {
				return err
			}
		}
	}
	if _, err := decoder.Token(); err != nil {
		return xerrors.Errorf("failed to read Antigravity hooks value: %w", err)
	}
	return nil
}

// antigravityGroupEventNames returns the sorted event keys present in a
// rendered Traceary group.
func antigravityGroupEventNames(group json.RawMessage) []string {
	events := map[string]json.RawMessage{}
	if err := json.Unmarshal(group, &events); err != nil {
		return nil
	}
	names := make([]string, 0, len(events))
	for name := range events {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// antigravityGroupDiff classifies each event in the desired Traceary group
// against the existing group's events.
func antigravityGroupDiff(existingGroup, desiredGroup json.RawMessage) hookMergeDiff {
	desired := map[string]json.RawMessage{}
	_ = json.Unmarshal(desiredGroup, &desired)
	existing := map[string]json.RawMessage{}
	if len(existingGroup) > 0 {
		_ = json.Unmarshal(existingGroup, &existing)
	}

	var diff hookMergeDiff
	for event, desiredValue := range desired {
		existingValue, ok := existing[event]
		switch {
		case !ok:
			diff.AddedEvents = append(diff.AddedEvents, event)
		case jsonRawEqual(existingValue, desiredValue):
			diff.PreservedEvents = append(diff.PreservedEvents, event)
		default:
			diff.RefreshedEvents = append(diff.RefreshedEvents, event)
		}
	}
	for event := range existing {
		if _, ok := desired[event]; !ok {
			diff.RemovedEvents = append(diff.RemovedEvents, event)
		}
	}
	sort.Strings(diff.AddedEvents)
	sort.Strings(diff.RefreshedEvents)
	sort.Strings(diff.PreservedEvents)
	sort.Strings(diff.RemovedEvents)
	return diff
}

// jsonRawEqual compares two raw JSON values for semantic equality by
// re-marshalling through any so key order and whitespace do not matter.
func jsonRawEqual(a, b json.RawMessage) bool {
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		return false
	}
	encodedA, err := json.Marshal(av)
	if err != nil {
		return false
	}
	encodedB, err := json.Marshal(bv)
	if err != nil {
		return false
	}
	return string(encodedA) == string(encodedB)
}

// orderedJSONField is a single key/value pair rendered in declaration order.
type orderedJSONField struct {
	Key   string
	Value any
}

// marshalOrderedJSONObject renders a JSON object whose keys appear in the given
// order with two-space indentation, matching the formatting of the other
// packaged hook configs. Field values may be json.RawMessage (re-indented) or
// any json.Marshal-able value.
func marshalOrderedJSONObject(fields []orderedJSONField) ([]byte, error) {
	ordered := make(map[string]json.RawMessage, len(fields))
	order := make([]string, 0, len(fields))
	for _, field := range fields {
		raw, ok := field.Value.(json.RawMessage)
		if !ok {
			encoded, err := json.Marshal(field.Value)
			if err != nil {
				return nil, xerrors.Errorf("failed to marshal hook field %q: %w", field.Key, err)
			}
			raw = encoded
		}
		ordered[field.Key] = raw
		order = append(order, field.Key)
	}
	return marshalIndentOrdered(ordered, order, "", "  ")
}

// marshalIndentOrdered marshals a raw-message object with the given key order
// and indentation. It builds the compact object first (preserving order) then
// runs json.Indent so nested values are pretty-printed consistently.
func marshalIndentOrdered(values map[string]json.RawMessage, order []string, prefix, indent string) ([]byte, error) {
	var builder strings.Builder
	builder.WriteByte('{')
	for i, key := range order {
		if i > 0 {
			builder.WriteByte(',')
		}
		encodedKey, err := json.Marshal(key)
		if err != nil {
			return nil, xerrors.Errorf("failed to marshal hook key %q: %w", key, err)
		}
		builder.Write(encodedKey)
		builder.WriteByte(':')
		builder.Write(values[key])
	}
	builder.WriteByte('}')

	var indented bytes.Buffer
	if err := json.Indent(&indented, []byte(builder.String()), prefix, indent); err != nil {
		return nil, xerrors.Errorf("failed to indent hook document: %w", err)
	}
	return indented.Bytes(), nil
}
