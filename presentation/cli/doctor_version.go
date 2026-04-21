package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

func checkLatestVersion(currentVersion string) doctorCheck {
	if currentVersion == "" {
		return doctorCheck{
			Name:    "version",
			Status:  doctorStatusSkip,
			Message: Localize("version check skipped: current version unknown", "バージョンチェックをスキップ: 現在のバージョンが不明"),
		}
	}

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("https://api.github.com/repos/duck8823/traceary/releases/latest")
	if err != nil {
		return doctorCheck{
			Name:    "version",
			Status:  doctorStatusSkip,
			Message: Localize("version check skipped: network unavailable", "バージョンチェックをスキップ: ネットワーク不通"),
		}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return doctorCheck{
			Name:    "version",
			Status:  doctorStatusSkip,
			Message: Localize("version check skipped: GitHub API unavailable", "バージョンチェックをスキップ: GitHub API 不通"),
		}
	}

	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return doctorCheck{
			Name:    "version",
			Status:  doctorStatusSkip,
			Message: Localize("version check skipped: failed to parse response", "バージョンチェックをスキップ: レスポンス解析失敗"),
		}
	}

	latestVersion := strings.TrimPrefix(release.TagName, "v")
	normalizedCurrent := strings.TrimPrefix(currentVersion, "v")
	// Strip build metadata: "0.2.4 (commit=..., date=..., go=...)" → "0.2.4"
	if idx := strings.IndexAny(normalizedCurrent, " +"); idx >= 0 {
		normalizedCurrent = normalizedCurrent[:idx]
	}

	return compareTracearyVersion(normalizedCurrent, latestVersion)
}

// compareTracearyVersion compares the running binary's semver-ish
// version string (which may be a Go pseudo-version such as
// `0.7.3-0.20260420223154-9a43e0847edd`) against the latest release
// tag and renders a doctor check. Pseudo-versions for X.Y.Z-0.* sort
// newer than release X.Y.(Z-1) but older than release X.Y.Z, so a
// main build whose prefix already exceeds the latest release must be
// reported as "ahead of the latest release" instead of telling the
// operator to `brew upgrade` (which would downgrade the binary).
func compareTracearyVersion(currentVersion, latestVersion string) doctorCheck {
	currentSemver := "v" + currentVersion
	latestSemver := "v" + latestVersion

	if !semver.IsValid(currentSemver) || !semver.IsValid(latestSemver) {
		// Fall back to string equality when either side is not parseable
		// as semver. This keeps the legacy behaviour for exotic build
		// strings instead of silently claiming "up to date".
		if currentVersion == latestVersion {
			return doctorCheck{
				Name:    "version",
				Status:  doctorStatusPass,
				Message: localizef("traceary is up to date: %s", "traceary は最新です: %s", currentVersion),
			}
		}
		return doctorCheck{
			Name:   "version",
			Status: doctorStatusWarn,
			Message: localizef(
				"update available: %s → %s (run: brew upgrade traceary)",
				"更新があります: %s → %s (実行: brew upgrade traceary)",
				currentVersion, latestVersion,
			),
		}
	}

	comparison := semver.Compare(currentSemver, latestSemver)
	switch {
	case comparison > 0:
		return doctorCheck{
			Name:   "version",
			Status: doctorStatusPass,
			Message: localizef(
				"traceary is a development build newer than the latest release: %s > %s",
				"traceary は最新リリースより新しい development build です: %s > %s",
				currentVersion, latestVersion,
			),
		}
	case comparison == 0:
		return doctorCheck{
			Name:    "version",
			Status:  doctorStatusPass,
			Message: localizef("traceary is up to date: %s", "traceary は最新です: %s", currentVersion),
		}
	default:
		return doctorCheck{
			Name:   "version",
			Status: doctorStatusWarn,
			Message: localizef(
				"update available: %s → %s (run: brew upgrade traceary)",
				"更新があります: %s → %s (実行: brew upgrade traceary)",
				currentVersion, latestVersion,
			),
		}
	}
}
