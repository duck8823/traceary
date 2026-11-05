//go:build darwin || linux

package runtime_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	infra "github.com/duck8823/traceary/infrastructure/runtime"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLifecycleEpochAndShutdownEvidenceArePrivateHistoricalAndImmutable(t *testing.T) {
	_, descriptor, _ := ptyFixture(t)
	ctx := context.Background()
	root := daemonRoot(t)
	store, err := infra.NewExecutionStore(root, infra.MaxRegistryBytes)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.RegisterProfile(ctx, descriptor.Snapshot().Profile); err != nil {
		t.Fatal(err)
	}
	old := descriptor.Snapshot().Identity.Epoch
	first, err := store.RecordEpochStart(ctx, old)
	if err != nil || first.Epoch != old || len(first.Interrupted) != 0 || first.AutoStartOptIn {
		t.Fatal("initial lifecycle marker/defaults invalid", err)
	}
	if _, err = store.RecordEpochStart(ctx, old); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("same epoch startup accepted")
	}
	if err = store.ReserveLaunch(ctx, descriptor.Snapshot()); err != nil {
		t.Fatal(err)
	}
	if err = descriptor.Started(123); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveLaunch(ctx, descriptor.Snapshot()); err != nil {
		t.Fatal(err)
	}
	selection := app.OutputSelection{Identity: descriptor.Identity(), Worktree: descriptor.Target().Worktree().ID(), Profile: descriptor.Snapshot().Profile.ID()}
	pending, err := domain.ControlReceiptFrom(domain.ControlSnapshot{Identity: descriptor.Identity(), Intent: domain.ControlIntentState{RequestID: "uncertain-manual", Kind: domain.ControlInput, ManualTerminal: true, InputBytes: 3}, Outcome: domain.ControlPending})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.ReserveControl(ctx, selection, pending); err != nil {
		t.Fatal(err)
	}
	current := strings.Repeat("e", 32)
	second, err := store.RecordEpochStart(ctx, current)
	if err != nil || second.Epoch != current || second.PreviousEpoch != old || second.PreviousClean || len(second.Interrupted) != 1 {
		t.Fatal("crash/restart did not preserve historical unknown", err)
	}
	historical := second.Interrupted[0]
	if historical.Snapshot().Binding.Identity != descriptor.Identity() || historical.Status() != "orphaned" || historical.Snapshot().PendingControls != 1 {
		t.Fatal("old process/intent evidence lost")
	}
	runs, err := store.ListLaunches(ctx)
	if err != nil || len(runs) != 1 || runs[0] != descriptor.Snapshot() {
		t.Fatal("epoch transition invented process exit/adoption", err)
	}
	receipts, err := store.ListControlReceipts(ctx, selection)
	if err != nil || len(receipts) != 1 || receipts[0].Snapshot().Outcome != domain.ControlPending {
		t.Fatal("old intent replayed/rewritten", err)
	}
	binding, err := domain.ShutdownBindingFrom(domain.ShutdownBindingState{Epoch: current, PreviewID: strings.Repeat("a", 32), Revision: 7, RosterDigest: strings.Repeat("b", 64)})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.ReserveShutdown(ctx, binding); err != nil {
		t.Fatal(err)
	}
	if err = store.ReserveShutdown(ctx, binding); !errors.Is(err, domain.ErrDuplicate) {
		t.Fatal("durable shutdown intent overwritten", err)
	}
	result := app.ShutdownResult{Binding: binding.Snapshot(), Outcome: domain.ShutdownDraining}
	if err = store.SaveShutdown(ctx, result); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveShutdown(ctx, result); err != nil {
		t.Fatal("exact terminal publication not idempotent", err)
	}
	conflict := result
	conflict.Outcome = domain.ShutdownUnknown
	if err = store.SaveShutdown(ctx, conflict); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("terminal lifecycle evidence rewritten")
	}
	if err = store.RecordCleanShutdown(ctx, current); err != nil {
		t.Fatal(err)
	}
	third, err := store.RecordEpochStart(ctx, strings.Repeat("f", 32))
	if err != nil || !third.PreviousClean || third.PreviousEpoch != current || len(third.Interrupted) != 1 {
		t.Fatal("clean daemon marker fabricated old process safety", err)
	}
	info, err := os.Lstat(filepath.Join(root, "lifecycle.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("lifecycle journal not private")
	}
	data, err := os.ReadFile(filepath.Join(root, "lifecycle.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{`"pid"`, `"data"`, `"payload"`, `"fingerprint"`, `"environment"`, `"executable"`, `"native_session"`} {
		if bytes.Contains(data, []byte(forbidden)) {
			t.Fatal("unsafe/unverified lifecycle content")
		}
	}
	third.Interrupted[0] = domain.Interruption{}
	reread, err := store.ReadLifecycle(ctx)
	if err != nil || reread.Interrupted[0].Snapshot().Binding.Identity != descriptor.Identity() {
		t.Fatal("read snapshot aliases stored evidence")
	}
}

func TestLifecyclePreferenceAndContinuousDeferralSurviveRestartWithoutHostMutation(t *testing.T) {
	root := daemonRoot(t)
	ctx := context.Background()
	store, err := infra.NewExecutionStore(root, infra.MaxRegistryBytes)
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.ReadLifecycle(ctx)
	if err != nil || state.AutoStartOptIn {
		t.Fatal("auto-start not off by default")
	}
	if err = store.SetAutoStartOptIn(ctx, true); err != nil {
		t.Fatal(err)
	}
	epoch := strings.Repeat("1", 32)
	if _, err = store.RecordEpochStart(ctx, epoch); err != nil {
		t.Fatal(err)
	}
	d, err := domain.NewUpdateDeferral(domain.UpdateTarget{Version: "0.99.0", Protocol: 2}, time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SaveUpdateDeferral(ctx, epoch, d.Snapshot()); err != nil {
		t.Fatal(err)
	}
	if err = d.Retarget(domain.UpdateTarget{Version: "0.99.1", Protocol: 2}); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveUpdateDeferral(ctx, epoch, d.Snapshot()); err != nil {
		t.Fatal(err)
	}
	reopened, err := infra.OpenExecutionStore(root, infra.MaxRegistryBytes)
	if err != nil {
		t.Fatal(err)
	}
	next := strings.Repeat("2", 32)
	state, err = reopened.RecordEpochStart(ctx, next)
	if err != nil || !state.AutoStartOptIn {
		t.Fatal("local preference reset at restart", err)
	}
	value, found := state.UpdateDeferral.Value()
	if !found || value != d.Snapshot() {
		t.Fatal("continuous deferral lost/reset")
	}
	if err = reopened.SaveUpdateDeferral(ctx, epoch, d.Snapshot()); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("old epoch changed active deferral")
	}
	later := d.Snapshot()
	later.Started = later.Started.Add(time.Hour)
	if err = reopened.SaveUpdateDeferral(ctx, next, later); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("continuous start silently reset")
	}
	if err = reopened.ClearUpdateDeferral(ctx, next); err != nil {
		t.Fatal(err)
	}
	state, err = reopened.ReadLifecycle(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, found = state.UpdateDeferral.Value(); found {
		t.Fatal("explicit handoff deferral clear not persisted")
	}
	if err = reopened.SetAutoStartOptIn(ctx, false); err != nil {
		t.Fatal(err)
	}
}

func TestLifecycleStrictSchemaAndEnvelopeBudgetRefuseBeforeMutation(t *testing.T) {
	ctx := context.Background()
	for _, body := range []string{`{"version":99}`, `{"version":1,"revision":0,"epoch":"","clean":false,"previous_epoch":"","previous_clean":false,"interrupted":null,"shutdowns":[],"autostart_opt_in":false}`, `{"version":1,"version":1}`, `{"version":1,"revision":0,"payload":"secret"}`} {
		t.Run("schema", func(t *testing.T) {
			root := daemonRoot(t)
			store, err := infra.NewExecutionStore(root, infra.MaxRegistryBytes)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "lifecycle.json")
			if err = os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err = store.RecordEpochStart(ctx, strings.Repeat("1", 32)); err == nil {
				t.Fatal("invalid lifecycle schema mutated")
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != body {
				t.Fatal("refused lifecycle changed bytes")
			}
		})
	}
	t.Run("aggregate", func(t *testing.T) {
		_, descriptor, _ := ptyFixture(t)
		root := daemonRoot(t)
		store, err := infra.NewExecutionStore(root, 3000)
		if err != nil {
			t.Fatal(err)
		}
		file, err := os.OpenFile(filepath.Join(root, "lifecycle.json"), os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		if err = file.Truncate(2990); err != nil {
			t.Fatal(err)
		}
		_ = file.Close()
		if err = store.RegisterProfile(ctx, descriptor.Snapshot().Profile); !errors.Is(err, domain.ErrLimit) {
			t.Fatal("other owner ignored lifecycle budget", err)
		}
		if _, err = os.Stat(filepath.Join(root, "execution.json")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("overflow created execution metadata")
		}
	})
	t.Run("oversize", func(t *testing.T) {
		root := daemonRoot(t)
		store, err := infra.NewExecutionStore(root, infra.MaxRegistryBytes)
		if err != nil {
			t.Fatal(err)
		}
		file, err := os.OpenFile(filepath.Join(root, "lifecycle.json"), os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		if err = file.Truncate(16*1024*1024 + 1); err != nil {
			t.Fatal(err)
		}
		_ = file.Close()
		if _, err = store.ReadLifecycle(ctx); !errors.Is(err, domain.ErrLimit) {
			t.Fatal("oversize lifecycle read admitted", err)
		}
	})
}

func TestLifecyclePrivateAttributesAndReceiptCapacityNeverEvictOrRepair(t *testing.T) {
	ctx := context.Background()
	for _, kind := range []string{"symlink", "hardlink", "world-readable"} {
		t.Run(kind, func(t *testing.T) {
			root := daemonRoot(t)
			store, err := infra.NewExecutionStore(root, infra.MaxRegistryBytes)
			if err != nil {
				t.Fatal(err)
			}
			if err = store.SetAutoStartOptIn(ctx, false); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "lifecycle.json")
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "symlink":
				target := filepath.Join(root, "test-owned-target")
				if err = os.Rename(path, target); err != nil {
					t.Fatal(err)
				}
				if err = os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err = os.Link(path, filepath.Join(root, "test-owned-alias")); err != nil {
					t.Fatal(err)
				}
			case "world-readable":
				if err = os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			}
			if err = store.SetAutoStartOptIn(ctx, true); !errors.Is(err, domain.ErrUnsafe) {
				t.Fatal("unsafe lifecycle attributes accepted", err)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("attribute refusal changed stored bytes")
			}
		})
	}
	t.Run("capacity", func(t *testing.T) {
		root := daemonRoot(t)
		store, err := infra.NewExecutionStore(root, infra.MaxRegistryBytes)
		if err != nil {
			t.Fatal(err)
		}
		epoch := strings.Repeat("a", 32)
		if _, err = store.RecordEpochStart(ctx, epoch); err != nil {
			t.Fatal(err)
		}
		binding, err := domain.ShutdownBindingFrom(domain.ShutdownBindingState{Epoch: epoch, PreviewID: strings.Repeat("b", 32), RosterDigest: strings.Repeat("c", 64)})
		if err != nil {
			t.Fatal(err)
		}
		if err = store.ReserveShutdown(ctx, binding); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, "lifecycle.json")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]json.RawMessage
		if err = json.Unmarshal(data, &doc); err != nil {
			t.Fatal(err)
		}
		var records []json.RawMessage
		if err = json.Unmarshal(doc["shutdowns"], &records); err != nil {
			t.Fatal(err)
		}
		expanded := make([]json.RawMessage, domain.MaxLaunchRequests+1)
		for i := range expanded {
			expanded[i] = records[0]
		}
		doc["shutdowns"], err = json.Marshal(expanded)
		if err != nil {
			t.Fatal(err)
		}
		before, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, before, 0600); err != nil {
			t.Fatal(err)
		}
		if err = store.SetAutoStartOptIn(ctx, true); err == nil {
			t.Fatal("over-capacity lifecycle receipt array accepted")
		}
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("over-capacity refusal evicted or repaired entries")
		}
	})
}
