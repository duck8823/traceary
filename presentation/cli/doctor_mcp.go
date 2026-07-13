package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
	"golang.org/x/xerrors"
)

type doctorMCPServer struct {
	Command string   `json:"command" toml:"command"`
	Args    []string `json:"args" toml:"args"`
}

func (c *RootCLI) inspectMCPRegistrationForClient(client, outputPath string) doctorCheck {
	name := client + "-mcp"
	switch client {
	case "claude":
		if detection := c.detectClaudeTracearyPluginForCLI(); detection.Active {
			return doctorCheck{Name: name, Status: doctorStatusPass, Message: localizef("claude plugin %q provides the traceary MCP server (%s)", "claude plugin %q が traceary MCP server を提供しています (%s)", detection.PluginKey, detection.SettingsPath)}
		}
		paths := []string{outputPath}
		if home, err := userHomeDirFunc(); err == nil {
			paths = append(paths, filepath.Join(home, ".claude", "settings.json"))
		}
		return c.inspectJSONMCPRegistration(name, "claude", paths, "traceary mcp-server")
	case "codex":
		if home, err := userHomeDirFunc(); err == nil && codexTracearyPluginEnabled(filepath.Join(home, ".codex", "config.toml")) {
			return doctorCheck{Name: name, Status: doctorStatusPass, Message: localizef("codex plugin traceary@local-traceary-plugins is enabled and provides the traceary MCP server: %s", "codex plugin traceary@local-traceary-plugins が有効で traceary MCP server を提供しています: %s", filepath.Join(home, ".codex", "config.toml"))}
		}
		if home, err := userHomeDirFunc(); err == nil {
			return c.inspectTOMLMCPRegistration(name, "codex", filepath.Join(home, ".codex", "config.toml"), "traceary mcp-server")
		}
		return doctorCheck{Name: name, Status: doctorStatusFail, Message: "failed to resolve home directory for codex MCP registration"}
	case "gemini":
		paths := []string{outputPath}
		if home, err := userHomeDirFunc(); err == nil {
			paths = append(paths, filepath.Join(home, ".gemini", "extensions", "traceary", "gemini-extension.json"))
		}
		return inspectJSONMCPRegistration(name, "gemini", paths, "gemini extensions install https://github.com/duck8823/traceary")
	default:
		return doctorCheck{Name: name, Status: doctorStatusSkip, Message: localizef("%s MCP registration check is not supported", "%s MCP registration check は未対応です", client)}
	}
}

func (c *RootCLI) inspectAntigravityMCPRegistration() doctorCheck {
	const (
		name = "antigravity-mcp"
		fix  = "cd <traceary-repository> && agy plugin install integrations/antigravity-plugin"
	)
	home, err := userHomeDirFunc()
	if err != nil {
		return doctorCheck{Name: name, Status: doctorStatusFail, Message: localizef("failed to resolve home directory for Antigravity MCP registration: %v", "Antigravity MCP registration 用のホームディレクトリを解決できませんでした: %v", err)}
	}
	pluginDir := antigravityCLIPluginDir(home)
	info, err := os.Stat(pluginDir)
	if err != nil {
		if os.IsNotExist(err) {
			return doctorCheck{Name: name, Status: doctorStatusSkip, Message: localizef("Antigravity CLI plugin is not installed at %s; direct hook installations do not provide MCP tools", "%s に Antigravity CLI plugin がありません。hook の直接設定では MCP tool は提供されません", pluginDir)}
		}
		return doctorCheck{Name: name, Status: doctorStatusFail, Message: localizef("failed to inspect Antigravity CLI plugin directory: %v", "Antigravity CLI plugin directory の確認に失敗しました: %v", err)}
	}
	if !info.IsDir() {
		return doctorCheck{Name: name, Status: doctorStatusFail, Message: localizef("Antigravity CLI plugin path is not a directory: %s", "Antigravity CLI plugin path が directory ではありません: %s", pluginDir)}
	}
	mcpPath := filepath.Join(pluginDir, "mcp_config.json")
	if _, err := os.Stat(mcpPath); err != nil {
		if os.IsNotExist(err) {
			return doctorCheck{Name: name, Status: doctorStatusWarn, Message: localizef("Antigravity CLI plugin is installed but does not include the Traceary MCP configuration: %s", "Antigravity CLI plugin は導入済みですが Traceary MCP configuration がありません: %s", mcpPath), Hint: "reinstall the Antigravity CLI plugin with MCP support", FixCommand: fix}
		}
		return doctorCheck{Name: name, Status: doctorStatusFail, Message: localizef("failed to inspect Antigravity MCP configuration: %v", "Antigravity MCP configuration の確認に失敗しました: %v", err)}
	}
	return c.inspectJSONMCPRegistration(name, "antigravity", []string{mcpPath}, fix)
}

