package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"
)

func (c *RootCLI) newHooksHelperCommand() *cobra.Command {
	helperCmd := &cobra.Command{
		Use:    "helper",
		Short:  "Internal helper commands for Traceary hook scripts",
		Hidden: true,
	}
	helperCmd.AddCommand(c.newHooksHelperJSONGetCommand())
	helperCmd.AddCommand(c.newHooksHelperBuildFailureOutputCommand())
	helperCmd.AddCommand(c.newHooksHelperNormalizeGitRemoteCommand())

	return helperCmd
}

func (c *RootCLI) newHooksHelperJSONGetCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "json-get <path> [default]",
		Short:  "Read a value from the hook payload JSON",
		Hidden: true,
		Args:   cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			defaultValue := ""
			if len(args) == 2 {
				defaultValue = args[1]
			}

			return runHooksHelperJSONGet(cmd.InOrStdin(), cmd.OutOrStdout(), args[0], defaultValue)
		},
	}

	return cmd
}

func (c *RootCLI) newHooksHelperBuildFailureOutputCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "build-failure-output",
		Short:  "Extract a compact failure payload for audit hooks",
		Hidden: true,
		Args:   noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runHooksHelperBuildFailureOutput(cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}

	return cmd
}

func (c *RootCLI) newHooksHelperNormalizeGitRemoteCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "normalize-git-remote [raw]",
		Short:  "Normalize a git remote URL to a Traceary workspace identifier",
		Hidden: true,
		Args:   cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw := ""
			if len(args) == 1 {
				raw = args[0]
			}

			normalized := normalizeGitRemote(raw)
			if _, err := io.WriteString(cmd.OutOrStdout(), normalized); err != nil {
				return xerrors.Errorf("failed to write normalized remote: %w", err)
			}

			return nil
		},
	}

	return cmd
}

func runHooksHelperJSONGet(input io.Reader, output io.Writer, path string, defaultValue string) error {
	payload, err := readHookPayload(input)
	if err != nil || len(payload) == 0 {
		return writeHookHelperOutput(output, defaultValue)
	}

	value, ok := lookupHookPayloadValue(payload, path)
	if !ok {
		return writeHookHelperOutput(output, defaultValue)
	}
	if value == nil {
		return writeHookHelperOutput(output, defaultValue)
	}

	renderedValue, err := renderHookHelperValue(value)
	if err != nil {
		return writeHookHelperOutput(output, defaultValue)
	}

	return writeHookHelperOutput(output, renderedValue)
}

func runHooksHelperBuildFailureOutput(input io.Reader, output io.Writer) error {
	payload, err := readHookPayload(input)
	if err != nil || len(payload) == 0 {
		return nil
	}

	var decodedPayload map[string]any
	if err := json.Unmarshal(payload, &decodedPayload); err != nil {
		return nil
	}

	failurePayload := map[string]any{}
	if errorValue, ok := decodedPayload["error"].(string); ok && strings.TrimSpace(errorValue) != "" {
		failurePayload["error"] = errorValue
	}
	if interruptValue, ok := decodedPayload["is_interrupt"]; ok {
		failurePayload["is_interrupt"] = interruptValue
	}
	if len(failurePayload) == 0 {
		return nil
	}

	encodedValue, err := marshalStableJSON(failurePayload)
	if err != nil {
		return nil
	}

	if _, err := output.Write(encodedValue); err != nil {
		return xerrors.Errorf("failed to write failure payload: %w", err)
	}

	return nil
}

func readHookPayload(input io.Reader) ([]byte, error) {
	if envValue, ok := lookupHookEnv("TRACEARY_HOOK_INPUT"); ok {
		return []byte(envValue), nil
	}
	if input == nil {
		return nil, nil
	}

	payload, err := io.ReadAll(input)
	if err != nil {
		return nil, xerrors.Errorf("failed to read hook payload: %w", err)
	}
	if len(bytes.TrimSpace(payload)) == 0 {
		return nil, nil
	}

	return payload, nil
}

func lookupHookEnv(key string) (string, bool) {
	value, exists := os.LookupEnv(key)
	if !exists {
		return "", false
	}

	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return "", false
	}

	return trimmedValue, true
}

