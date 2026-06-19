package filesystem

import (
	"encoding/json"
	"sort"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
)

// HooksInspector implements application.HooksInspector by parsing the JSON
// payload stored by a target client's hook configuration file.
type HooksInspector struct{}

// NewHooksInspector constructs a HooksInspector.
func NewHooksInspector() *HooksInspector {
	return &HooksInspector{}
}

// Inspect parses the given hook configuration content and reports whether
// it contains a top-level "hooks" field and whether any Traceary-managed
// hook was detected. It returns application.ErrHookConfigNotJSONObject when
// the payload is not a JSON object and
// application.ErrHookConfigInvalidHooksField when the "hooks" field has the
// wrong shape.
func (i *HooksInspector) Inspect(content []byte) (bool, bool, error) {
	hooksMap, ok, err := parseHooksMap(content)
	if err != nil {
		return ok, false, err
	}
	if !ok {
		return false, false, nil
	}

	for _, matchers := range hooksMap {
		for _, matcher := range matchers {
			for _, command := range matcher.Hooks {
				if isTracearyManagedHookCommandDocument(command, nil) {
					return true, true, nil
				}
			}
		}
	}

	return true, false, nil
}

// DuplicateManagedHooks reports Traceary-managed hook registrations that
// appear more than once for the same host event, matcher, and managed key.
func (i *HooksInspector) DuplicateManagedHooks(content []byte) ([]application.HookDuplicate, error) {
	hooksMap, ok, err := parseHooksMap(content)
	if err != nil || !ok {
		return nil, err
	}

	counts := map[string]*application.HookDuplicate{}
	for event, matchers := range hooksMap {
		for _, matcher := range matchers {
			matcherValue := ""
			if matcher.Matcher != nil {
				matcherValue = *matcher.Matcher
			}
			for _, command := range matcher.Hooks {
				managedKey := extractTracearyManagedKeyFromEntry(command.Name, command.Command)
				if managedKey == "" {
					continue
				}
				id := event + "\x00" + matcherValue + "\x00" + managedKey
				duplicate, ok := counts[id]
				if !ok {
					duplicate = &application.HookDuplicate{
						Event:      event,
						Matcher:    matcherValue,
						ManagedKey: managedKey,
					}
					counts[id] = duplicate
				}
				duplicate.Count++
			}
		}
	}

	duplicates := make([]application.HookDuplicate, 0)
	for _, duplicate := range counts {
		if duplicate.Count > 1 {
			duplicates = append(duplicates, *duplicate)
		}
	}
	sort.Slice(duplicates, func(i, j int) bool {
		if duplicates[i].Event != duplicates[j].Event {
			return duplicates[i].Event < duplicates[j].Event
		}
		if duplicates[i].Matcher != duplicates[j].Matcher {
			return duplicates[i].Matcher < duplicates[j].Matcher
		}
		return duplicates[i].ManagedKey < duplicates[j].ManagedKey
	})

	return duplicates, nil
}

// ExtractManagedKeyFromEntry delegates to the free ExtractTracearyManagedKeyFromEntry
// function so presentation code can consume the canonical-key extraction
// through the application.HooksInspector interface without importing the
// infrastructure package directly.
func (i *HooksInspector) ExtractManagedKeyFromEntry(name, command string) string {
	return ExtractTracearyManagedKeyFromEntry(name, command)
}

// ManagedCoverage reports which Traceary-managed enrichment surfaces are wired
// for the given client in a hook configuration. It uses the same canonical-key
// extraction as duplicate detection so dev-build installs with a non-`traceary`
// binary name are still recognized through their Traceary-managed entry names.
func (i *HooksInspector) ManagedCoverage(content []byte, client string) (application.HookManagedCoverage, error) {
	hooksMap, ok, err := parseHooksMap(content)
	if err != nil || !ok {
		return application.HookManagedCoverage{}, err
	}
	targetClient := strings.TrimSpace(client)
	if targetClient == "" {
		return application.HookManagedCoverage{}, nil
	}

	coverage := application.HookManagedCoverage{}
	claudeAuditEvents := map[string]bool{}
	for event, matchers := range hooksMap {
		for _, matcher := range matchers {
			matcherValue := ""
			if matcher.Matcher != nil {
				matcherValue = *matcher.Matcher
			}
			for _, command := range matcher.Hooks {
				managedKey := extractTracearyManagedKeyFromEntry(command.Name, command.Command)
				switch {
				case managedCoverageMatchesPrompt(event, managedKey, targetClient):
					coverage.HasPrompt = true
				case managedCoverageMatchesTranscript(event, managedKey, targetClient):
					coverage.HasTranscript = true
				case managedCoverageMatchesAudit(event, matcherValue, managedKey, targetClient):
					if targetClient == "claude" {
						claudeAuditEvents[event] = true
					} else {
						coverage.HasAudit = true
					}
				case managedCoverageMatchesCompact(event, managedKey, targetClient):
					coverage.HasCompact = true
				}
			}
		}
	}
	if targetClient == "claude" {
		coverage.HasAudit = claudeAuditEvents["PostToolUse"] && claudeAuditEvents["PostToolUseFailure"]
	}

	return coverage, nil
}

func managedCoverageMatchesPrompt(event, managedKey, client string) bool {
	if managedKey != managedKeyOf("traceary-prompt.sh", client) {
		return false
	}
	switch client {
	case "claude", "codex":
		return event == "UserPromptSubmit"
	case "gemini":
		return event == "BeforeAgent"
	default:
		return false
	}
}

func managedCoverageMatchesTranscript(event, managedKey, client string) bool {
	if managedKey != managedKeyOf("traceary-transcript.sh", client) {
		return false
	}
	switch client {
	case "claude", "codex":
		return event == "Stop"
	case "gemini":
		return event == "AfterAgent"
	default:
		return false
	}
}

func managedCoverageMatchesAudit(event, matcherValue, managedKey, client string) bool {
	if managedKey != managedKeyOf("traceary-audit.sh", client) {
		return false
	}
	switch client {
	case "claude":
		return event == "PostToolUse" || event == "PostToolUseFailure"
	case "codex":
		return event == "PostToolUse"
	case "gemini":
		return event == "AfterTool" && matcherValue == "run_shell_command"
	default:
		return false
	}
}

func managedCoverageMatchesCompact(event, managedKey, client string) bool {
	if !strings.HasPrefix(managedKey, managedKeyOf("traceary-compact.sh", client)+":") {
		return false
	}
	switch client {
	case "claude":
		return event == "PreCompact" || event == "PostCompact" || event == "SessionStart"
	case "gemini":
		return event == "PreCompress"
	default:
		return false
	}
}

func parseHooksMap(content []byte) (map[string][]hookMatcherDocument, bool, error) {
	root := map[string]json.RawMessage{}
	if err := json.Unmarshal(content, &root); err != nil {
		return nil, false, xerrors.Errorf("%w: %v", application.ErrHookConfigNotJSONObject, err)
	}

	hooksValue, ok := root["hooks"]
	if !ok {
		return nil, false, nil
	}

	hooksMap := map[string][]hookMatcherDocument{}
	if err := json.Unmarshal(hooksValue, &hooksMap); err != nil {
		return nil, true, xerrors.Errorf("%w: %v", application.ErrHookConfigInvalidHooksField, err)
	}

	return hooksMap, true, nil
}