func inspectJSONMCPRegistration(name, client string, paths []string, fix string) doctorCheck {
	return (&RootCLI{}).inspectJSONMCPRegistration(name, client, paths, fix)
}

func (c *RootCLI) inspectJSONMCPRegistration(name, client string, paths []string, fix string) doctorCheck {
	seenConfig := false
	firstPath := ""
	for _, path := range uniqueNonEmpty(paths) {
		if firstPath == "" {
			firstPath = path
		}
		content, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return doctorCheck{Name: name, Status: doctorStatusFail, Message: localizef("failed to read %s MCP config: %v", "%s MCP config の読み込みに失敗しました: %v", client, err)}
		}
		seenConfig = true
		var root struct {
			MCPServers map[string]doctorMCPServer `json:"mcpServers"`
		}
		if err := json.Unmarshal(content, &root); err != nil {
			return doctorCheck{Name: name, Status: doctorStatusFail, Message: localizef("%s MCP config is invalid JSON: %s", "%s MCP config の JSON が不正です: %s", client, path)}
		}
		server, ok := root.MCPServers["traceary"]
		if !ok {
			continue
		}
		check := evaluateMCPServer(name, client, path, server, fix)
		if check.Status == doctorStatusWarn && client == "claude" {
			attachJSONMCPFix(&check, path)
		}
		return check
	}
	if !seenConfig {
		if client == "claude" && firstPath != "" {
			check := doctorCheck{Name: name, Status: doctorStatusWarn, Message: localizef("%s MCP config file is absent; traceary MCP server is not registered: %s", "%s MCP config file がないため traceary MCP server が登録されていません: %s", client, firstPath), Hint: "register traceary MCP server", FixCommand: fix}
			attachJSONMCPFix(&check, firstPath)
			return check
		}
		return doctorCheck{Name: name, Status: doctorStatusSkip, Message: localizef("%s MCP config file is absent; skipped registration check", "%s MCP config file がないため registration check を skip しました", client)}
	}
	check := doctorCheck{Name: name, Status: doctorStatusWarn, Message: localizef("%s config exists but does not register the traceary MCP server", "%s config はありますが traceary MCP server が登録されていません", client), Hint: "register traceary MCP server", FixCommand: fix}
	if client == "claude" && firstPath != "" {
		attachJSONMCPFix(&check, firstPath)
	}
	return check
}

