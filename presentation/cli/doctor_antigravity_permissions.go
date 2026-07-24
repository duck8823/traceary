package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/xerrors"
)

// antigravityRequiredHookPermissions is the complete and intentionally narrow
// command boundary needed by the packaged Antigravity hooks. Keep this list in
// sync with integrations/antigravity-plugin/hooks.json and
// permissions.example.json. The hook commands run inside Antigravity's terminal
// sandbox; no unsandboxed permission is needed.
var antigravityRequiredHookPermissions = []string{
	"command(traceary hook antigravity pre-invocation)",
	"command(traceary hook antigravity pre-tool-use)",
	"command(traceary hook antigravity post-tool-use)",
	"command(traceary hook antigravity stop)",
}

// antigravityPermissionRules mirrors the documented Antigravity `permissions`
// object in Antigravity CLI's global settings document.
type antigravityPermissionRules struct {
	Allow []string `json:"allow"`
	Deny  []string `json:"deny"`
	Ask   []string `json:"ask"`
}

// antigravityPermissionAssessment is the observable result used by doctor.
// Missing lists exact hook resources that have no exact allow. Shadowed lists
// exact resources matched by a deny/ask rule, which takes precedence in the
// Antigravity host. Unsafe lists broader command or unsandboxed allow rules;
// these never substitute for the packaged exact rules.
type antigravityPermissionAssessment struct {
	Executable bool
	Missing    []string
	Shadowed   []string
	Unsafe     []string
	ReadErrors []string
}

// evaluateAntigravityHeadlessPermissions applies the documented Antigravity
// permission semantics needed by Traceary's hook boundary. It intentionally
// accepts only the four exact command resources as positive evidence. A prefix,
// regex, or wildcard grant could authorize model-initiated commands beyond the
// hook entrypoints and therefore fails the least-privilege check.
func evaluateAntigravityHeadlessPermissions(rules antigravityPermissionRules) antigravityPermissionAssessment {
	assessment := antigravityPermissionAssessment{}
	exactAllows := make(map[string]struct{}, len(rules.Allow))
	for _, rule := range rules.Allow {
		exactAllows[normalizeAntigravityPermissionResource(rule)] = struct{}{}
	}

	for _, required := range antigravityRequiredHookPermissions {
		if _, ok := exactAllows[required]; !ok {
			assessment.Missing = append(assessment.Missing, required)
			continue
		}
		if antigravityAnyRuleMatches(rules.Deny, required) || antigravityAnyRuleMatches(rules.Ask, required) {
			assessment.Shadowed = append(assessment.Shadowed, required)
		}
	}

	for _, rule := range rules.Allow {
		action, _, ok := splitAntigravityPermissionResource(rule)
		if !ok || (action != "command" && action != "unsandboxed") {
			continue
		}
		for _, required := range antigravityRequiredHookPermissions {
			if !antigravityPermissionRuleMatches(rule, required) {
				continue
			}
			if action == "unsandboxed" || normalizeAntigravityPermissionResource(rule) != required {
				assessment.Unsafe = appendUniqueString(assessment.Unsafe, normalizeAntigravityPermissionResource(rule))
			}
			break
		}
	}

	assessment.Executable = len(assessment.Missing) == 0 &&
		len(assessment.Shadowed) == 0 &&
		len(assessment.Unsafe) == 0
	return assessment
}

func normalizeAntigravityPermissionResource(resource string) string {
	action, target, ok := splitAntigravityPermissionResource(resource)
	if !ok {
		return strings.TrimSpace(resource)
	}
	return action + "(" + strings.Join(strings.Fields(target), " ") + ")"
}

func splitAntigravityPermissionResource(resource string) (string, string, bool) {
	resource = strings.TrimSpace(resource)
	open := strings.IndexByte(resource, '(')
	if open <= 0 || !strings.HasSuffix(resource, ")") {
		return "", "", false
	}
	action := strings.TrimSpace(resource[:open])
	target := strings.TrimSpace(resource[open+1 : len(resource)-1])
	if action == "" || target == "" {
		return "", "", false
	}
	return action, target, true
}

func antigravityAnyRuleMatches(rules []string, required string) bool {
	for _, rule := range rules {
		if antigravityPermissionRuleMatches(rule, required) {
			return true
		}
	}
	return false
}

// antigravityPermissionRuleMatches implements the documented exact token-prefix
// model with anchored regular expressions. Go's RE2 engine keeps user-owned
// settings rules safe to inspect. The host's global "*" target is handled as a
// special case rather than as a standalone invalid regular expression.
func antigravityPermissionRuleMatches(rule, required string) bool {
	ruleAction, ruleTarget, ok := splitAntigravityPermissionResource(rule)
	if !ok {
		return false
	}
	requiredAction, requiredTarget, ok := splitAntigravityPermissionResource(required)
	if !ok {
		return false
	}
	if ruleAction == "unsandboxed" && requiredAction == "command" {
		// `unsandboxed` uses the same command-prefix matcher. Treat it as a
		// matching unsafe grant when evaluating the Traceary command boundary.
		ruleAction = "command"
	}
	if ruleAction != requiredAction {
		return false
	}
	if ruleTarget == "*" {
		return true
	}

	patternTokens := strings.Fields(ruleTarget)
	requiredTokens := strings.Fields(requiredTarget)
	if len(patternTokens) == 0 || len(patternTokens) > len(requiredTokens) {
		return false
	}
	for i, pattern := range patternTokens {
		re, err := regexp.Compile("^(?:" + pattern + ")$")
		if err != nil || !re.MatchString(requiredTokens[i]) {
			return false
		}
	}
	return true
}

