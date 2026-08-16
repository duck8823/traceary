package cli

import "context"

// nativeHostPackageChecks probes and builds the native plugin activation
// checks for hosts that ship a Traceary plugin distribution with its own
// probe (grok, kimi). It reads only host manifests/caches and host CLI
// output; it never touches the Traceary store. handled is false for any
// other client, so callers can tell "not a native host" apart from "native
// host with zero checks".
func (c *RootCLI) nativeHostPackageChecks(ctx context.Context, client, projectDir, currentVersion string) (checks []doctorCheck, handled bool) {
	switch client {
	case "grok":
		state, probeErr := probeGrokDoctorState(ctx, projectDir)
		if probeErr != nil {
			return []doctorCheck{{
				Name:    "grok-inspect",
				Status:  doctorStatusWarn,
				Message: localizef("failed to inspect Grok installation: %v", "Grok installation の検査に失敗しました: %v", probeErr),
				Hint:    Localize("run `grok inspect --json` and retry doctor after resolving the host error", "`grok inspect --json` のエラーを解消してから doctor を再実行してください"),
			}}, true
		}
		return buildGrokDoctorChecks(state, currentVersion), true
	case "kimi":
		state, probeErr := probeKimiDoctorState(ctx, projectDir)
		if probeErr != nil {
			// probeKimiDoctorState renders path-free errors only; keep the
			// remediation generic rather than echoing host internals.
			return []doctorCheck{{
				Name:    "kimi-inspect",
				Status:  doctorStatusWarn,
				Message: localizef("failed to inspect the Kimi plugin installation: %v", "Kimi plugin の導入状態を検査できませんでした: %v", probeErr),
				Hint:    Localize("reinstall the native Traceary Kimi plugin with scripts/install-kimi-plugin.sh, then rerun doctor", "scripts/install-kimi-plugin.sh で native Traceary Kimi plugin を再インストールしてから doctor を再実行してください"),
			}}, true
		}
		return buildKimiDoctorChecks(state, currentVersion), true
	default:
		return nil, false
	}
}

// hostPackageIdentityChecks produces the host package identity family: the
// installed package version for every host manifest/cache
// (inspectPluginVersionChecks) plus the native grok/kimi activation checks
// for the resolved clients. It is store-independent by construction — every
// call it makes reads host manifests, host plugin caches, or host CLI probes
// only — so it stays available in the bounded (large-store) doctor report.
func (c *RootCLI) hostPackageIdentityChecks(ctx context.Context, clients []string, projectDir, currentVersion string) []doctorCheck {
	checks := make([]doctorCheck, 0, len(clients)+4)
	for _, client := range clients {
		nativeChecks, handled := c.nativeHostPackageChecks(ctx, client, projectDir, currentVersion)
		if !handled {
			continue
		}
		checks = append(checks, nativeChecks...)
	}
	checks = append(checks, c.inspectPluginVersionChecks(currentVersion)...)
	return checks
}
