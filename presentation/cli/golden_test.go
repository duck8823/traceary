package cli_test

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

var updateGolden = flag.Bool("update", false, "update golden test fixtures")

func assertJSONGolden(t *testing.T, got []byte, fixturePath string) {
	t.Helper()
	assertGolden(t, got, fixturePath)
}

func assertNDJSONGolden(t *testing.T, got []byte, fixturePath string) {
	t.Helper()
	assertGolden(t, got, fixturePath)
}

func assertGolden(t *testing.T, got []byte, fixturePath string) {
	t.Helper()

	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(fixturePath), 0o755); err != nil {
			t.Fatalf("create golden fixture directory %q: %v", filepath.Dir(fixturePath), err)
		}
		if err := os.WriteFile(fixturePath, got, 0o644); err != nil {
			t.Fatalf("update golden fixture %q: %v", fixturePath, err)
		}
	}

	want, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read golden fixture %q: %v", fixturePath, err)
	}
	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Fatalf("golden fixture mismatch %q (-want +got):\n%s", fixturePath, diff)
	}
}

func TestGoldenHarness_NDJSONHelper(t *testing.T) {
	fixturePath := filepath.Join(t.TempDir(), "events.ndjson.golden")
	got := []byte("{\"event_id\":\"event-1\"}\n")
	if err := os.WriteFile(fixturePath, got, 0o644); err != nil {
		t.Fatalf("write NDJSON fixture: %v", err)
	}

	assertNDJSONGolden(t, got, fixturePath)
}

func TestEventShow_JSON_Golden(t *testing.T) {
	eventID, err := types.EventIDFrom("event-golden-1")
	if err != nil {
		t.Fatalf("EventIDFrom() error = %v", err)
	}
	agent, err := types.AgentFrom("codex")
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	sessionID, err := types.SessionIDFrom("session-golden-1")
	if err != nil {
		t.Fatalf("SessionIDFrom() error = %v", err)
	}
	eventDetails, err := apptypes.EventDetailsOf(
		model.EventOf(
			eventID,
			types.EventKindCommandExecuted,
			"cli",
			agent,
			sessionID,
			"duck8823/traceary",
			"go test ./presentation/cli",
			time.Date(2026, 4, 8, 12, 30, 0, 0, time.UTC),
		),
		types.Some(model.CommandAuditOf(
			eventID,
			"go test ./presentation/cli",
			"stdin",
			"stdout",
			false,
			false,
			types.Some(0),
		)),
	)
	if err != nil {
		t.Fatalf("EventDetailsOf() error = %v", err)
	}

	stdout := &bytes.Buffer{}
	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(&eventUsecaseStub{showDetails: eventDetails}),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"show", "--db-path", "/tmp/test-traceary.db", "--json", "event-golden-1"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	assertJSONGolden(t, stdout.Bytes(), filepath.Join("testdata", "event_show.json.golden"))
}
