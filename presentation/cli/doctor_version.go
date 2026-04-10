package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
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

	if normalizedCurrent == latestVersion {
		return doctorCheck{
			Name:    "version",
			Status:  doctorStatusPass,
			Message: localizef("traceary is up to date: %s", "traceary は最新です: %s", normalizedCurrent),
		}
	}

	return doctorCheck{
		Name:   "version",
		Status: doctorStatusWarn,
		Message: localizef(
			"update available: %s → %s (run: brew upgrade traceary)",
			"更新があります: %s → %s (実行: brew upgrade traceary)",
			normalizedCurrent, latestVersion,
		),
	}
}
