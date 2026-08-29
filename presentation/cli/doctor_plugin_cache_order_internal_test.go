package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/duck8823/traceary/application"
)

type stubMultiVersionPluginCacheInspector struct {
	status application.PluginCacheStatus
}

func (s stubMultiVersionPluginCacheInspector) DetectClaudePluginCacheStatus(string, string) application.PluginCacheStatus {
	return s.status
}

type stubActiveClaudePluginDetector struct {
	detection application.ClaudePluginDetection
}

func (s stubActiveClaudePluginDetector) DetectClaudeTracearyPluginIn(string) application.ClaudePluginDetection {
	return s.detection
}

func (stubActiveClaudePluginDetector) ListClaudeLocalPluginLeftovers(string) (application.ClaudeLocalLeftoverScan, error) {
	return application.ClaudeLocalLeftoverScan{}, nil
}

func TestInspectClaudePluginCacheWarnsRestartBeforeRemoveInBothLocales(t *testing.T) {
	home := t.TempDir()
	SetUserHomeDirFunc(func() (string, error) { return home, nil })
	t.Cleanup(ResetUserHomeDirFunc)

	root := &RootCLI{
		pluginDetector: stubActiveClaudePluginDetector{
			detection: application.ClaudePluginDetection{
				Active:       true,
				SettingsPath: filepath.Join(home, ".claude", "settings.json"),
				PluginKey:    "traceary@traceary-plugins",
			},
		},
		pluginCacheInspector: stubMultiVersionPluginCacheInspector{
			status: application.PluginCacheStatus{
				CachePath:          filepath.Join(home, ".claude", "plugins", "cache", "traceary-plugins", "traceary"),
				CachedVersion:      "0.48.1",
				CachedVersions:     []string{"0.48.1", "0.48.0"},
				MarketplaceVersion: "0.48.1",
				MarketplacePath:    filepath.Join(home, ".claude", "plugins", "marketplaces", "traceary-plugins", "plugins", "traceary", "plugin.json"),
			},
		},
	}

	for _, lang := range []string{"en", "ja"} {
		t.Run(lang, func(t *testing.T) {
			t.Setenv("TRACEARY_LANG", lang)
			resetConfiguredCLILanguageCacheForTest()
			check := root.inspectClaudePluginCacheStatus()
			if check == nil {
				t.Fatal("check = nil, want multi-version WARN")
			}
			if check.Name != "claude-plugin-cache" {
				t.Fatalf("name = %q", check.Name)
			}
			if check.Status != doctorStatusWarn {
				t.Fatalf("status = %q, want warn; message=%q", check.Status, check.Message)
			}
			msg := check.Message
			if strings.Contains(strings.ToLower(msg), "optionally remove") {
				t.Fatalf("message still presents removal as optional tidy-up: %q", msg)
			}
			for _, needle := range pluginCacheRestartBeforeRemoveNeedles(lang) {
				if !strings.Contains(msg, needle) {
					t.Fatalf("message missing %q (%s): %q", needle, lang, msg)
				}
			}
			restartIdx, removeIdx := pluginCacheRestartAndRemoveIndexes(msg, lang)
			if restartIdx < 0 {
				t.Fatalf("message missing restart constraint (%s): %q", lang, msg)
			}
			if removeIdx < 0 {
				t.Fatalf("message missing remove advice (%s): %q", lang, msg)
			}
			if restartIdx > removeIdx {
				t.Fatalf("remove appears before restart (%s): %q", lang, msg)
			}
		})
	}
}

func pluginCacheRestartBeforeRemoveNeedles(lang string) []string {
	if lang == "ja" {
		return []string{
			"先に再起動",
			"そのあと",
			"実行中の session の下からディレクトリを消すと hook が壊れます",
		}
	}
	return []string{
		"restart every host session that could hold the old snapshot first",
		"only after those sessions have been restarted, remove",
		"Removing the directory while a session is running breaks that session's hooks",
	}
}

func pluginCacheRestartAndRemoveIndexes(msg, lang string) (restartIdx, removeIdx int) {
	if lang == "ja" {
		return strings.Index(msg, "再起動"), strings.Index(msg, "削除")
	}
	lower := strings.ToLower(msg)
	return strings.Index(lower, "restart"), strings.Index(lower, "remove")
}

func TestPostUpgradePluginDocsStateRestartBeforeRemoveInBothLocales(t *testing.T) {
	t.Parallel()
	pairs := []struct {
		rel    string
		lang   string
		want   []string
		forbid []string
	}{
		{
			rel:  filepath.Join("..", "..", "docs", "release", "post-upgrade-plugins.md"),
			lang: "en",
			want: []string{
				"restart every host session that could hold the old snapshot first",
				"remove the older cache directory",
				"If you already removed an older snapshot from under a live session, restart the host to recover",
			},
			forbid: []string{"optionally remove"},
		},
		{
			rel:  filepath.Join("..", "..", "docs", "release", "post-upgrade-plugins.ja.md"),
			lang: "ja",
			want: []string{
				"古いスナップショットを保持している可能性のあるホスト session をすべて先に再起動",
				"そのあとで古い cache ディレクトリを削除",
				"実行中の session の下から古いスナップショットを既に削除してしまった場合は、ホストを再起動して復旧",
			},
			forbid: []string{"optionally remove"},
		},
	}
	for _, tt := range pairs {
		t.Run(tt.lang, func(t *testing.T) {
			t.Parallel()
			body, err := os.ReadFile(tt.rel)
			if err != nil {
				t.Fatalf("ReadFile(%s) error = %v", tt.rel, err)
			}
			text := string(body)
			for _, needle := range tt.want {
				if !strings.Contains(text, needle) {
					t.Fatalf("%s missing %q", tt.rel, needle)
				}
			}
			for _, needle := range tt.forbid {
				if strings.Contains(strings.ToLower(text), needle) {
					t.Fatalf("%s still contains %q", tt.rel, needle)
				}
			}
			restartIdx, removeIdx := pluginCacheRestartAndRemoveIndexes(text, tt.lang)
			if restartIdx < 0 || removeIdx < 0 {
				t.Fatalf("%s missing restart or remove wording", tt.rel)
			}
			if restartIdx > removeIdx {
				t.Fatalf("%s mentions remove before restart", tt.rel)
			}
		})
	}
}
