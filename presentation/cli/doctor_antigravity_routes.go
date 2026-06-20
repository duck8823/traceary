package cli

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/duck8823/traceary/domain/types"
)

// Antigravity supports three independent install surfaces for Traceary hooks.
// Each is optional on its own; collectively at least one must be healthy for
// Traceary to capture Antigravity sessions. Doctor reports each route's status
// separately and a single summary decides whether the overall setup is healthy,
// so a missing workspace file no longer warns when a user-level or CLI-plugin
// route is active.
const (
	antigravityRouteWorkspaceLabel = "workspace"
	antigravityRouteUserLabel      = "user-level"
	antigravityRoutePluginLabel    = "CLI plugin"

	antigravityRouteWorkspaceCheck = "antigravity-hooks-workspace"
	antigravityRouteUserCheck      = "antigravity-hooks-user"
	antigravityRouteSummaryCheck   = "antigravity-hooks"
)

// antigravityHookRoute is one observed Antigravity hook install surface.
// Healthy reports whether the route registers the Traceary hooks; Present
// reports whether the route artifact exists at all (even when not healthy) and
// is used only to phrase the summary precisely. Check is the per-route
// doctorCheck appended to the report.
type antigravityHookRoute struct {
	Label   string
	Healthy bool
	Present bool
	Check   doctorCheck
}

// antigravityHookFileHealth classifies the shared workspace/user hooks.json
// document shape (a top-level map of hook-group name to event configs).
type antigravityHookFileHealth int

const (
	antigravityHookFileAbsent  antigravityHookFileHealth = iota // file missing
	antigravityHookFileHealthy                                  // valid JSON object with a traceary group
	antigravityHookFileNoGroup                                  // valid JSON object without a traceary group
	antigravityHookFileInvalid                                  // present but not a JSON object
)

// classifyAntigravityHookFile maps a read result for a workspace/user
// hooks.json into a health value. It is pure: readErr that is os.IsNotExist
// means absent; any other read error or non-object document is invalid.
func classifyAntigravityHookFile(data []byte, readErr error) antigravityHookFileHealth {
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return antigravityHookFileAbsent
		}
		return antigravityHookFileInvalid
	}
	var groups map[string]json.RawMessage
	if err := json.Unmarshal(data, &groups); err != nil {
		return antigravityHookFileInvalid
	}
	if _, ok := groups["traceary"]; !ok {
		return antigravityHookFileNoGroup
	}
	return antigravityHookFileHealthy
}

// buildAntigravityHookFileCheck maps a classified hook-file route to its
// per-route doctorCheck plus the (healthy, present) flags the summary needs. A
// missing or group-less file is SKIP — an absent optional route is never a
// warning on its own; the summary decides whether overall coverage is missing.
// An unreadable / malformed file is FAIL because the host itself rejects it
// regardless of the other routes.
func buildAntigravityHookFileCheck(label, checkName, path string, health antigravityHookFileHealth) (doctorCheck, bool, bool) {
	switch health {
	case antigravityHookFileHealthy:
		return doctorCheck{
			Name:   checkName,
			Status: doctorStatusPass,
			Message: localizef(
				"Antigravity %s hooks register the traceary group: %s",
				"Antigravity %s hooks に traceary グループが登録されています: %s",
				label, path,
			),
		}, true, true
	case antigravityHookFileNoGroup:
		return doctorCheck{
			Name:   checkName,
			Status: doctorStatusSkip,
			Message: localizef(
				"Antigravity %s hooks at %s have no traceary group, so this route is inactive (optional when another route is active). Enable it with: traceary hooks install --client antigravity --upgrade",
				"Antigravity %s hooks (%s) に traceary グループがないため、この経路は無効です（別の経路が有効なら任意です）。有効化: traceary hooks install --client antigravity --upgrade",
				label, path,
			),
		}, false, true
	case antigravityHookFileInvalid:
		return doctorCheck{
			Name:   checkName,
			Status: doctorStatusFail,
			Message: localizef(
				"invalid Antigravity %s hooks config at %s (not a JSON object). Antigravity will reject it; fix or reinstall with: traceary hooks install --client antigravity",
				"Antigravity %s hooks config (%s) が不正です（JSON オブジェクトではありません）。Antigravity が読み込めないため、修正するか再インストールしてください: traceary hooks install --client antigravity",
				label, path,
			),
		}, false, true
	default: // antigravityHookFileAbsent
		return doctorCheck{
			Name:   checkName,
			Status: doctorStatusSkip,
			Message: localizef(
				"no Antigravity %s hooks at %s (optional when a user-level or CLI-plugin route is active)",
				"Antigravity %s hooks (%s) はありません（user-level または CLI plugin の経路が有効なら任意です）",
				label, path,
			),
		}, false, false
	}
}

// inspectAntigravityHookFileRoute reads a workspace/user hooks.json and returns
// the observed route. The fix order matters: the JSON message above prints
// label then path, so callers pass a fixed label.
func inspectAntigravityHookFileRoute(label, checkName, path string) antigravityHookRoute {
	data, err := os.ReadFile(path) // #nosec G304 -- resolved install path
	health := classifyAntigravityHookFile(data, err)
	check, healthy, present := buildAntigravityHookFileCheck(label, checkName, path, health)
	return antigravityHookRoute{Label: label, Healthy: healthy, Present: present, Check: check}
}

