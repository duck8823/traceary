package cli

import (
	"encoding/json"
	"os"
	"runtime"
)

// inspectAntigravityConfigFile reports whether the Antigravity hooks config at
// outputPath registers the Traceary hook group. Antigravity's hooks.json is a
// top-level map of hook-group name to event configs, so this checks for the
// "traceary" group rather than the shared {"hooks": {...}} shape.
func inspectAntigravityConfigFile(outputPath string) doctorCheck {
	const checkName = "antigravity-config"
	data, err := os.ReadFile(outputPath) // #nosec G304 -- resolved install path
	if err != nil {
		return doctorCheck{
			Name:   checkName,
			Status: doctorStatusWarn,
			Message: localizef(
				"no Antigravity hooks config at %s. Install with: traceary hooks install --client antigravity",
				"%s に Antigravity hooks config がありません。導入: traceary hooks install --client antigravity",
				outputPath,
			),
		}
	}
	var groups map[string]json.RawMessage
	if err := json.Unmarshal(data, &groups); err != nil {
		return doctorCheck{
			Name:    checkName,
			Status:  doctorStatusFail,
			Message: localizef("invalid Antigravity hooks config at %s: %v", "%s の Antigravity hooks config が不正です: %v", outputPath, err),
		}
	}
	if _, ok := groups["traceary"]; !ok {
		return doctorCheck{
			Name:   checkName,
			Status: doctorStatusWarn,
			Message: localizef(
				"Antigravity hooks config at %s has no traceary group. Run: traceary hooks install --client antigravity --upgrade",
				"%s の Antigravity hooks config に traceary グループがありません。実行: traceary hooks install --client antigravity --upgrade",
				outputPath,
			),
		}
	}
	return doctorCheck{
		Name:    checkName,
		Status:  doctorStatusPass,
		Message: localizef("Antigravity hooks config registers the traceary group: %s", "Antigravity hooks config に traceary グループが登録されています: %s", outputPath),
	}
}

// antigravityCapabilityState represents the detected installation and
// automation-surface state of the Antigravity application.
type antigravityCapabilityState int

const (
	antigravityStateNotInstalled     antigravityCapabilityState = iota // no app bundle and no CLI on PATH
	antigravityStateToolUnavailable                                    // installed but no supported public headless/hook/package surface
	antigravityStateNotAuthenticated                                   // future: supported surface exists but not authenticated/configured
	antigravityStateAvailable                                          // future: supported CLI/contract confirmed and ready
)

// antigravityCapabilityProbe captures the observed facts about an Antigravity
// installation. Keeping observed facts separate from the state-mapping logic
// means future surface-level or auth-level signals can be added without
// changing the production predicate-wiring signature.
type antigravityCapabilityProbe struct {
	CLIFound                  bool // "antigravity" binary found on PATH
	BundleFound               bool // OS-specific app bundle found on filesystem
	SupportedSurfaceConfirmed bool // a supported public hook/CLI contract is confirmed
	AuthenticatedOrConfigured bool // supported surface is authenticated/configured (future)
}

// antigravityBundlePaths returns the expected app bundle paths for the
// current OS. Only macOS bundle paths are currently known for Antigravity.
func antigravityBundlePaths() []string {
	if runtime.GOOS != "darwin" {
		return nil
	}
	return []string{"/Applications/Antigravity.app"}
}

// antigravityProbeToState maps an observed probe to a capability state.
// The mapping is purely deterministic — no I/O, no side effects.
func antigravityProbeToState(p antigravityCapabilityProbe) antigravityCapabilityState {
	if !p.CLIFound && !p.BundleFound {
		return antigravityStateNotInstalled
	}
	if !p.SupportedSurfaceConfirmed {
		return antigravityStateToolUnavailable
	}
	if !p.AuthenticatedOrConfigured {
		return antigravityStateNotAuthenticated
	}
	return antigravityStateAvailable
}

// detectAntigravityCapabilityWithBundlePaths probes for Antigravity
// installation and available automation surfaces using the provided predicate
// functions and an explicit list of bundle paths to check.
//
// Accepting bundlePaths explicitly makes the function portable: tests on Linux
// can supply fake paths without depending on antigravityBundlePaths(), which
// returns nil on non-macOS platforms.
//
// It does not launch the app, perform browser automation, or read credentials.
// lookPath is exec.LookPath-compatible; bundlePathExists reports whether a
// given filesystem path exists, accepting os.Stat-compatible semantics.
//
// As of v0.21.1, Traceary ships a documented Antigravity hooks/plugin contract
// (workspace .agents/hooks.json, global ~/.gemini/config/hooks.json, and the
// packaged integrations/antigravity-plugin). A present install — the `agy` CLI
// on PATH or the app bundle — therefore confirms a supported surface. The hook
// contract needs no Traceary-side authentication and Traceary does not read
// credentials, so a present install is considered ready (available).
func detectAntigravityCapabilityWithBundlePaths(
	lookPath func(string) (string, error),
	bundlePathExists func(string) bool,
	bundlePaths []string,
) antigravityCapabilityState {
	probe := antigravityCapabilityProbe{}
	for _, binary := range []string{"agy", "antigravity"} {
		if _, err := lookPath(binary); err == nil {
			probe.CLIFound = true
			break
		}
	}
	for _, bundlePath := range bundlePaths {
		if bundlePathExists(bundlePath) {
			probe.BundleFound = true
			break
		}
	}
	if probe.CLIFound || probe.BundleFound {
		probe.SupportedSurfaceConfirmed = true
		probe.AuthenticatedOrConfigured = true
	}
	return antigravityProbeToState(probe)
}

