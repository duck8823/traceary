package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/presentation/cli"
)

type reportUsecaseStub struct {
	criteria apptypes.ReportCriteria
	result   apptypes.ReportSnapshot
	err      error
}

func (s *reportUsecaseStub) Generate(_ context.Context, criteria apptypes.ReportCriteria) (apptypes.ReportSnapshot, error) {
	s.criteria = criteria
	if s.result.Period.Timezone == "" {
		s.result = reportSnapshotForCriteria(criteria)
	}
	return s.result, s.err
}

func TestRootCLI_Report_JSONUsesSharedSnapshot(t *testing.T) {
	t.Parallel()
	stub := &reportUsecaseStub{}
	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithReport(stub),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"report", "--db-path", "/tmp/test-traceary.db",
		"--from", "2026-07-01", "--to", "2026-07-16", "--json",
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	var got apptypes.ReportSnapshot
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got.Period.RequestedFrom != "2026-07-01" || got.Period.RequestedTo != "2026-07-16" {
		t.Fatalf("requested period = %+v", got.Period)
	}
	if got.Period.EffectiveFromInclusive != "2026-07-01T00:00:00Z" || got.Period.EffectiveToExclusive != "2026-07-17T00:00:00Z" {
		t.Fatalf("effective period = %+v", got.Period)
	}
	if got.Aggregation.Coverage != apptypes.ReportCoverageComplete || got.Aggregation.PageSize != 5000 || got.Aggregation.ResultCap != 0 {
		t.Fatalf("aggregation = %+v", got.Aggregation)
	}
	if stub.criteria.PageSize() != 5000 || stub.criteria.ResultCap() != 0 {
		t.Fatalf("criteria page=%d cap=%d", stub.criteria.PageSize(), stub.criteria.ResultCap())
	}
}

func TestRootCLI_Report_JSONGolden(t *testing.T) {
	t.Parallel()
	extent := apptypes.ReportSourceExtent{
		Coverage: apptypes.ReportCoveragePartial, ObservedCount: 1, PageSize: 2, ResultCap: 1,
		ResponseTruncated: true, TruncationReason: "result_cap",
		ObservedEarliestAt: "2026-07-01T01:00:00Z", ObservedLatestAt: "2026-07-01T01:00:00Z",
	}
	stub := &reportUsecaseStub{result: apptypes.ReportSnapshot{
		Period: apptypes.ReportPeriod{
			From: "2026-07-01T00:00:00Z", To: "2026-07-02T00:00:00Z",
			RequestedFrom: "2026-07-01", RequestedTo: "2026-07-01",
			EffectiveFromInclusive: "2026-07-01T00:00:00Z", EffectiveToExclusive: "2026-07-02T00:00:00Z",
			Timezone: "UTC", SnapshotAt: "2026-07-02T12:00:00Z", FromDateOnly: true, ToDateOnly: true,
		},
		Aggregation: apptypes.ReportAggregation{
			Coverage: apptypes.ReportCoveragePartial, PageSize: 2, ResultCap: 1,
			Sources: apptypes.ReportSourceExtents{Sessions: extent, Events: extent, Commands: extent},
		},
		Workspace: "workspace", ClientFilter: "codex",
		Sessions:        []apptypes.ReportSessionRow{{Client: "codex", Sessions: 1, TotalEvents: 2, CommandCount: 1}},
		CaptureCoverage: []apptypes.ReportCoverageRow{{Client: "codex", Sessions: 1, WithPrompt: 1, PromptTranscriptMissing: 1}},
		Failures: apptypes.ReportFailures{
			Total: 1, ByClient: map[string]int{"codex": 1}, ByReason: map[string]int{"exit_code": 1}, Samples: []string{"event-1"},
		},
		TopCommands:      []apptypes.ReportCommandOutput{{Command: "go", Count: 1, FailedCount: 1, SampleEventID: "event-1"}},
		FailureLoops:     []apptypes.ReportFailureLoopOutput{},
		EventScanCount:   1,
		SessionScanCount: 1,
	}}
	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{}), cli.WithReport(stub)).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"report", "--db-path", "/tmp/test-traceary.db", "--from", "2026-07-01", "--to", "2026-07-01",
		"--workspace", "workspace", "--client", "codex", "--page-size", "2", "--result-cap", "1", "--json",
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	assertJSONGolden(t, stdout.Bytes(), filepath.Join("testdata", "report", "partial.golden.json"))
}

func TestRootCLI_Report_DefaultWindowPreservesOmittedRequestedBounds(t *testing.T) {
	t.Parallel()
	stub := &reportUsecaseStub{}
	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{}), cli.WithReport(stub)).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"report", "--db-path", "/tmp/test-traceary.db", "--json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	interval := stub.criteria.Interval()
	if interval.HasRequestedFrom() || interval.HasRequestedTo() {
		t.Fatalf("requested bounds = [%q, %q), want omitted", interval.RequestedFrom(), interval.RequestedTo())
	}
	if !interval.EffectiveToExclusive().Equal(interval.SnapshotAt()) {
		t.Fatalf("effective to = %s, snapshot = %s", interval.EffectiveToExclusive(), interval.SnapshotAt())
	}
	if got := interval.EffectiveToExclusive().Sub(interval.EffectiveFromInclusive()); got != 7*24*time.Hour {
		t.Fatalf("default window = %s, want 168h", got)
	}
}

