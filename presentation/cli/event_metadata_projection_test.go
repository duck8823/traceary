package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_ListMetadataProjectionBoundsTenThousandLargeEvents(t *testing.T) {
	t.Parallel()

	metadata := make([]apptypes.EventMetadata, 10_000)
	for i := range metadata {
		metadata[i] = newCLIMetadataFixture(t, fmt.Sprintf("event-%05d", i))
	}
	full := &eventUsecaseStub{}
	metadataUsecase := &eventMetadataUsecaseStub{listMetadata: metadata}
	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(full),
		cli.WithEventMetadata(metadataUsecase),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"list", "--db-path", "/tmp/test-traceary.db", "--limit", "10000", "--json", "--fields", "id"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if full.listCalls != 0 || metadataUsecase.listCalls != 1 {
		t.Fatalf("full List() calls = %d, metadata List() calls = %d", full.listCalls, metadataUsecase.listCalls)
	}
	if stdout.Len() > 400_000 {
		t.Fatalf("metadata output bytes = %d, want <= 400000", stdout.Len())
	}
	if bytes.Contains(stdout.Bytes(), []byte("message")) || bytes.Contains(stdout.Bytes(), []byte("body")) {
		t.Fatal("metadata output contains body-bearing keys")
	}
}

func TestRootCLI_ListJSONFieldsUsesMetadataProjection(t *testing.T) {
	t.Parallel()

	metadata := newCLIMetadataFixture(t, "event-metadata")
	full := &eventUsecaseStub{}
	metadataUsecase := &eventMetadataUsecaseStub{listMetadata: []apptypes.EventMetadata{metadata}}
	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(full),
		cli.WithEventMetadata(metadataUsecase),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"list", "--db-path", "/tmp/test-traceary.db", "--json", "--fields", "id,ts,kind,exit_code"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if full.listCalls != 0 {
		t.Fatalf("full List() calls = %d, want 0", full.listCalls)
	}
	if metadataUsecase.listCalls != 1 {
		t.Fatalf("metadata List() calls = %d, want 1", metadataUsecase.listCalls)
	}
	var rows []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
	for _, forbidden := range []string{"message", "client", "agent", "session_id", "workspace", "source_hook"} {
		if _, ok := rows[0][forbidden]; ok {
			t.Fatalf("unrequested field %q was serialized: %s", forbidden, stdout.String())
		}
	}
	if rows[0]["event_id"] != "event-metadata" || rows[0]["exit_code"] != float64(17) {
		t.Fatalf("metadata row = %#v", rows[0])
	}
}

func TestRootCLI_SearchJSONFieldsUsesMetadataProjection(t *testing.T) {
	t.Setenv("TRACEARY_WORKSPACE", "")
	cli.SetDetectRepoContextFunc(func(context.Context) (string, error) { return "", nil })
	t.Cleanup(cli.ResetDetectRepoContextFunc)

	full := &eventUsecaseStub{}
	metadataUsecase := &eventMetadataUsecaseStub{searchMetadata: []apptypes.EventMetadata{newCLIMetadataFixture(t, "event-search")}}
	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(full),
		cli.WithEventMetadata(metadataUsecase),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"search", "--db-path", "/tmp/test-traceary.db", "--json", "--fields", "id,kind", "needle"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if full.searchCalls != 0 || metadataUsecase.searchCalls != 1 {
		t.Fatalf("full Search() calls = %d, metadata Search() calls = %d", full.searchCalls, metadataUsecase.searchCalls)
	}
	if bytes.Contains(stdout.Bytes(), []byte("message")) || bytes.Contains(stdout.Bytes(), []byte("body")) {
		t.Fatalf("metadata search output contains body field: %s", stdout.String())
	}
}

func TestRootCLI_ListJSONFieldsIncludingMessageUsesFullProjection(t *testing.T) {
	t.Parallel()

	full := &eventUsecaseStub{}
	metadataUsecase := &eventMetadataUsecaseStub{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(full),
		cli.WithEventMetadata(metadataUsecase),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"list", "--db-path", "/tmp/test-traceary.db", "--json", "--fields", "id,message"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if full.listCalls != 1 || metadataUsecase.listCalls != 0 {
		t.Fatalf("full List() calls = %d, metadata List() calls = %d", full.listCalls, metadataUsecase.listCalls)
	}
}

func newCLIMetadataFixture(t *testing.T, id string) apptypes.EventMetadata {
	t.Helper()
	extent, err := apptypes.EventBodyExtentOf(types.None[int](), 8*1024*1024, types.None[bool](), types.None[bool](), types.None[int]())
	if err != nil {
		t.Fatalf("EventBodyExtentOf() error = %v", err)
	}
	metadata, err := apptypes.EventMetadataOf(
		types.EventID(id),
		types.EventKindCommandExecuted,
		types.Client("cli"),
		types.Agent("codex"),
		types.SessionID("session-1"),
		types.Workspace("duck8823/traceary"),
		"post_tool_use",
		time.Date(2026, 7, 22, 6, 0, 0, 0, time.UTC),
		extent,
		types.Some(apptypes.CommandAuditMetadataOf(types.Some(17), true)),
	)
	if err != nil {
		t.Fatalf("EventMetadataOf() error = %v", err)
	}
	return metadata
}