// detectAntigravityCapability probes for Antigravity installation using the
// OS-specific bundle paths returned by antigravityBundlePaths.
func detectAntigravityCapability(
	lookPath func(string) (string, error),
	bundlePathExists func(string) bool,
) antigravityCapabilityState {
	return detectAntigravityCapabilityWithBundlePaths(lookPath, bundlePathExists, antigravityBundlePaths())
}

// buildAntigravityCapabilityCheck returns the doctor check for
// "antigravity-capability" based on the detected state.
func buildAntigravityCapabilityCheck(state antigravityCapabilityState) doctorCheck {
	const checkName = "antigravity-capability"
	switch state {
	case antigravityStateAvailable:
		return doctorCheck{
			Name:   checkName,
			Status: doctorStatusPass,
			Message: Localize(
				"Antigravity is installed and Traceary supports its public hooks/plugin contract. "+
					"Install hooks with: traceary hooks install --client antigravity (workspace .agents/hooks.json) "+
					"or --global (~/.gemini/config/hooks.json), or add the packaged integrations/antigravity-plugin. "+
					"Traceary does not read Antigravity credentials.",
				"Antigravity はインストールされており、Traceary は公開された hooks/plugin contract をサポートしています。"+
					"hook の導入: traceary hooks install --client antigravity (workspace は .agents/hooks.json) "+
					"または --global (~/.gemini/config/hooks.json)、もしくは同梱の integrations/antigravity-plugin を追加してください。"+
					"Traceary は Antigravity の認証情報を読み取りません。",
			),
		}
	case antigravityStateNotAuthenticated:
		return doctorCheck{
			Name:   checkName,
			Status: doctorStatusWarn,
			Message: Localize(
				"Antigravity is installed with a supported surface but is not authenticated or configured (not_authenticated). "+
					"Complete the required setup in Antigravity before Traceary can capture sessions. "+
					"Traceary does not read credentials; this state is detected via a supported CLI/contract check.",
				"Antigravity はサポートされたサーフェスでインストールされていますが、認証または設定が完了していません (not_authenticated)。"+
					"Traceary がセッションをキャプチャできるようになるには、Antigravity での必要な設定を完了してください。"+
					"Traceary は認証情報を読み取りません。このステートはサポートされた CLI/contract チェックで検出されます。",
			),
		}
	case antigravityStateToolUnavailable:
		return doctorCheck{
			Name:   checkName,
			Status: doctorStatusWarn,
			Message: Localize(
				"Antigravity is installed but no supported public surface was detected (tool_unavailable). "+
					"Traceary supports the public Antigravity hooks/plugin contract; reinstall the `agy` CLI or app bundle and re-run doctor.",
				"Antigravity はインストールされていますが、サポートされた公開サーフェスを検出できませんでした (tool_unavailable)。"+
					"Traceary は公開された Antigravity hooks/plugin contract をサポートしています。`agy` CLI またはアプリバンドルを再インストールし、doctor を再実行してください。",
			),
		}
	default: // antigravityStateNotInstalled
		return doctorCheck{
			Name:   checkName,
			Status: doctorStatusWarn,
			Message: Localize(
				"Antigravity is not installed (no app bundle or CLI found on PATH). "+
					"Traceary cannot capture Antigravity sessions.",
				"Antigravity がインストールされていません（アプリバンドルも PATH 上の CLI も見つかりません）。"+
					"Traceary は Antigravity セッションをキャプチャできません。",
			),
		}
	}
}

// antigravityBundleExistsFunc reports whether an Antigravity app bundle exists
// at the given path. It is a package-level var so tests can force a
// deterministic installed/not-installed state regardless of the host machine.
var antigravityBundleExistsFunc = func(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// inspectAntigravityCapability is the doctor-facing entry point. It wires
// the production path lookup (execLookPathFunc) and bundle existence check
// (antigravityBundleExistsFunc) into detectAntigravityCapability.
func inspectAntigravityCapability() doctorCheck {
	state := detectAntigravityCapability(execLookPathFunc, antigravityBundleExistsFunc)
	return buildAntigravityCapabilityCheck(state)
}
