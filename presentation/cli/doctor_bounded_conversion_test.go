package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/presentation/cli"
)

type conversionQueryStub struct {
	calls int
	rows  []queryservice.ConsolidationConversionRow
	auth  []queryservice.RefinementAuthorshipRow
}

func (s *conversionQueryStub) ConversionSince(context.Context, time.Time) ([]queryservice.ConsolidationConversionRow, error) {
	s.calls++
	return s.rows, nil
}

func (s *conversionQueryStub) RefinementAuthorshipSince(context.Context, time.Time) ([]queryservice.RefinementAuthorshipRow, error) {
	return s.auth, nil
}

func TestDoctorLargeStoreReportsConsolidationConversion(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	setTracearyPathToCurrentExecutable(t)
	largeStore := writeValidSparseLargeStore(t)
	ledger := &conversionQueryStub{}
	store := &storeManagementUsecaseStub{}
	events := &eventUsecaseStub{}
	capacity := &panicCapacityInspector{}
	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(store),
		cli.WithEvent(events),
		cli.WithCapacityInspector(capacity),
		cli.WithConsolidationConversion(ledger),
	).Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"doctor", "--db-path", largeStore, "--json", "--warnings-ok"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	report := decodeDoctorReport(t, stdout.Bytes())
	if report.Mode != "metadata_only_large_store" {
		t.Fatalf("report.Mode = %q, want metadata_only_large_store", report.Mode)
	}
	if events.listCalls != 0 {
		t.Fatalf("large-store doctor listed %d events, want no event/payload reads", events.listCalls)
	}
	if capacity.calls != 0 {
		t.Fatalf("large-store capacity calls=%d, want zero", capacity.calls)
	}
	if store.initCalled {
		t.Fatal("large-store doctor initialized SQLite")
	}
	check := statusByName(report, "consolidation-conversion")
	if check.Name == "" {
		t.Fatalf("consolidation-conversion missing from bounded report: %+v", report.Checks)
	}
	if check.Status != "pass" {
		t.Fatalf("consolidation-conversion = %#v, want pass", check)
	}
	if !strings.Contains(check.Message, "no consolidation requests in the last 7 days") {
		t.Fatalf("consolidation-conversion message = %q", check.Message)
	}
	if ledger.calls != 1 {
		t.Fatalf("ConversionSince calls = %d, want 1 (must not scale with store size)", ledger.calls)
	}
	for _, name := range []string{
		"memory-inbox-saturation",
		"db-write",
		"audit-reliability",
	} {
		skipped := statusByName(report, name)
		if skipped.Status != "skip" {
			t.Fatalf("%s = %#v, want explicit skip on bounded path", name, skipped)
		}
	}
}