func (c *RootCLI) inspectTOMLMCPRegistration(name, client, path, fix string) doctorCheck {
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			check := doctorCheck{Name: name, Status: doctorStatusWarn, Message: localizef("%s MCP config file is absent; traceary MCP server is not registered: %s", "%s MCP config file がないため traceary MCP server が登録されていません: %s", client, path), Hint: "register traceary MCP server", FixCommand: fix}
			attachTOMLMCPFix(&check, path)
			return check
		}
		return doctorCheck{Name: name, Status: doctorStatusFail, Message: localizef("failed to read %s MCP config: %v", "%s MCP config の読み込みに失敗しました: %v", client, err)}
	}
	var root struct {
		MCPServers map[string]doctorMCPServer `toml:"mcp_servers"`
	}
	if err := toml.Unmarshal(content, &root); err != nil {
		return doctorCheck{Name: name, Status: doctorStatusFail, Message: localizef("%s MCP config is invalid TOML: %s", "%s MCP config の TOML が不正です: %s", client, path)}
	}
	server, ok := root.MCPServers["traceary"]
	if !ok {
		check := doctorCheck{Name: name, Status: doctorStatusWarn, Message: localizef("%s config exists but does not register the traceary MCP server: %s", "%s config はありますが traceary MCP server が登録されていません: %s", client, path), Hint: "register traceary MCP server", FixCommand: fix}
		attachTOMLMCPFix(&check, path)
		return check
	}
	check := evaluateMCPServer(name, client, path, server, fix)
	if check.Status == doctorStatusWarn {
		attachTOMLMCPFix(&check, path)
	}
	return check
}

func attachJSONMCPFix(check *doctorCheck, path string) {
	check.AutoFixAvailable = true
	check.FixFunc = func(_ context.Context, dryRun bool) (string, error) {
		action := fmt.Sprintf("write traceary MCP registration to %s", path)
		if dryRun {
			return "would: " + action, nil
		}
		return action, writeJSONMCPRegistration(path)
	}
}

func attachTOMLMCPFix(check *doctorCheck, path string) {
	check.AutoFixAvailable = true
	check.FixFunc = func(_ context.Context, dryRun bool) (string, error) {
		action := fmt.Sprintf("write traceary MCP registration to %s", path)
		if dryRun {
			return "would: " + action, nil
		}
		return action, writeTOMLMCPRegistration(path)
	}
}

func writeJSONMCPRegistration(path string) error {
	var root map[string]json.RawMessage
	content, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return xerrors.Errorf("failed to read existing MCP config: %w", err)
		}
		root = map[string]json.RawMessage{}
	} else {
		if err := json.Unmarshal(content, &root); err != nil {
			return xerrors.Errorf("failed to parse existing MCP JSON: %w", err)
		}
		if err := backupDoctorOriginal(path); err != nil {
			return err
		}
	}
	servers := map[string]json.RawMessage{}
	if raw, ok := root["mcpServers"]; ok {
		_ = json.Unmarshal(raw, &servers)
	}
	tracearyRaw, err := json.Marshal(doctorMCPServer{Command: "traceary", Args: []string{"mcp-server"}})
	if err != nil {
		return xerrors.Errorf("failed to marshal traceary MCP server: %w", err)
	}
	servers["traceary"] = tracearyRaw
	raw, err := json.Marshal(servers)
	if err != nil {
		return xerrors.Errorf("failed to marshal MCP servers: %w", err)
	}
	root["mcpServers"] = raw
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return xerrors.Errorf("failed to marshal MCP config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return xerrors.Errorf("failed to create MCP config directory: %w", err)
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
		return xerrors.Errorf("failed to write MCP config: %w", err)
	}
	return nil
}

func writeTOMLMCPRegistration(path string) error {
	content, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return xerrors.Errorf("failed to read existing MCP config: %w", err)
	}
	if err == nil {
		if err := backupDoctorOriginal(path); err != nil {
			return err
		}
	}
	text := ""
	if err == nil {
		text = replaceOrAppendTOMLTracearyMCPRegistration(string(content))
	} else {
		text = tracearyMCPServerTOML()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return xerrors.Errorf("failed to create MCP config directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return xerrors.Errorf("failed to write MCP config: %w", err)
	}
	return nil
}

func tracearyMCPServerTOML() string {
	return "[mcp_servers.traceary]\ncommand = \"traceary\"\nargs = [\"mcp-server\"]\n"
}

