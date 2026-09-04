package cli_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_SearchTwoTierJSONExposesTierOnEachHit(t *testing.T) {
	t.Setenv("TRACEARY_WORKSPACE", "")
	t.Setenv("TRACEARY_SESSION_ID", "")
	t.Setenv("TRACEARY_LANG", "en")

	event := mustGoldenEvent(
		t,
		"evt-tier",
		types.EventKindNote,
		"cli",
		"codex",
		"sess-fallback",
		"duck8823/traceary",
		"tier-label-term in the body",
		time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC),
		"",
	)
	refinement := apptypes.SearchSessionHitOf(
		types.SessionID("sess-refinement"),
		"tier-label-term in the summary",
		0,
		time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC),
	).WithTier(apptypes.SearchHitTierRefinement)
	fallbackSession := apptypes.SearchSessionHitOf(
		types.SessionID("sess-fallback"),
		"",
		0,
		time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC),
	).WithTier(apptypes.SearchHitTierFallback)
	page := apptypes.TwoTierSearchPageOf(
		[]apptypes.SearchEventHit{apptypes.SearchEventHitOf(event, apptypes.SearchHitTierFallback)},
		[]apptypes.SearchSessionHit{refinement, fallbackSession},
		apptypes.RefinementDispositionApplied,
		1,
	)

	stdout, stderr := executeTwoTierSearchJSON(t, &twoTierSearchStub{page: page}, nil)
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr)
	}
	var payload struct {
		Events   []map[string]any `json:"events"`
		Sessions []map[string]any `json:"sessions"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v\n%s", err, stdout)
	}
	if len(payload.Events) != 1 || payload.Events[0]["tier"] != "fallback" {
		t.Fatalf("events = %#v, want tier=fallback", payload.Events)
	}
	if _, ok := payload.Events[0]["event_id"]; !ok {
		t.Fatalf("events lost existing keys: %#v", payload.Events[0])
	}
	if len(payload.Sessions) != 2 {
		t.Fatalf("sessions len = %d, want 2", len(payload.Sessions))
	}
	gotTiers := []string{
		payload.Sessions[0]["tier"].(string),
		payload.Sessions[1]["tier"].(string),
	}
	if gotTiers[0] != "refinement" || gotTiers[1] != "fallback" {
		t.Fatalf("session tiers = %v", gotTiers)
	}
}

func TestRootCLI_SearchTwoTierKindNotice(t *testing.T) {
	t.Setenv("TRACEARY_WORKSPACE", "")
	t.Setenv("TRACEARY_SESSION_ID", "")
	t.Setenv("TRACEARY_LANG", "en")

	event := mustGoldenEvent(
		t,
		"evt-kind",
		types.EventKindNote,
		"cli",
		"codex",
		"sess-kind",
		"duck8823/traceary",
		"kind-term",
		time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC),
		"",
	)
	page := apptypes.TwoTierSearchPageOf(
		[]apptypes.SearchEventHit{apptypes.SearchEventHitOf(event, apptypes.SearchHitTierFallback)},
		nil,
		apptypes.RefinementDispositionKindExcluded,
		1,
	)
	_, stderr := executeTwoTierSearchJSON(t, &twoTierSearchStub{page: page}, []string{"--kind", "note"})
	if !strings.Contains(stderr.String(), "--kind") {
		t.Fatalf("missing kind notice: %q", stderr)
	}
}

func TestRootCLI_SearchTwoTierFailuresNotice(t *testing.T) {
	t.Setenv("TRACEARY_WORKSPACE", "")
	t.Setenv("TRACEARY_SESSION_ID", "")
	t.Setenv("TRACEARY_LANG", "en")

	event := mustGoldenEvent(
		t,
		"evt-fail",
		types.EventKindCommandExecuted,
		"cli",
		"codex",
		"sess-fail",
		"duck8823/traceary",
		"fail-term",
		time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC),
		"",
	)
	page := apptypes.TwoTierSearchPageOf(
		[]apptypes.SearchEventHit{apptypes.SearchEventHitOf(event, apptypes.SearchHitTierFallback)},
		nil,
		apptypes.RefinementDispositionFailuresExcluded,
		1,
	)
	_, stderr := executeTwoTierSearchJSON(t, &twoTierSearchStub{page: page}, []string{"--failures"})
	if !strings.Contains(stderr.String(), "--failures") {
		t.Fatalf("missing failures notice: %q", stderr)
	}
}

func executeTwoTierSearchJSON(t *testing.T, stub *twoTierSearchStub, extra []string) (*bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithTwoTierSearch(stub),
	).Command()
	rootCmd.SetOut(out)
	rootCmd.SetErr(errOut)
	args := []string{"search", "--db-path", "/tmp/test-traceary.db", "--json", "needle"}
	args = append(args, extra...)
	rootCmd.SetArgs(args)
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v\nstderr=%s", err, errOut)
	}
	return out, errOut
}

func TestRootCLI_SearchTwoTierPreservesEnvelopeKeys(t *testing.T) {
	t.Setenv("TRACEARY_WORKSPACE", "")
	t.Setenv("TRACEARY_LANG", "en")
	page := apptypes.TwoTierSearchPageOf(nil, nil, apptypes.RefinementDispositionApplied, 0)
	stdout, _ := executeTwoTierSearchJSON(t, &twoTierSearchStub{page: page}, nil)
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if _, ok := payload["events"]; !ok {
		t.Fatalf("missing events key: %#v", payload)
	}
	if _, ok := payload["sessions"]; !ok {
		t.Fatalf("missing sessions key: %#v", payload)
	}
	if len(payload) != 2 {
		t.Fatalf("envelope grew extra keys: %#v", payload)
	}
}
