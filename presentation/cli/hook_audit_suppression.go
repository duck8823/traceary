package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func shouldSuppressHookAudit(payload []byte, command string) bool {
	if hookAuditSuppressionEnabled(os.Getenv(hookAuditSuppressionEnvKey)) {
		return true
	}
	if commandCarriesHookAuditSuppression(command) {
		return true
	}
	if isTracearySelfInspectionCommand(command) {
		return true
	}
	toolName := hookPayloadString(payload, "tool_name", "")
	return isTracearyReadMCPToolName(toolName)
}

func hookAuditSuppressionEnabled(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	parsed, err := strconv.ParseBool(trimmed)
	if err == nil {
		return parsed
	}
	switch strings.ToLower(trimmed) {
	case "yes", "y", "on":
		return true
	case "no", "n", "off":
		return false
	default:
		return true
	}
}

func commandCarriesHookAuditSuppression(command string) bool {
	for _, token := range hookAuditCommandTokens(command) {
		if token == "env" || isShellEnvAssignment(token) && !strings.HasPrefix(token, hookAuditSuppressionEnvKey+"=") {
			continue
		}
		if value, ok := strings.CutPrefix(token, hookAuditSuppressionEnvKey+"="); ok {
			return hookAuditSuppressionEnabled(value)
		}
		return false
	}
	return false
}

func isTracearySelfInspectionCommand(command string) bool {
	tokens := tracearyCommandTokens(command)
	if len(tokens) == 0 {
		return false
	}
	switch tokens[0] {
	case "list", "search", "show", "tail", "timeline", "context", "sessions", "top", "doctor", "status":
		return true
	case "session":
		if len(tokens) < 2 {
			return false
		}
		switch tokens[1] {
		case "active", "latest", "tree", "handoff":
			return true
		}
	case "memory":
		if len(tokens) < 2 {
			return false
		}
		switch tokens[1] {
		case "list", "search", "show":
			return true
		case "inbox":
			return len(tokens) >= 3 && (tokens[2] == "list" || tokens[2] == "show")
		}
	case "hooks":
		return len(tokens) >= 2 && (tokens[1] == "print" || tokens[1] == "guide")
	}
	return false
}

func tracearyCommandTokens(command string) []string {
	tokens := hookAuditCommandTokens(command)
	for len(tokens) > 0 {
		token := tokens[0]
		switch {
		case token == "env" || token == "command":
			tokens = tokens[1:]
			continue
		case isShellEnvAssignment(token):
			tokens = tokens[1:]
			continue
		case isTracearyCommandToken(token):
			return tokens[1:]
		default:
			return nil
		}
	}
	return nil
}

func hookAuditCommandTokens(command string) []string {
	fields := strings.Fields(command)
	tokens := make([]string, 0, len(fields))
	for _, field := range fields {
		token := strings.Trim(field, `"'`)
		if token == "" {
			continue
		}
		tokens = append(tokens, token)
	}
	return tokens
}

func isShellEnvAssignment(token string) bool {
	key, _, ok := strings.Cut(token, "=")
	if !ok || key == "" {
		return false
	}
	for i, r := range key {
		if i == 0 {
			if r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
				return false
			}
			continue
		}
		if r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func isTracearyCommandToken(token string) bool {
	return filepath.Base(strings.TrimSpace(token)) == "traceary"
}

func isTracearyReadMCPToolName(toolName string) bool {
	normalized := strings.TrimSpace(toolName)
	if normalized == "" {
		return false
	}
	tracearyQualified := strings.HasPrefix(normalized, "mcp__traceary__") || strings.HasPrefix(normalized, "traceary.")
	normalized = strings.TrimPrefix(normalized, "mcp__traceary__")
	normalized = strings.TrimPrefix(normalized, "traceary.")
	switch normalized {
	case "list_events", "get_context", "session_status", "query_memory":
		return true
	case "search":
		return tracearyQualified
	default:
		return false
	}
}
