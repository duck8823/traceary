package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_SearchKindSuppressionAnnouncement(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	t.Setenv("TRACEARY_WORKSPACE", "")
	cli.SetDetectRepoContextFunc(func(context.Context) (string, error) { return "", nil })
	defer cli.ResetDetectRepoContextFunc()

	event := mustGoldenEvent(t, "evt-kind", types.EventKindNote, "cli", "codex", "session-kind", "", "needle", time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), "")
	page := apptypes.TwoTierSearchPageOf(
		[]apptypes.SearchEventHit{apptypes.SearchEventHitOf(event, apptypes.SearchHitTierFallback)},
		nil,
		apptypes.RefinementDispositionKindExcluded,
		1,
	)

	testCases := []struct {
		name       string
		json       bool
		locale     string
		wantOutput string
		wantWarn   string
	}{
		{
			name:       "text",
			locale:     "en",
			wantOutput: "needle",
			wantWarn:   "traceary: matching sessions were suppressed because --kind cannot be applied to session summaries. Run the same search without --kind to see them.\n",
		},
		{
			name:       "json does not repeat the JSON shape notice",
			json:       true,
			locale:     "en",
			wantOutput: "evt-kind",
			wantWarn:   "traceary: matching sessions were suppressed because --kind cannot be applied to session summaries. Run the same search without --kind to see them.\n",
		},
		{
			name:       "Japanese",
			locale:     "ja",
			wantOutput: "needle",
			wantWarn:   "traceary: セッション要約には --kind を適用できないため、一致したセッションを表示していません。--kind を外して同じ検索を実行すると確認できます。\n",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TRACEARY_LANG", tc.locale)
			args := []string{"search", "--db-path", "/tmp/test-traceary.db", "--kind", "note", "needle"}
			if tc.json {
				args = append(args[:len(args)-1], "--json", args[len(args)-1])
			}
			stdout, stderr := executeSearchWithTwoTier(t, args, &twoTierSearchStub{page: page})
			if !strings.Contains(string(stdout), tc.wantOutput) {
				t.Fatalf("stdout = %q, want substring %q", stdout, tc.wantOutput)
			}
			if diff := cmp.Diff(tc.wantWarn, string(stderr)); diff != "" {
				t.Fatalf("stderr (-want +got):\n%s", diff)
			}
		})
	}
}

func TestRootCLI_SearchKindSuppressionNoFalseAnnouncement(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	page := apptypes.TwoTierSearchPageOf(nil, nil, apptypes.RefinementDispositionKindExcluded, 0)
	_, stderr := executeSearchWithTwoTier(t, []string{"search", "--db-path", "/tmp/test-traceary.db", "--kind", "note", "needle"}, &twoTierSearchStub{page: page})
	if diff := cmp.Diff("", string(stderr)); diff != "" {
		t.Fatalf("empty kind match count must be silent (-want +got):\n%s", diff)
	}
}

func TestRootCLI_SearchKindFilterDoesNotProbeProjection(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	t.Setenv("TRACEARY_WORKSPACE", "")
	cli.SetDetectRepoContextFunc(func(context.Context) (string, error) { return "", nil })
	defer cli.ResetDetectRepoContextFunc()

	event := mustGoldenEvent(t, "evt-kind", types.EventKindNote, "cli", "codex", "session-kind", "", "needle", time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), "")
	page := apptypes.TwoTierSearchPageOf(
		[]apptypes.SearchEventHit{apptypes.SearchEventHitOf(event, apptypes.SearchHitTierFallback)},
		nil,
		apptypes.RefinementDispositionKindExcluded,
		1,
	)
	_, stderr := executeSearchWithTwoTier(t, []string{
		"search", "--db-path", "/tmp/test-traceary.db", "--kind", "note", "needle",
	}, &twoTierSearchStub{page: page})
	if !strings.Contains(string(stderr), "--kind") {
		t.Fatalf("stderr = %q, want the kind suppression notice", stderr)
	}
}

func executeSearchWithTwoTier(t *testing.T, args []string, stub *twoTierSearchStub) ([]byte, []byte) {
	t.Helper()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	root := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithTwoTierSearch(stub),
	).Command()
	root.SetOut(out)
	root.SetErr(errOut)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	return out.Bytes(), errOut.Bytes()
}
