package cli_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_ListAndSearchShareDateOnlyIntervalSemantics(t *testing.T) {
	t.Parallel()

	wantFrom := time.Date(2026, 3, 8, 5, 0, 0, 0, time.UTC)
	wantTo := time.Date(2026, 3, 9, 4, 0, 0, 0, time.UTC)

	t.Run("list", func(t *testing.T) {
		t.Parallel()
		events := &eventUsecaseStub{}
		root := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithEvent(events),
		).Command()
		root.SetOut(&bytes.Buffer{})
		root.SetErr(&bytes.Buffer{})
		root.SetArgs([]string{
			"list", "--db-path", "/tmp/test-traceary.db",
			"--from", "2026-03-08", "--to", "2026-03-08",
			"--timezone", "America/New_York",
		})
		if err := root.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		assertEffectiveInterval(t, events.listCriteria.From(), events.listCriteria.To(), wantFrom, wantTo)
	})

	t.Run("search", func(t *testing.T) {
		t.Parallel()
		events := &eventUsecaseStub{}
		root := cli.NewRootCLI(
			cli.WithStoreManagement(&storeManagementUsecaseStub{}),
			cli.WithEvent(events),
		).Command()
		root.SetOut(&bytes.Buffer{})
		root.SetErr(&bytes.Buffer{})
		root.SetArgs([]string{
			"search", "needle", "--db-path", "/tmp/test-traceary.db",
			"--from", "2026-03-08", "--to", "2026-03-08",
			"--timezone", "America/New_York",
		})
		if err := root.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		assertEffectiveInterval(t, events.eventSearchCriteria.From(), events.eventSearchCriteria.To(), wantFrom, wantTo)
	})
}

func assertEffectiveInterval(t *testing.T, gotFrom, gotTo, wantFrom, wantTo time.Time) {
	t.Helper()
	if !gotFrom.Equal(wantFrom) || !gotTo.Equal(wantTo) {
		t.Fatalf("effective interval = [%s, %s), want [%s, %s)", gotFrom, gotTo, wantFrom, wantTo)
	}
}