// antigravityCLIPluginRoute observes the Antigravity CLI plugin route, reusing
// the existing probe/classify/build trio and mapping its shape into the route
// model. The plugin builder keeps its rich stale/unknown WARN and FixCommand.
func antigravityCLIPluginRoute() antigravityHookRoute {
	check := inspectAntigravityCLIPlugin()
	home, err := userHomeDirFunc()
	if err != nil {
		// Home could not be resolved; inspectAntigravityCLIPlugin already
		// returned a SKIP describing the failure. The route is neither healthy
		// nor known to be present.
		return antigravityHookRoute{Label: antigravityRoutePluginLabel, Check: check}
	}
	shape := classifyAntigravityCLIPluginProbe(probeAntigravityCLIPlugin(antigravityCLIPluginDir(home)))
	return antigravityHookRoute{
		Label:   antigravityRoutePluginLabel,
		Healthy: shape == antigravityCLIPluginHealthy,
		Present: shape != antigravityCLIPluginAbsent,
		Check:   check,
	}
}

// antigravityUserHooksPath returns the user-level Antigravity hooks path
// (~/.gemini/config/hooks.json), reusing the canonical --global resolver so the
// path stays a single source of truth.
func antigravityUserHooksPath() (string, bool) {
	path, resolved, err := resolveHooksGlobalPath("antigravity")
	if err != nil || !resolved {
		return "", false
	}
	return path, true
}

// inspectAntigravityHookRoutes observes all three Antigravity hook routes and
// returns the per-route checks followed by the aggregate summary.
func (c *RootCLI) inspectAntigravityHookRoutes(projectDir string) []doctorCheck {
	routes := make([]antigravityHookRoute, 0, 3)
	if wsPath, err := c.hooksOrchestrator.ResolveInstallPath("antigravity", projectDir, types.None[string]()); err == nil {
		routes = append(routes, inspectAntigravityHookFileRoute(antigravityRouteWorkspaceLabel, antigravityRouteWorkspaceCheck, wsPath))
	}
	if userPath, ok := antigravityUserHooksPath(); ok {
		routes = append(routes, inspectAntigravityHookFileRoute(antigravityRouteUserLabel, antigravityRouteUserCheck, userPath))
	}
	routes = append(routes, antigravityCLIPluginRoute())
	return antigravityHookRouteChecks(routes)
}

// antigravityHookRouteChecks emits each route's per-route check followed by the
// aggregate summary.
func antigravityHookRouteChecks(routes []antigravityHookRoute) []doctorCheck {
	checks := make([]doctorCheck, 0, len(routes)+1)
	for _, route := range routes {
		checks = append(checks, route.Check)
	}
	checks = append(checks, antigravityHookRouteSummary(routes))
	return checks
}

// antigravityRouteInstallHint is the actionable install guidance listing all
// three supported routes.
func antigravityRouteInstallHint() string {
	return Localize(
		"Install one of: workspace (`traceary hooks install --client antigravity`), user-level (`traceary hooks install --client antigravity --global`), or the Antigravity CLI plugin (`agy plugin install integrations/antigravity-plugin`).",
		"次のいずれかを導入してください: workspace (`traceary hooks install --client antigravity`)、user-level (`traceary hooks install --client antigravity --global`)、または Antigravity CLI plugin (`agy plugin install integrations/antigravity-plugin`)。",
	)
}

// antigravityHookRouteSummary collapses the observed routes into the single
// antigravity-hooks check. Any healthy route is a PASS; otherwise it is a WARN
// with an actionable install message. The summary is the only place that needs
// the cross-route view, keeping the per-route checks sibling-independent.
func antigravityHookRouteSummary(routes []antigravityHookRoute) doctorCheck {
	healthy := make([]string, 0, len(routes))
	presentNotHealthy := make([]string, 0, len(routes))
	for _, route := range routes {
		switch {
		case route.Healthy:
			healthy = append(healthy, route.Label)
		case route.Present:
			presentNotHealthy = append(presentNotHealthy, route.Label)
		}
	}

	if len(healthy) > 0 {
		return doctorCheck{
			Name:   antigravityRouteSummaryCheck,
			Status: doctorStatusPass,
			Message: localizef(
				"Antigravity hooks are active via: %s. Workspace hooks are optional when a user-level or CLI-plugin route is active.",
				"Antigravity hooks は次の経路で有効です: %s。user-level または CLI plugin の経路が有効な場合、workspace hooks は任意です。",
				strings.Join(healthy, ", "),
			),
		}
	}

	if len(presentNotHealthy) > 0 {
		return doctorCheck{
			Name:       antigravityRouteSummaryCheck,
			Status:     doctorStatusWarn,
			FixCommand: "traceary hooks install --client antigravity",
			Message: localizef(
				"no healthy Antigravity hook route is active; these routes exist but do not register Traceary: %s (see the per-route checks above). "+antigravityRouteInstallHint(),
				"有効な Antigravity hook 経路がありません。次の経路は存在しますが Traceary を登録していません: %s（上の経路別チェックを参照）。"+antigravityRouteInstallHint(),
				strings.Join(presentNotHealthy, ", "),
			),
		}
	}

	return doctorCheck{
		Name:       antigravityRouteSummaryCheck,
		Status:     doctorStatusWarn,
		FixCommand: "traceary hooks install --client antigravity",
		Message: Localize(
			"no supported Antigravity hook route is installed (workspace, user-level, or CLI plugin). "+antigravityRouteInstallHint(),
			"サポートされた Antigravity hook 経路がインストールされていません (workspace / user-level / CLI plugin)。"+antigravityRouteInstallHint(),
		),
	}
}