func appendUniqueString(values []string, candidate string) []string {
	for _, value := range values {
		if value == candidate {
			return values
		}
	}
	return append(values, candidate)
}

// readAntigravityPermissionRules extracts the documented top-level
// `permissions` object from a settings document. Unknown settings are ignored;
// malformed permission list types fail closed.
func readAntigravityPermissionRules(data []byte) (antigravityPermissionRules, error) {
	var document struct {
		Permissions json.RawMessage `json:"permissions"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		return antigravityPermissionRules{}, xerrors.Errorf("decode Antigravity settings: %w", err)
	}
	if len(document.Permissions) == 0 || string(document.Permissions) == "null" {
		return antigravityPermissionRules{}, nil
	}
	var rules antigravityPermissionRules
	if err := json.Unmarshal(document.Permissions, &rules); err != nil {
		return antigravityPermissionRules{}, xerrors.Errorf("decode Antigravity permissions: %w", err)
	}
	return rules, nil
}

// inspectAntigravityHeadlessPermissions reads only Antigravity CLI's documented
// global settings document (~/.gemini/antigravity-cli/settings.json). The host
// does not use Gemini CLI or project settings as a permission source for this
// integration, so treating either as readiness evidence would be misleading.
// It does not launch Antigravity, inspect conversations, or read credentials.
func inspectAntigravityHeadlessPermissions() antigravityPermissionAssessment {
	home, err := userHomeDirFunc()
	if err != nil {
		return antigravityPermissionAssessment{
			ReadErrors: []string{"home directory could not be resolved"},
		}
	}

	path := filepath.Join(home, ".gemini", "antigravity-cli", "settings.json")
	data, readErr := os.ReadFile(path) // #nosec G304 -- fixed Antigravity CLI global settings path
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return evaluateAntigravityHeadlessPermissions(antigravityPermissionRules{})
		}
		assessment := evaluateAntigravityHeadlessPermissions(antigravityPermissionRules{})
		assessment.ReadErrors = []string{filepath.Base(path)}
		assessment.Executable = false
		return assessment
	}
	rules, parseErr := readAntigravityPermissionRules(data)
	if parseErr != nil {
		assessment := evaluateAntigravityHeadlessPermissions(antigravityPermissionRules{})
		assessment.ReadErrors = []string{filepath.Base(path)}
		return assessment
	}
	return evaluateAntigravityHeadlessPermissions(rules)
}

func buildAntigravityHeadlessCoverageCheck(
	routes []antigravityHookRoute,
	assessment antigravityPermissionAssessment,
) doctorCheck {
	const checkName = "antigravity-headless-hooks"
	healthyRoutes := make([]string, 0, len(routes))
	for _, route := range routes {
		if route.Healthy {
			healthyRoutes = append(healthyRoutes, route.Label)
		}
	}
	if len(healthyRoutes) == 0 {
		return doctorCheck{
			Name:   checkName,
			Status: doctorStatusSkip,
			Message: Localize(
				"no healthy Antigravity hook route is installed; headless command permission coverage is not evaluated",
				"健全な Antigravity hook 経路が導入されていないため、headless command permission coverage は判定しません",
			),
		}
	}
	if len(healthyRoutes) != 1 {
		return doctorCheck{
			Name:   checkName,
			Status: doctorStatusWarn,
			Hint: Localize(
				"Retain exactly one Antigravity hook route before relying on headless hook readiness; multiple routes can invoke each hook more than once.",
				"headless hook readiness を利用する前に Antigravity hook 経路を 1 つだけ残してください。複数経路では各 hook が複数回呼ばれる可能性があります。",
			),
			Message: localizef(
				"Antigravity hooks are active via multiple routes (%s), so headless hook readiness is not healthy even when scoped permissions are present. Retain exactly one route.",
				"Antigravity hooks は複数経路 (%s) で有効なため、scoped permission があっても headless hook readiness は健全ではありません。経路を 1 つだけ残してください。",
				strings.Join(healthyRoutes, ", "),
			),
		}
	}
	if assessment.Executable {
		return doctorCheck{
			Name:   checkName,
			Status: doctorStatusPass,
			Message: localizef(
				"Antigravity hooks are installed via %s and all four required hook commands have exact, effective permissions for sandboxed headless execution.",
				"Antigravity hooks は %s 経由で導入され、必要な 4 個の hook command には sandboxed headless 実行用の exact かつ有効な permission があります。",
				strings.Join(healthyRoutes, ", "),
			),
		}
	}

	return doctorCheck{
		Name:   checkName,
		Status: doctorStatusWarn,
		Hint: Localize(
			"Merge integrations/antigravity-plugin/permissions.example.json into the Antigravity permissions object. Keep plan mode and the terminal sandbox enabled; do not use wildcard command grants, unsandboxed grants, or permission bypass flags.",
			"integrations/antigravity-plugin/permissions.example.json を Antigravity の permissions object に merge してください。plan mode と terminal sandbox は有効なままにし、command の wildcard grant、unsandboxed grant、permission bypass flag は使用しないでください。",
		),
		Message: localizef(
			"Antigravity hooks are installed via %s but are not executable in non-interactive sandboxed headless mode with the required scoped permissions (missing=%d shadowed=%d unsafe=%d unreadable_settings=%d).",
			"Antigravity hooks は %s 経由で導入されていますが、必要な scoped permission では非対話の sandboxed headless mode から実行できません (missing=%d shadowed=%d unsafe=%d unreadable_settings=%d)。",
			strings.Join(healthyRoutes, ", "),
			len(assessment.Missing),
			len(assessment.Shadowed),
			len(assessment.Unsafe),
			len(assessment.ReadErrors),
		),
	}
}
