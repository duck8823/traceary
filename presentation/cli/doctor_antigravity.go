package cli

import (
	"os"
	"runtime"
)

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
// As of v0.21.0, no supported public CLI/hook contract for Antigravity is
// confirmed, so SupportedSurfaceConfirmed is always false and even a
// PATH-resolvable binary yields tool_unavailable.
func detectAntigravityCapabilityWithBundlePaths(
	lookPath func(string) (string, error),
	bundlePathExists func(string) bool,
	bundlePaths []string,
) antigravityCapabilityState {
	probe := antigravityCapabilityProbe{}
	if _, err := lookPath("antigravity"); err == nil {
		probe.CLIFound = true
	}
	for _, bundlePath := range bundlePaths {
		if bundlePathExists(bundlePath) {
			probe.BundleFound = true
			break
		}
	}
	// No supported public hook/automation contract is confirmed for Antigravity
	// in v0.21.0; SupportedSurfaceConfirmed stays false.
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
				"Antigravity CLI/hook contract is available and configured.",
				"Antigravity CLI/hook contract は利用可能で設定済みです。",
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
				"Antigravity app is installed but no supported public headless/hook/package surface is confirmed (tool_unavailable). "+
					"Traceary cannot capture Antigravity sessions until Google exposes a supported CLI/hook contract. "+
					"Hook and package implementation is tracked in #1196.",
				"Antigravity アプリはインストールされていますが、公開済みの headless/hook/package サーフェスは確認されていません (tool_unavailable)。"+
					"Google が CLI/hook contract を公開するまで、Traceary は Antigravity セッションをキャプチャできません。"+
					"hook / package 実装は #1196 で追跡中です。",
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

// inspectAntigravityCapability is the doctor-facing entry point. It wires
// the production path lookup (execLookPathFunc) and bundle existence check
// (os.Stat) into detectAntigravityCapability.
func inspectAntigravityCapability() doctorCheck {
	bundleExists := func(path string) bool {
		_, err := os.Stat(path)
		return err == nil
	}
	state := detectAntigravityCapability(execLookPathFunc, bundleExists)
	return buildAntigravityCapabilityCheck(state)
}
