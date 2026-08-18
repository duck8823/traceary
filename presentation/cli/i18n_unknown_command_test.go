package cli

import (
	"errors"
	"strings"
	"testing"

	"golang.org/x/xerrors"
)

func TestLocalizeCobraExecuteError_JapaneseUnknownCommandKeepsSuggestions(t *testing.T) {
	t.Setenv(cliLanguageEnvKey, "ja")
	resetConfiguredCLILanguageCacheForTest()
	t.Cleanup(resetConfiguredCLILanguageCacheForTest)

	err := LocalizeCobraExecuteError(errors.New("unknown command \"nosuchcmd\" for \"traceary\"\n\nDid you mean this?\n\tsearch"))
	if err == nil {
		t.Fatal("LocalizeCobraExecuteError() = nil")
	}
	got := err.Error()
	if strings.Contains(got, "unknown command") {
		t.Fatalf("error still English: %q", got)
	}
	if !strings.Contains(got, "不明なコマンド") || !strings.Contains(got, "nosuchcmd") {
		t.Fatalf("error = %q, want Japanese unknown-command text", got)
	}
	if !strings.Contains(got, "Did you mean this?") || !strings.Contains(got, "search") {
		t.Fatalf("error = %q, want cobra suggestion list kept", got)
	}
}

func TestLocalizeCobraExecuteError_LeavesOtherErrors(t *testing.T) {
	in := xerrors.New("database query failed")
	if got := LocalizeCobraExecuteError(in); got != in {
		t.Fatalf("LocalizeCobraExecuteError() = %v, want original", got)
	}
}

func TestStoreCompactIndexFamilyBytesHelpIsJapanese(t *testing.T) {
	t.Setenv(cliLanguageEnvKey, "ja")
	resetConfiguredCLILanguageCacheForTest()
	t.Cleanup(resetConfiguredCLILanguageCacheForTest)

	root := NewRootCLI().Command()
	compact := findCommandOrNil(findCommandOrNil(root, "store"), "compact")
	if compact == nil {
		t.Fatal("store compact is not registered")
	}
	flag := compact.Flags().Lookup("index-family-bytes")
	if flag == nil {
		t.Fatal("--index-family-bytes is missing")
	}
	if strings.Contains(flag.Usage, "steady-state physical byte target") {
		t.Fatalf("usage still English-only: %q", flag.Usage)
	}
	if !strings.Contains(flag.Usage, "物理バイト") {
		t.Fatalf("usage=%q, want Japanese description", flag.Usage)
	}
}
