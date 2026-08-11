package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/google/go-cmp/cmp"
)

func TestRetentionNetChangeDisplaysDifferenceForLargerAndSmallerCandidates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		current   string
		projected string
		want      string
	}{
		{name: "candidate exceeds marker", current: "44", projected: "37", want: "7"},
		{name: "candidate is smaller than marker", current: "9", projected: "37", want: "-28"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := retentionNetChange(test.current, test.projected)
			if err != nil {
				t.Fatalf("retentionNetChange() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("retentionNetChange() = %q, want %q", got, test.want)
			}
			if got == test.current || got == test.projected {
				t.Fatalf("displayed figure reused a stored extent: %q", got)
			}
		})
	}
}

type rawBodyRetentionStub struct {
	plan []byte
}

type retentionStoreStub struct {
	minimalStoreStub
	initCalled bool
}

func (s *retentionStoreStub) Initialize(context.Context) error {
	s.initCalled = true
	return nil
}

func (s *rawBodyRetentionStub) CreatePlan(context.Context, time.Time, string, time.Time) ([]byte, error) {
	return s.plan, nil
}

func (*rawBodyRetentionStub) Apply(context.Context, []byte, string, string, time.Time) (apptypes.RawBodyApplyResult, error) {
	return apptypes.RawBodyApplyResult{}, nil
}

func (*rawBodyRetentionStub) Restore(context.Context, []byte, string, string, time.Time) (apptypes.RawBodyRestoreResult, error) {
	return apptypes.RawBodyRestoreResult{}, nil
}