func lookupHookPayloadValue(payload []byte, path string) (any, bool) {
	var currentValue any
	if err := json.Unmarshal(payload, &currentValue); err != nil {
		return nil, false
	}

	for _, segment := range strings.Split(path, ".") {
		if segment == "" {
			continue
		}

		currentMap, ok := currentValue.(map[string]any)
		if !ok {
			return nil, false
		}

		nextValue, exists := currentMap[segment]
		if !exists {
			return nil, false
		}
		currentValue = nextValue
	}

	return currentValue, true
}

func renderHookHelperValue(value any) (string, error) {
	switch typedValue := value.(type) {
	case map[string]any, []any:
		encodedValue, err := marshalStableJSON(value)
		if err != nil {
			return "", xerrors.Errorf("failed to render JSON value: %w", err)
		}

		return string(encodedValue), nil
	case string:
		return typedValue, nil
	default:
		encodedValue, err := marshalStableJSON(value)
		if err != nil {
			return "", xerrors.Errorf("failed to render scalar value: %w", err)
		}

		return strings.Trim(string(encodedValue), `"`), nil
	}
}

func writeHookHelperOutput(output io.Writer, value string) error {
	if _, err := io.WriteString(output, value); err != nil {
		return xerrors.Errorf("failed to write hook helper output: %w", err)
	}

	return nil
}

func marshalStableJSON(value any) ([]byte, error) {
	var buffer bytes.Buffer
	if err := writeStableJSON(&buffer, value); err != nil {
		return nil, err
	}

	return buffer.Bytes(), nil
}

func writeStableJSON(buffer *bytes.Buffer, value any) error {
	switch typedValue := value.(type) {
	case nil:
		buffer.WriteString("null")
	case bool, float64, json.Number:
		encodedValue, err := json.Marshal(typedValue)
		if err != nil {
			return xerrors.Errorf("failed to marshal JSON value: %w", err)
		}
		buffer.Write(encodedValue)
	case string:
		encodedValue, err := json.Marshal(typedValue)
		if err != nil {
			return xerrors.Errorf("failed to marshal JSON string: %w", err)
		}
		buffer.Write(encodedValue)
	case []any:
		buffer.WriteByte('[')
		for index, item := range typedValue {
			if index > 0 {
				buffer.WriteByte(',')
			}
			if err := writeStableJSON(buffer, item); err != nil {
				return err
			}
		}
		buffer.WriteByte(']')
	case map[string]any:
		buffer.WriteByte('{')
		keys := make([]string, 0, len(typedValue))
		for key := range typedValue {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for index, key := range keys {
			if index > 0 {
				buffer.WriteByte(',')
			}

			encodedKey, err := json.Marshal(key)
			if err != nil {
				return xerrors.Errorf("failed to marshal JSON key: %w", err)
			}
			buffer.Write(encodedKey)
			buffer.WriteByte(':')
			if err := writeStableJSON(buffer, typedValue[key]); err != nil {
				return err
			}
		}
		buffer.WriteByte('}')
	default:
		encodedValue, err := json.Marshal(typedValue)
		if err != nil {
			return xerrors.Errorf("failed to marshal unknown JSON value: %w", err)
		}
		buffer.Write(encodedValue)
	}

	return nil
}

func normalizeGitRemote(raw string) string {
	trimmedValue := strings.TrimSpace(raw)
	trimmedValue = strings.TrimSuffix(trimmedValue, ".git")
	if trimmedValue == "" {
		return ""
	}

	if strings.HasPrefix(trimmedValue, "git@") && strings.Contains(trimmedValue, ":") {
		hostAndPath := strings.TrimPrefix(trimmedValue, "git@")
		parts := strings.SplitN(hostAndPath, ":", 2)
		if len(parts) != 2 {
			return trimmedValue
		}

		return strings.ToLower(strings.Trim(parts[0], "/")) + "/" + strings.Trim(parts[1], "/")
	}

	parsedValue, err := url.Parse(trimmedValue)
	if err == nil && parsedValue.Hostname() != "" {
		return strings.ToLower(parsedValue.Hostname()) + "/" + strings.Trim(parsedValue.Path, "/")
	}

	return trimmedValue
}