func TestRootCLI_Report_LegacyPeriodFieldsKeepSecondPrecision(t *testing.T) {
	t.Parallel()
	stub := &reportUsecaseStub{}
	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{}), cli.WithReport(stub)).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"report", "--db-path", "/tmp/test-traceary.db", "--json",
		"--from", "2026-07-01T00:00:00.123Z", "--to", "2026-07-02T00:00:00.456Z",
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	var got apptypes.ReportSnapshot
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got.Period.From != "2026-07-01T00:00:00Z" || got.Period.To != "2026-07-02T00:00:00Z" {
		t.Fatalf("legacy period = [%q, %q)", got.Period.From, got.Period.To)
	}
	if got.Period.EffectiveFromInclusive != "2026-07-01T00:00:00.123Z" || got.Period.EffectiveToExclusive != "2026-07-02T00:00:00.456Z" {
		t.Fatalf("effective period = [%q, %q)", got.Period.EffectiveFromInclusive, got.Period.EffectiveToExclusive)
	}
}

func TestRootCLI_Report_TextPreservesRequestedCalendarEnd(t *testing.T) {
	t.Parallel()
	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{}), cli.WithReport(&reportUsecaseStub{})).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"report", "--db-path", "/tmp/test-traceary.db",
		"--from", "2026-03-08", "--to", "2026-03-08", "--timezone", "America/New_York",
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "Period: 2026-03-08 → 2026-03-08 (inclusive calendar end; timezone=America/New_York)") {
		t.Fatalf("text must preserve requested calendar end: %s", out)
	}
	if !strings.Contains(out, "Effective interval: 2026-03-08T05:00:00Z → 2026-03-09T04:00:00Z") {
		t.Fatalf("text missing DST-safe effective interval: %s", out)
	}
}

func TestRootCLI_Report_LegacyLimitMapsOnlyToPageSize(t *testing.T) {
	t.Parallel()
	stub := &reportUsecaseStub{}
	rootCmd := cli.NewRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{}), cli.WithReport(stub)).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"report", "--db-path", "/tmp/test-traceary.db", "--limit", "7", "--json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if stub.criteria.PageSize() != 7 || stub.criteria.ResultCap() != 0 {
		t.Fatalf("legacy limit mapped to page=%d cap=%d", stub.criteria.PageSize(), stub.criteria.ResultCap())
	}
}

func TestRootCLI_Report_RejectsLegacyLimitWithPageSize(t *testing.T) {
	t.Parallel()
	rootCmd := cli.NewRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{}), cli.WithReport(&reportUsecaseStub{})).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"report", "--limit", "7", "--page-size", "8"})
	if err := rootCmd.Execute(); err == nil || !strings.Contains(err.Error(), "cannot be used together") {
		t.Fatalf("Execute() error = %v, want conflict", err)
	}
}

func TestRootCLI_Report_PartialTextDoesNotPrintRates(t *testing.T) {
	t.Parallel()
	stub := &reportUsecaseStub{result: apptypes.ReportSnapshot{
		Period:          apptypes.ReportPeriod{Timezone: "UTC", EffectiveFromInclusive: "2026-07-01T00:00:00Z", EffectiveToExclusive: "2026-07-02T00:00:00Z", SnapshotAt: "2026-07-02T00:00:00Z"},
		Aggregation:     apptypes.ReportAggregation{Coverage: apptypes.ReportCoveragePartial, PageSize: 10, ResultCap: 1},
		CaptureCoverage: []apptypes.ReportCoverageRow{{Client: "codex", Sessions: 1, PromptTranscriptMissingRatio: nil}},
		TopCommands:     []apptypes.ReportCommandOutput{{Command: "go", Count: 1, FailedCount: 1, FailureRate: nil}},
		Failures:        apptypes.ReportFailures{ByClient: map[string]int{}, ByReason: map[string]int{}},
	}}
	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{}), cli.WithReport(stub)).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"report", "--db-path", "/tmp/test-traceary.db"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if strings.Count(stdout.String(), "unavailable(partial)") != 2 || strings.Contains(stdout.String(), "rate=0.00") {
		t.Fatalf("partial text must not print numeric rates: %s", stdout.String())
	}
}

func reportSnapshotForCriteria(criteria apptypes.ReportCriteria) apptypes.ReportSnapshot {
	interval := criteria.Interval()
	formatNano := func(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }
	formatSeconds := func(value time.Time) string { return value.UTC().Format(time.RFC3339) }
	emptyExtent, _ := apptypes.ReportSourceExtentOf(nil, criteria.PageSize(), criteria.ResultCap(), false)
	return apptypes.ReportSnapshot{
		Period: apptypes.ReportPeriod{
			From: formatSeconds(interval.EffectiveFromInclusive()), To: formatSeconds(interval.EffectiveToExclusive()),
			RequestedFrom: interval.RequestedFrom(), RequestedTo: interval.RequestedTo(),
			EffectiveFromInclusive: formatNano(interval.EffectiveFromInclusive()), EffectiveToExclusive: formatNano(interval.EffectiveToExclusive()),
			Timezone: interval.Timezone(), SnapshotAt: formatNano(interval.SnapshotAt()),
			FromDateOnly: interval.FromIsDateOnly(), ToDateOnly: interval.ToIsDateOnly(),
		},
		Aggregation: apptypes.ReportAggregation{
			Coverage: apptypes.ReportCoverageComplete, PageSize: criteria.PageSize(), ResultCap: criteria.ResultCap(),
			Sources: apptypes.ReportSourceExtents{Sessions: emptyExtent, Events: emptyExtent, Commands: emptyExtent},
		},
		Workspace: criteria.Workspace().String(), ClientFilter: criteria.Client().String(),
		Sessions: []apptypes.ReportSessionRow{}, CaptureCoverage: []apptypes.ReportCoverageRow{},
		Failures:    apptypes.ReportFailures{ByClient: map[string]int{}, ByReason: map[string]int{}, Samples: []string{}},
		TopCommands: []apptypes.ReportCommandOutput{},
	}
}
