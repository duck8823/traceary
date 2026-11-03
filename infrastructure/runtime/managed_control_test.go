//go:build darwin || linux

package runtime_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	infra "github.com/duck8823/traceary/infrastructure/runtime"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestManagedExplicitControlsSocketNativeAndDurableNoReplay(t *testing.T) {
	_, descriptor, result := ptyFixture(t)
	ctx := context.Background()
	root := daemonRoot(t)
	services, err := (infra.LocalOperatorFactory{}).Create(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	project, err := services.Registry.RegisterProject(ctx, descriptor.Target().Worktree().Path())
	if err != nil {
		t.Fatal(err)
	}
	worktree, err := services.Registry.RegisterWorktree(ctx, project.ID(), descriptor.Target().Worktree().Path(), descriptor.Target().Worktree().Store())
	if err != nil {
		t.Fatal(err)
	}
	journal, err := infra.OpenExecutionStore(root, infra.MaxRegistryBytes)
	if err != nil {
		t.Fatal(err)
	}
	if err = journal.RegisterProfile(ctx, descriptor.Snapshot().Profile); err != nil {
		t.Fatal(err)
	}
	owners := make(chan *app.Runtime, 1)
	factory := func(_ context.Context, _ string, epoch string) (*app.Runtime, error) {
		launcher, e := infra.NewPTYLauncher(services.Registry, []string{"TRACEARY_PTY_FIXTURE=1", "FIXTURE_OUTPUT_MODE=control", "FIXTURE_RESULT=" + result, "GORACE=atexit_sleep_ms=0"})
		if e != nil {
			return nil, fmt.Errorf("compose synthetic controller: %w", e)
		}
		owner, e := app.NewRuntime(services.Registry, journal, launcher, journal, epoch)
		if e == nil {
			owners <- owner
		}
		if e != nil {
			return nil, fmt.Errorf("compose synthetic owner: %w", e)
		}
		return owner, nil
	}
	d := daemon(t, nil, infra.DaemonOptions{RuntimeFactory: factory})
	cancel, done := startDaemon(t, d, root)
	owner := <-owners
	status, err := d.Status(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	run, err := d.Launch(ctx, root, app.LaunchRequest{Epoch: status.Epoch, Worktree: worktree.ID(), Profile: descriptor.Target().Profile(), RequestID: "native-control"})
	if err != nil {
		t.Fatal(err)
	}
	selection := app.OutputSelection{Identity: run.Identity, Worktree: worktree.ID(), Profile: run.Profile.ID()}
	stop, _ := domain.ControlIntentFrom(domain.ControlIntentState{RequestID: "stop", Kind: domain.ControlStop, WorkLossAcknowledged: true})
	t.Cleanup(func() {
		cleanup, stopCancel := context.WithTimeout(ctx, 3*time.Second)
		defer stopCancel()
		_, _ = owner.Control(cleanup, app.ControlRequest{Selection: selection, Intent: stop})
	})
	waitFile := func(path string) []byte {
		t.Helper()
		deadline := time.Now().Add(4 * time.Second)
		for {
			data, e := os.ReadFile(path)
			if e == nil {
				return data
			}
			if time.Now().After(deadline) {
				t.Fatal("synthetic native control result missing")
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	_ = waitFile(result + ".ready")
	raw := []byte{0xe6, 0x97, 0xa5, 0x1b, 0xff}
	intent, _ := domain.ControlIntentFrom(domain.ControlIntentState{RequestID: "manual-1", Kind: domain.ControlInput, ManualTerminal: true, InputBytes: 5})
	request := app.ControlRequest{Selection: selection, Intent: intent, Data: raw}
	first, err := d.Control(ctx, root, request)
	if err != nil || first.Receipt.Outcome != domain.ControlDelivered || first.Receipt.WrittenBytes != 5 {
		t.Fatalf("socket manual input: %v", err)
	}
	if !bytes.Equal(waitFile(result+".input"), raw) {
		t.Fatal("socket input altered")
	}
	duplicate, err := d.Control(ctx, root, request)
	if err != nil || duplicate != first {
		t.Fatal("duplicate control replayed or receipt changed")
	}
	observation := rawEndpoint(t, root, "observations.sock", "observations")
	wire, _ := json.Marshal(map[string]any{"protocol_version": 1, "request_id": "forbidden-input", "operation": "input", "control": map[string]any{"selection": map[string]string{"epoch": selection.Identity.Epoch, "run": selection.Identity.Run, "generation": selection.Identity.Generation, "worktree": string(selection.Worktree), "profile": string(selection.Profile)}, "intent": map[string]any{"id": "forbidden", "kind": "input", "manual": true, "work_loss": false, "input_bytes": "1", "sequence": "0", "rows": 0, "cols": 0}, "data": "eA=="}})
	rawFrame(t, observation, string(wire))
	if rawCode(t, observation) != "unsupported" {
		t.Fatal("observation role gained terminal authority")
	}
	resize, _ := domain.ControlIntentFrom(domain.ControlIntentState{RequestID: "resize", Kind: domain.ControlResize, ResizeSequence: 1, Rows: 31, Cols: 101})
	if _, err = d.Control(ctx, root, app.ControlRequest{Selection: selection, Intent: resize}); err != nil {
		t.Fatal(err)
	}
	marker, _ := domain.ControlIntentFrom(domain.ControlIntentState{RequestID: "marker", Kind: domain.ControlInput, ManualTerminal: true, InputBytes: 1})
	if _, err = d.Control(ctx, root, app.ControlRequest{Selection: selection, Intent: marker, Data: []byte("r")}); err != nil {
		t.Fatal(err)
	}
	if string(waitFile(result+".geometry")) != "[31,101]" {
		t.Fatal("role escalation, input replay or resize acknowledgment forged")
	}
	if _, err = d.Control(ctx, root, app.ControlRequest{Selection: selection, Intent: stop}); err != nil {
		t.Fatal(err)
	}
	waitCtx, waitCancel := context.WithTimeout(ctx, 4*time.Second)
	defer waitCancel()
	if _, err = owner.AwaitExit(waitCtx, run.Identity); err != nil {
		t.Fatal(err)
	}
	receipt, err := d.Receipt(ctx, root, selection, "manual-1")
	if err != nil || receipt != first {
		t.Fatal("completed receipt not inspectable after exit")
	}
	if again, e := d.Control(ctx, root, request); e != nil || again != first {
		t.Fatal("completed duplicate inspected as new mutation after exit")
	}
	late, _ := domain.ControlIntentFrom(domain.ControlIntentState{RequestID: "late", Kind: domain.ControlInput, ManualTerminal: true, InputBytes: 1})
	if _, err = d.Control(ctx, root, app.ControlRequest{Selection: selection, Intent: late, Data: []byte("x")}); !errors.Is(err, domain.ErrChanged) {
		t.Fatalf("late control delivered: %v", err)
	}
	receipts, err := journal.ListControlReceipts(ctx, selection)
	if err != nil || len(receipts) != 4 {
		t.Fatalf("durable receipt count: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "receipts.json"))
	if err != nil || bytes.Contains(data, raw) {
		t.Fatal("raw input persisted")
	}
	cancel()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("control reconciliation lost daemon ownership")
	}
}
