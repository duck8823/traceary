//go:build darwin || linux

package runtime_test

import (
	"context"
	"errors"
	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	infra "github.com/duck8823/traceary/infrastructure/runtime"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestControlJournalPrivateMetadataOnlyTerminalAndImmutableEvidence(t *testing.T) {
	_, descriptor, _ := ptyFixture(t)
	root := daemonRoot(t)
	store, err := infra.NewExecutionStore(root, infra.MaxRegistryBytes)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	snapshot := descriptor.Snapshot()
	if err = store.RegisterProfile(ctx, snapshot.Profile); err != nil {
		t.Fatal(err)
	}
	if err = store.ReserveLaunch(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	selection := app.OutputSelection{Identity: snapshot.Identity, Worktree: snapshot.Target.Worktree().ID(), Profile: snapshot.Profile.ID()}
	state := domain.ControlSnapshot{Identity: snapshot.Identity, Intent: domain.ControlIntentState{RequestID: "manual-1", Kind: domain.ControlInput, ManualTerminal: true, InputBytes: 5}, Outcome: domain.ControlPending}
	pending, err := domain.ControlReceiptFrom(state)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.ReserveControl(ctx, selection, pending); err != nil {
		t.Fatal(err)
	}
	if err = store.ReserveControl(ctx, selection, pending); !errors.Is(err, domain.ErrDuplicate) {
		t.Fatal("durable intent overwritten")
	}
	state.Outcome = domain.ControlUnknown
	state.WrittenBytes = 2
	partial, err := domain.ControlReceiptFrom(state)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SaveControl(ctx, selection, partial); err != nil {
		t.Fatal(err)
	}
	receipts, err := store.ListControlReceipts(ctx, selection)
	if err != nil || len(receipts) != 1 || receipts[0].Snapshot() != state {
		t.Fatalf("durable receipt round trip: %v", err)
	}
	state.Outcome = domain.ControlDelivered
	state.WrittenBytes = 5
	replacement, _ := domain.ControlReceiptFrom(state)
	if err = store.SaveControl(ctx, selection, replacement); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("unknown receipt rewritten as success")
	}
	file := filepath.Join(root, "receipts.json")
	info, err := os.Lstat(file)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("receipt file is not private")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"fingerprint", "hmac", "payload", "data", "executable", "environment", "pid", "null"} {
		if strings.Contains(string(data), `"`+forbidden+`"`) {
			t.Fatal("receipt file contains forbidden content")
		}
	}
	foreign := selection
	foreign.Profile = "foreign"
	if err = store.ReserveControl(ctx, foreign, pending); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("foreign binding admitted")
	}
}