func replaceOrAppendTOMLTracearyMCPRegistration(content string) string {
	lines := strings.SplitAfter(content, "\n")
	start := -1
	end := len(lines)
	for i, line := range lines {
		if tomlTableHeaderName(line) == "mcp_servers.traceary" {
			if start == -1 {
				start = i
			}
			continue
		}
		if start != -1 && isTOMLTableHeader(line) {
			end = i
			break
		}
	}
	replacement := tracearyMCPServerTOML()
	if start == -1 {
		text := strings.TrimRight(content, "\n")
		if text == "" {
			return replacement
		}
		return text + "\n\n" + replacement
	}
	out := strings.Join(lines[:start], "") + replacement
	if end < len(lines) {
		if out != "" && !strings.HasSuffix(out, "\n\n") && strings.TrimSpace(strings.Join(lines[end:], "")) != "" {
			out += "\n"
		}
		out += strings.Join(lines[end:], "")
	}
	return out
}

func isTOMLTableHeader(line string) bool {
	return tomlTableHeaderName(line) != ""
}

func tomlTableHeaderName(line string) string {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "[") {
		return ""
	}
	if strings.HasPrefix(trimmed, "[[") {
		return ""
	}
	end := strings.Index(trimmed, "]")
	if end <= 1 {
		return ""
	}
	after := strings.TrimSpace(trimmed[end+1:])
	if after != "" && !strings.HasPrefix(after, "#") {
		return ""
	}
	return strings.TrimSpace(trimmed[1:end])
}

func backupDoctorOriginal(path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return xerrors.Errorf("failed to read original config for backup: %w", err)
	}
	backup := fmt.Sprintf("%s.bak.%s", path, time.Now().UTC().Format("20060102T150405Z"))
	if err := os.WriteFile(backup, content, 0o644); err != nil {
		return xerrors.Errorf("failed to write MCP config backup: %w", err)
	}
	return nil
}

func evaluateMCPServer(name, client, path string, server doctorMCPServer, fix string) doctorCheck {
	if !hasMCPServerCommand(server) {
		return doctorCheck{Name: name, Status: doctorStatusWarn, Message: localizef("%s MCP registration exists but does not run `traceary mcp-server`: %s", "%s MCP registration はありますが `traceary mcp-server` を実行していません: %s", client, path), Hint: "update traceary MCP server command", FixCommand: fix}
	}
	if commandLooksStale(server.Command) {
		return doctorCheck{Name: name, Status: doctorStatusWarn, Message: localizef("%s MCP registration points at a stale traceary binary %s: %s", "%s MCP registration が古い traceary binary %s を参照しています: %s", client, server.Command, path), Hint: "update MCP registration to the running traceary binary", FixCommand: fix}
	}
	return doctorCheck{Name: name, Status: doctorStatusPass, Message: localizef("%s config registers traceary MCP server via %s: %s", "%s config は %s で traceary MCP server を登録しています: %s", client, formatMCPCommand(server), path)}
}

func hasMCPServerCommand(server doctorMCPServer) bool {
	return strings.HasSuffix(filepath.Base(server.Command), "traceary") && len(server.Args) > 0 && server.Args[0] == "mcp-server"
}

func commandLooksStale(command string) bool {
	if command == "traceary" || !filepath.IsAbs(command) {
		return false
	}
	current, err := osExecutableFunc()
	if err != nil || current == "" {
		return false
	}
	return !sameFile(command, current)
}

func formatMCPCommand(server doctorMCPServer) string {
	parts := append([]string{server.Command}, server.Args...)
	return strings.Join(parts, " ")
}

func codexTracearyPluginEnabled(path string) bool {
	content, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var root struct {
		Plugins map[string]struct {
			Enabled bool `toml:"enabled"`
		} `toml:"plugins"`
	}
	if err := toml.Unmarshal(content, &root); err != nil {
		return false
	}
	for key, plugin := range root.Plugins {
		if strings.HasPrefix(key, "traceary@") && plugin.Enabled {
			return true
		}
	}
	return false
}

func uniqueNonEmpty(values []string) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
