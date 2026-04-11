package cli

import (
	"encoding/json"
	"os"
	"strings"

	"golang.org/x/xerrors"
)

func marshalHooksSettingsFile(outputPath string, settings *hooksSettings, force bool) ([]byte, error) {
	if _, err := os.Stat(outputPath); err != nil {
		if os.IsNotExist(err) {
			return marshalHooksSettings(settings)
		}

		return nil, xerrors.Errorf("%s: %w", Localize("failed to inspect existing file", "既存ファイルの確認に失敗しました"), err)
	}

	if force {
		return marshalHooksSettings(settings)
	}

	existingContent, err := os.ReadFile(outputPath)
	if err != nil {
		return nil, xerrors.Errorf("%s: %w", Localize("failed to read existing settings file", "既存設定ファイルの読み込みに失敗しました"), err)
	}

	mergedContent, err := mergeHooksSettingsJSON(existingContent, settings)
	if err != nil {
		return nil, xerrors.Errorf("%s: %w", Localize("failed to merge existing hook configuration", "既存 hook 設定のマージに失敗しました"), err)
	}

	return mergedContent, nil
}

func marshalHooksSettings(settings *hooksSettings) ([]byte, error) {
	encoded, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, xerrors.Errorf("%s: %w", Localize("failed to marshal hook configuration example", "hook 設定例の JSON 変換に失敗しました"), err)
	}

	return encoded, nil
}

func mergeHooksSettingsJSON(existingContent []byte, settings *hooksSettings) ([]byte, error) {
	if len(strings.TrimSpace(string(existingContent))) == 0 {
		return marshalHooksSettings(settings)
	}

	root := map[string]json.RawMessage{}
	if err := json.Unmarshal(existingContent, &root); err != nil {
		return nil, xerrors.Errorf(Localize("existing settings file must contain a JSON object", "既存設定ファイルは JSON object である必要があります"))
	}

	existingHooks := map[string][]hookMatcher{}
	if hooksValue, ok := root["hooks"]; ok && len(strings.TrimSpace(string(hooksValue))) > 0 {
		if err := json.Unmarshal(hooksValue, &existingHooks); err != nil {
			return nil, xerrors.Errorf(Localize(
				"existing hooks field must be a JSON object whose values are hook arrays",
				"既存 hooks フィールドは hook 配列を値に持つ JSON object である必要があります",
			))
		}
	}

	for hookEvent, desiredMatchers := range settings.Hooks {
		existingHooks[hookEvent] = mergeHookMatchers(existingHooks[hookEvent], desiredMatchers)
	}

	encodedHooks, err := json.MarshalIndent(existingHooks, "", "  ")
	if err != nil {
		return nil, xerrors.Errorf("failed to marshal merged hooks JSON: %w", err)
	}
	root["hooks"] = encodedHooks

	encodedRoot, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, xerrors.Errorf("failed to marshal merged settings JSON: %w", err)
	}

	return encodedRoot, nil
}

func mergeHookMatchers(existing []hookMatcher, desired []hookMatcher) []hookMatcher {
	merged := make([]hookMatcher, 0, len(existing)+len(desired))
	for _, matcher := range existing {
		filteredHooks := make([]hookCommand, 0, len(matcher.Hooks))
		for _, hook := range matcher.Hooks {
			if isTracearyManagedHookCommand(hook) {
				continue
			}
			filteredHooks = append(filteredHooks, hook)
		}
		if len(filteredHooks) == 0 {
			continue
		}
		matcher.Hooks = filteredHooks
		merged = append(merged, matcher)
	}

	return append(merged, desired...)
}

func isTracearyManagedHookCommand(hook hookCommand) bool {
	if strings.HasPrefix(strings.TrimSpace(hook.Name), "traceary-") {
		return true
	}

	commandValue := strings.TrimSpace(hook.Command)
	return strings.Contains(commandValue, "traceary-session.sh") ||
		strings.Contains(commandValue, "traceary-audit.sh") ||
		(strings.Contains(commandValue, "traceary-compact.sh") && strings.Contains(commandValue, "post-compact"))
}