func TestControlJournalDiscriminatesUnknownVersionBeforeV1Shape(t *testing.T) {
	_, descriptor, _ := ptyFixture(t)
	snapshot := descriptor.Snapshot()
	selection := app.OutputSelection{Identity: snapshot.Identity, Worktree: snapshot.Target.Worktree().ID(), Profile: snapshot.Profile.ID()}
	for _, test := range []struct {
		name, document string
		want           error
	}{
		{"future-new-field", `{"version":2,"new_field":"new"}`, domain.ErrUnsupported},
		{"future-null-field", `{"version":2,"new_field":null}`, domain.ErrUnsupported},
		{"future-no-v1-shape", `{"version":2}`, domain.ErrUnsupported},
		{"known-unknown-field", `{"version":1,"revision":0,"runs":[],"new_field":true}`, domain.ErrInvalid},
		{"known-null", `{"version":1,"revision":0,"runs":null}`, domain.ErrInvalid},
		{"missing-version", `{"revision":0,"runs":[]}`, domain.ErrInvalid},
		{"null-version", `{"version":null}`, domain.ErrInvalid},
		{"wrong-version-type", `{"version":"2"}`, domain.ErrInvalid},
		{"duplicate-version", `{"version":2,"version":2}`, domain.ErrInvalid},
		{"invalid-json", `{"version":2} trailing`, domain.ErrInvalid},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := daemonRoot(t)
			store, err := infra.NewExecutionStore(root, infra.MaxRegistryBytes)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "receipts.json")
			if err = os.WriteFile(path, []byte(test.document), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err = store.ListControlReceipts(context.Background(), selection); !errors.Is(err, test.want) {
				t.Fatalf("version/shape distinction lost: got %v want %v", err, test.want)
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != test.document {
				t.Fatal("schema refusal mutated receipt evidence", err)
			}
		})
	}
}
func TestControlJournalRejectsNullAndUnknownSchemaBeforeMutation(t *testing.T) {
	_, descriptor, _ := ptyFixture(t)
	snapshot := descriptor.Snapshot()
	selection := app.OutputSelection{Identity: snapshot.Identity, Worktree: snapshot.Target.Worktree().ID(), Profile: snapshot.Profile.ID()}
	for _, document := range []string{`{"version":1,"revision":0,"runs":null}`, `{"version":1,"revision":0,"runs":[],"payload":"forbidden"}`, `{"version":1,"version":1,"revision":0,"runs":[]}`} {
		t.Run("strict", func(t *testing.T) {
			root := daemonRoot(t)
			store, err := infra.NewExecutionStore(root, infra.MaxRegistryBytes)
			if err != nil {
				t.Fatal(err)
			}
			if err = store.RegisterProfile(context.Background(), snapshot.Profile); err != nil {
				t.Fatal(err)
			}
			if err = store.ReserveLaunch(context.Background(), snapshot); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(root, "receipts.json")
			if err = os.WriteFile(file, []byte(document), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err = store.ListControlReceipts(context.Background(), selection); err == nil {
				t.Fatal("invalid control metadata accepted")
			}
			after, err := os.ReadFile(file)
			if err != nil || string(after) != document {
				t.Fatal("failed read altered evidence")
			}
		})
	}
}

func TestControlJournalSharedBudgetRejectsBeforeCommit(t *testing.T) {
	_, descriptor, _ := ptyFixture(t)
	root := daemonRoot(t)
	const budget int64 = 8192
	store, err := infra.NewExecutionStore(root, budget)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	snapshot := descriptor.Snapshot()
	if err = store.RegisterProfile(ctx, snapshot.Profile); err != nil {
		t.Fatal(err)
	}
	if err = store.ReserveLaunch(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	execution, err := os.ReadFile(filepath.Join(root, "execution.json"))
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(root, "history.json"), os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(budget - int64(len(execution)) - 64); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	selection := app.OutputSelection{Identity: snapshot.Identity, Worktree: snapshot.Target.Worktree().ID(), Profile: snapshot.Profile.ID()}
	receipt, err := domain.ControlReceiptFrom(domain.ControlSnapshot{Identity: snapshot.Identity, Intent: domain.ControlIntentState{RequestID: "input", Kind: domain.ControlInput, ManualTerminal: true, InputBytes: 1}, Outcome: domain.ControlPending})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.ReserveControl(ctx, selection, receipt); !errors.Is(err, domain.ErrLimit) || errors.Is(err, app.ErrControlCommitUncertain) {
		t.Fatalf("known precommit budget failure misclassified: %v", err)
	}
	if _, err = os.Stat(filepath.Join(root, "receipts.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("over-budget intent committed")
	}
}

func TestControlJournalFileCeilingRejectsBeforeAllocation(t *testing.T) {
	_, descriptor, _ := ptyFixture(t)
	root := daemonRoot(t)
	store, err := infra.NewExecutionStore(root, infra.MaxRegistryBytes)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := descriptor.Snapshot()
	ctx := context.Background()
	if err = store.RegisterProfile(ctx, snapshot.Profile); err != nil {
		t.Fatal(err)
	}
	if err = store.ReserveLaunch(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(filepath.Join(root, "receipts.json"), os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err = file.Truncate(64*1024*1024 + 1); err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	selection := app.OutputSelection{Identity: snapshot.Identity, Worktree: snapshot.Target.Worktree().ID(), Profile: snapshot.Profile.ID()}
	if _, err = store.ListControlReceipts(ctx, selection); !errors.Is(err, domain.ErrLimit) {
		t.Fatalf("receipt file limit not enforced before decode: %v", err)
	}
}