func TestStoreRetentionPlanDoesNotInitializeOrMigrateStore(t *testing.T) {
	t.Parallel()

	store := &retentionStoreStub{}
	root := NewRootCLI(
		WithStoreManagement(store),
		WithRawBodyRetention(&rawBodyRetentionStub{plan: []byte(`{"plan_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)}),
	)
	dir := t.TempDir()
	outputPath := filepath.Join(dir, "plan.json")
	if err := root.runStoreRetentionPlan(context.Background(), &bytes.Buffer{}, storeRetentionPlanInput{
		dbPath: filepath.Join(dir, "old.db"), keepDays: 30,
		recoveryPath: filepath.Join(dir, "recovery.tar"), outputPath: outputPath,
	}); err != nil {
		t.Fatalf("runStoreRetentionPlan() error = %v", err)
	}
	if store.initCalled {
		t.Fatal("Initialize() called during retention plan")
	}
	if _, err := os.Stat(filepath.Join(dir, "old.db")); !os.IsNotExist(err) {
		t.Fatalf("old DB stat error = %v, want not exist", err)
	}
}

func TestStoreRetentionPlanDisclosesCandidatesThatReclaimEssentiallyNothing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		candidate []string
		marker    string
		current   string
		// wantOutput omits the leading "Plan: <path>" line, which carries the
		// test's temporary directory and says nothing about the behaviour
		// under test.
		wantOutput string
	}{
		{
			name:      "every candidate reclaims bytes",
			candidate: []string{"50", "60"}, marker: "10", current: "110",
			wantOutput: "Plan ID: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n" +
				"Net body-column change after retention markers are written (encoded bytes): 90\nBodies discarded: 2\n",
		},
		{
			name:      "some candidates reclaim nothing",
			candidate: []string{"10", "20", "9"}, marker: "10", current: "39",
			wantOutput: "Plan ID: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n" +
				"Net body-column change after retention markers are written (encoded bytes): 9\nBodies discarded: 3\n" +
				"Of those, 2 reclaim nothing: their bodies are no larger than the marker that replaces them, and they are discarded anyway.\n",
		},
		{
			// The case the disclosure exists for: three bodies destroyed, one
			// byte gained. Nothing but the second line tells the operator that
			// before they apply it.
			name:      "every candidate reclaims nothing",
			candidate: []string{"10", "9"}, marker: "10", current: "19",
			wantOutput: "Plan ID: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n" +
				"Net body-column change after retention markers are written (encoded bytes): -1\nBodies discarded: 2\n" +
				"Of those, 2 reclaim nothing: their bodies are no larger than the marker that replaces them, and they are discarded anyway.\n",
		},
		{
			// No division by the candidate count, and no line about a set that
			// is empty.
			name:      "no candidates",
			candidate: nil, marker: "0", current: "0",
			wantOutput: "Plan ID: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n" +
				"Net body-column change after retention markers are written (encoded bytes): 0\nBodies discarded: 0\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			outputPath := filepath.Join(dir, "plan.json")
			root := NewRootCLI(
				WithStoreManagement(&retentionStoreStub{}),
				WithRawBodyRetention(&rawBodyRetentionStub{plan: retentionPlanOutputFixture(t, test.current, test.marker, test.candidate)}),
			)
			var output bytes.Buffer
			if err := root.runStoreRetentionPlan(context.Background(), &output, storeRetentionPlanInput{
				dbPath: filepath.Join(dir, "old.db"), keepDays: 30,
				recoveryPath: filepath.Join(dir, "recovery.tar"), outputPath: outputPath,
			}); err != nil {
				t.Fatalf("runStoreRetentionPlan() error = %v", err)
			}
			got := strings.TrimPrefix(output.String(), "Plan: "+outputPath+"\n")
			if got == output.String() {
				t.Fatalf("output did not start with the plan path: %q", output.String())
			}
			if diff := cmp.Diff(test.wantOutput, got); diff != "" {
				t.Errorf("runStoreRetentionPlan() output mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func retentionPlanOutputFixture(t *testing.T, current, marker string, candidateBytes []string) []byte {
	t.Helper()
	candidates := make([]any, 0, len(candidateBytes))
	for _, bytes := range candidateBytes {
		candidates = append(candidates, map[string]any{"logical_extent": map[string]string{"bytes": bytes}})
	}
	markerBytes, err := strconv.Atoi(marker)
	if err != nil {
		t.Fatalf("strconv.Atoi(marker) error = %v", err)
	}
	projected := markerBytes * len(candidateBytes)
	fixture := map[string]any{
		"plan_id": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"canonical_payload": map[string]any{
			"class_results": []any{map[string]any{"ceilings": []any{map[string]any{
				"current": map[string]string{"bytes": current}, "projected": map[string]string{"bytes": strconv.Itoa(projected)},
			}}}},
			"candidates": candidates,
		},
	}
	data, err := json.Marshal(fixture)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return data
}

func TestStoreRetentionApplyReadsAndValidatesBeforeStoreOpen(t *testing.T) {
	t.Parallel()

	store := &retentionStoreStub{}
	root := NewRootCLI(
		WithStoreManagement(store),
		WithRawBodyRetention(&rawBodyRetentionStub{}),
	)
	dir := t.TempDir()
	planPath := filepath.Join(dir, "plan.json")
	if err := os.WriteFile(planPath, []byte(`{"plan_id":"invalid-on-purpose"}`), 0o600); err != nil {
		t.Fatalf("WriteFile(plan) error = %v", err)
	}
	if _, err := root.readRetentionExecutionInput(storeRetentionExecutionInput{
		dbPath: filepath.Join(dir, "old.db"), planPath: planPath,
	}); err != nil {
		t.Fatalf("readRetentionExecutionInput() error = %v", err)
	}
	if store.initCalled {
		t.Fatal("Initialize() called before retention plan validation")
	}
	if _, err := os.Stat(filepath.Join(dir, "old.db")); !os.IsNotExist(err) {
		t.Fatalf("old DB stat error = %v, want not exist", err)
	}
}

func TestStoreRetentionCommandsAreVisibleButRemainExplicit(t *testing.T) {
	t.Parallel()

	rootCLI := NewRootCLI()
	command := rootCLI.newStoreRetentionCommand()
	if command.Hidden {
		t.Fatal("store retention command is hidden after copied-store dogfood")
	}
	files, _, err := command.Find([]string{"files"})
	if err != nil {
		t.Fatalf("Find(files) error = %v", err)
	}
	if files.Hidden {
		t.Fatal("store retention files command is hidden after copied-store dogfood")
	}
	apply, _, err := command.Find([]string{"apply"})
	if err != nil {
		t.Fatalf("Find(apply) error = %v", err)
	}
	for _, required := range []string{"plan", "recovery", "confirm-plan-id"} {
		if flag := apply.Flag(required); flag == nil {
			t.Fatalf("apply missing explicit %s flag", required)
		}
	}
}
