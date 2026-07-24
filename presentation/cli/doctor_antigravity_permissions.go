package cli

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
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
// object. Rules can come from CLI settings, shared user settings, or the
// matching project settings document.
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

// inspectAntigravityHeadlessPermissions reads only documented settings files.
// It does not launch Antigravity, inspect conversations, or read credentials.
func inspectAntigravityHeadlessPermissions(projectDir string) antigravityPermissionAssessment {
	home, err := userHomeDirFunc()
	if err != nil {
		return antigravityPermissionAssessment{
			ReadErrors: []string{"home directory could not be resolved"},
		}
	}

	paths := []string{
		filepath.Join(home, ".gemini", "antigravity-cli", "settings.json"),
		filepath.Join(home, ".gemini", "settings.json"),
	}
	paths = append(paths, matchingAntigravityProjectSettingsPaths(home, projectDir)...)

	var combined antigravityPermissionRules
	var readErrors []string
	for _, path := range paths {
		data, readErr := os.ReadFile(path) // #nosec G304 -- fixed Antigravity settings paths
		if readErr != nil {
			if os.IsNotExist(readErr) {
				continue
			}
			readErrors = append(readErrors, filepath.Base(path))
			continue
		}
		rules, parseErr := readAntigravityPermissionRules(data)
		if parseErr != nil {
			readErrors = append(readErrors, filepath.Base(path))
			continue
		}
		combined.Allow = append(combined.Allow, rules.Allow...)
		combined.Deny = append(combined.Deny, rules.Deny...)
		combined.Ask = append(combined.Ask, rules.Ask...)
	}

	assessment := evaluateAntigravityHeadlessPermissions(combined)
	assessment.ReadErrors = readErrors
	if len(readErrors) > 0 {
		assessment.Executable = false
	}
	return assessment
}

// matchingAntigravityProjectSettingsPaths selects only the current project's
// host-owned JSON document. Other project files are not treated as permission
// sources, preventing unrelated grants from making this workspace look ready.
func matchingAntigravityProjectSettingsPaths(home, projectDir string) []string {
	projectDir = cleanAntigravityProjectPath(projectDir)
	if projectDir == "" {
		return nil
	}
	candidates, err := filepath.Glob(filepath.Join(home, ".gemini", "config", "projects", "*.json"))
	if err != nil {
		return nil
	}
	var matches []string
	for _, candidate := range candidates {
		data, readErr := os.ReadFile(candidate) // #nosec G304 -- fixed Antigravity projects directory
		if readErr != nil {
			continue
		}
		if antigravityProjectSettingsMatch(data, projectDir) {
			matches = append(matches, candidate)
		}
	}
	return matches
}

func antigravityProjectSettingsMatch(data []byte, projectDir string) bool {
	var document struct {
		Name             string `json:"name"`
		ProjectResources struct {
			Resources []struct {
				FolderURI string `json:"folderUri"`
			} `json:"resources"`
		} `json:"projectResources"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		return false
	}
	if cleanAntigravityProjectPath(document.Name) == projectDir {
		return true
	}
	for _, resource := range document.ProjectResources.Resources {
		parsed, err := url.Parse(resource.FolderURI)
		if err != nil || parsed.Scheme != "file" {
			continue
		}
		path, err := url.PathUnescape(parsed.Path)
		if runtime.GOOS == "windows" && len(path) >= 3 && path[0] == '/' && path[2] == ':' {
			path = path[1:]
		}
		if err == nil && cleanAntigravityProjectPath(filepath.FromSlash(path)) == projectDir {
			return true
		}
	}
	return false
}

func cleanAntigravityProjectPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(absolute)
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
