//go:build darwin || linux

package runtime_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	infra "github.com/duck8823/traceary/infrastructure/runtime"
	"golang.org/x/xerrors"
)

// This uses real Registry/journal/PTY adapters and the real RuntimeFactory path.
// The finite test executable stands in for a native CLI; it certifies no real host.
func selectedDaemonFixture(t *testing.T, options ...infra.DaemonOptions) (*infra.LocalDaemon, string, *app.Runtime, domain.RunSnapshot) {
	t.Helper()
	_, descriptor, result := ptyFixture(t)
	root := daemonRoot(t)
	ctx := context.Background()
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
	release := filepath.Join(filepath.Dir(result), "selected-release")
	owners := make(chan *app.Runtime, 1)
	factory := func(_ context.Context, _ string, epoch string) (*app.Runtime, error) {
		launcher, e := infra.NewPTYLauncher(services.Registry, []string{"TRACEARY_PTY_FIXTURE=1", "FIXTURE_OUTPUT_MODE=maintenance", "FIXTURE_RESULT=" + result, "FIXTURE_RELEASE=" + release, "GORACE=atexit_sleep_ms=0"})
		if e != nil {
			return nil, xerrors.Errorf("compose selected fixture launcher: %w", e)
		}
		owner, e := app.NewRuntime(services.Registry, journal, launcher, journal, epoch)
		if e != nil {
			return nil, xerrors.Errorf("compose selected fixture owner: %w", e)
		}
		owners <- owner
		return owner, nil
	}
	if len(options) > 1 {
		t.Fatal("only one fixture option set is supported")
	}
	config := infra.DaemonOptions{}
	if len(options) == 1 {
		config = options[0]
	}
	config.RuntimeFactory = factory
	d := daemon(t, nil, config)
	_, _ = startDaemon(t, d, root)
	owner := <-owners
	t.Cleanup(func() { _ = os.WriteFile(release, []byte("release finite owned fixture"), 0600) })
	status, err := d.Status(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	run, err := d.Launch(ctx, root, app.LaunchRequest{Epoch: status.Epoch, Worktree: worktree.ID(), Profile: descriptor.Target().Profile(), RequestID: "selected-observation-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	return d, root, owner, run
}

func selectedWireValue(run domain.RunSnapshot) map[string]any {
	return map[string]any{"selection": map[string]any{"epoch": run.Identity.Epoch, "run": run.Identity.Run, "generation": run.Identity.Generation, "worktree": string(run.Target.Worktree().ID()), "profile": string(run.Profile.ID())}, "metadata": map[string]any{"host": run.Profile.Snapshot().Host, "host_version": run.Profile.Snapshot().Version, "event_id": "spool:selected", "kind": "prompt_boundary"}}
}

func TestSelectedObservationRealSocketReachesOwnedRuntimeWithoutLegacyReceiver(t *testing.T) {
	_, root, owner, run := selectedDaemonFixture(t)
	ctx := context.Background()
	metadata, err := infra.OpenMetadataStore(root, infra.MaxRegistryBytes)
	if err != nil {
		t.Fatal(err)
	}
	before, err := metadata.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	conn := rawEndpoint(t, root, "observations.sock", "observations")
	wire, err := json.Marshal(map[string]any{"protocol_version": 1, "request_id": "selected", "operation": "observe_selected_v1", "selected_observation": selectedWireValue(run)})
	if err != nil {
		t.Fatal(err)
	}
	rawFrame(t, conn, string(wire))
	if code := rawCode(t, conn); code != "queued" {
		t.Fatalf("valid selected observation ACK=%s, want queued", code)
	}
	selection := app.OutputSelection{Identity: run.Identity, Worktree: run.Target.Worktree().ID(), Profile: run.Profile.ID()}
	deadline := time.Now().Add(10 * time.Second)
	for {
		last, present, e := owner.LastObservation(selection)
		if e != nil {
			t.Fatal(e)
		}
		if present {
			if last.Snapshot().EventID != "spool:selected" {
				t.Fatal("unexpected retained metadata")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("queued selected sample not consumed by actual owner")
		}
		time.Sleep(5 * time.Millisecond)
	}
	after, err := metadata.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if before.Digest != after.Digest || before.ExecutionRevision != after.ExecutionRevision || before.RegistryRevision != after.RegistryRevision || before.ReceiptRevision != after.ReceiptRevision {
		t.Fatal("volatile observation changed persisted metadata")
	}
	if got := owner.List(); len(got) != 1 || got[0] != run {
		t.Fatal("selected telemetry changed process state", got)
	}
}

func selectedSample(t *testing.T, run domain.RunSnapshot) app.SelectedObservation {
	t.Helper()
	v := run.Profile.Snapshot()
	metadata, err := domain.DerivedObservationOf(domain.DerivedObservationState{Host: v.Host, HostVersion: v.Version, EventID: "spool:selected-sdk", Kind: domain.ObservationToolBoundary})
	if err != nil {
		t.Fatal(err)
	}
	return app.SelectedObservation{Selection: app.OutputSelection{Identity: run.Identity, Worktree: run.Target.Worktree().ID(), Profile: run.Profile.ID()}, Metadata: metadata}
}

func TestSelectedObservationSDKAndServerRejectMismatchesAndMixedArms(t *testing.T) {
	d, root, owner, run := selectedDaemonFixture(t)
	ctx := context.Background()
	valid := selectedSample(t, run)
	status, err := d.Status(ctx, root)
	if err != nil || status.ObservationDelivery != "configured" || !slices.Contains(status.Capabilities, "observe_selected_v1") {
		t.Fatal("actual RuntimeFactory intake not advertised", status, err)
	}
	metadata, err := infra.OpenMetadataStore(root, infra.MaxRegistryBytes)
	if err != nil {
		t.Fatal(err)
	}
	before, err := metadata.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name   string
		mutate func(*app.SelectedObservation)
		want   error
	}{
		{"run", func(o *app.SelectedObservation) { o.Selection.Identity.Run = strings.Repeat("a", 32) }, domain.ErrNotFound},
		{"generation", func(o *app.SelectedObservation) { o.Selection.Identity.Generation = strings.Repeat("a", 32) }, domain.ErrChanged},
		{"epoch", func(o *app.SelectedObservation) { o.Selection.Identity.Epoch = strings.Repeat("a", 32) }, domain.ErrChanged},
		{"worktree", func(o *app.SelectedObservation) {
			o.Selection.Worktree = domain.WorktreeID("w_" + strings.Repeat("a", 64))
		}, domain.ErrChanged},
		{"profile", func(o *app.SelectedObservation) { o.Selection.Profile = "different" }, domain.ErrChanged},
		{"host", func(o *app.SelectedObservation) {
			v := o.Metadata.Snapshot()
			v.Host = "claude"
			o.Metadata, _ = domain.DerivedObservationOf(v)
		}, domain.ErrChanged},
		{"host_version", func(o *app.SelectedObservation) {
			v := o.Metadata.Snapshot()
			v.HostVersion = "different 2"
			o.Metadata, _ = domain.DerivedObservationOf(v)
		}, domain.ErrChanged},
		{"zero_metadata", func(o *app.SelectedObservation) { o.Metadata = domain.DerivedObservation{} }, app.ErrRuntimeProtocol},
	} {
		t.Run(tt.name, func(t *testing.T) {
			o := valid
			tt.mutate(&o)
			if e := d.ObserveSelected(ctx, root, o); !errors.Is(e, tt.want) {
				t.Fatalf("selected mismatch=%v want %v", e, tt.want)
			}
		})
	}
	for _, arm := range []string{"history", "history_hint", "control", "receipt", "attach", "observation", "launch", "page"} {
		t.Run("mixed_"+arm, func(t *testing.T) {
			conn := rawEndpoint(t, root, "observations.sock", "observations")
			wire, e := json.Marshal(map[string]any{"protocol_version": 1, "request_id": "mixed", "operation": "observe_selected_v1", "selected_observation": selectedWireValue(run), arm: map[string]any{}})
			if e != nil {
				t.Fatal(e)
			}
			rawFrame(t, conn, string(wire))
			if code := rawCode(t, conn); code != "invalid" {
				t.Fatal("mixed arm admitted", code)
			}
		})
	}
	for _, endpoint := range []struct{ name, role, operation, want string }{{"daemon.sock", "management", "observe_selected_v1", "unsupported"}, {"observations.sock", "observations", "observe_selected_v2", "unsupported"}, {"observations.sock", "observations", "status", "unsupported"}, {"observations.sock", "observations", "input", "unsupported"}} {
		conn := rawEndpoint(t, root, endpoint.name, endpoint.role)
		wire, e := json.Marshal(map[string]any{"protocol_version": 1, "request_id": "wrong-route", "operation": endpoint.operation, "selected_observation": selectedWireValue(run)})
		if e != nil {
			t.Fatal(e)
		}
		rawFrame(t, conn, string(wire))
		if code := rawCode(t, conn); code != endpoint.want {
			t.Fatalf("route %s/%s = %s", endpoint.role, endpoint.operation, code)
		}
	}
	conn, err := net.DialTimeout("unix", filepath.Join(root, "observations.sock"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	hello, err := json.Marshal(map[string]any{"protocol_version": 1, "request_id": "hello-selected", "operation": "hello", "role": "observations", "selected_observation": selectedWireValue(run)})
	if err != nil {
		t.Fatal(err)
	}
	rawFrame(t, conn, string(hello))
	if code := rawCode(t, conn); code != "invalid" {
		t.Fatal("hello admitted data arm", code)
	}
	if _, present, e := owner.LastObservation(valid.Selection); e != nil || present {
		t.Fatal("rejected requests retained a sample", present, e)
	}
	after, err := metadata.Export(ctx)
	if err != nil || after.Digest != before.Digest {
		t.Fatal("negative requests changed metadata", err)
	}
	if err = d.ObserveSelected(ctx, root, valid); err != nil {
		t.Fatal("public typed SDK did not deliver", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		last, present, e := owner.LastObservation(valid.Selection)
		if e != nil {
			t.Fatal(e)
		}
		if present {
			if last != valid.Metadata {
				t.Fatal("SDK metadata changed")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("SDK sample not consumed")
		}
		time.Sleep(5 * time.Millisecond)
	}
	owner.DisableLaunches()
	status, err = d.Status(ctx, root)
	if err != nil || slices.Contains(status.Capabilities, "observe_selected_v1") || status.ObservationDelivery != "unavailable" {
		t.Fatal("disabled owner advertised selected intake", status, err)
	}
	if e := d.ObserveSelected(ctx, root, valid); !errors.Is(e, app.ErrRuntimeRejected) {
		t.Fatal("disabled owner admitted observation", e)
	}
}

func TestSelectedObservationQueueAdmissionIsNotPhysicalValidationSuccess(t *testing.T) {
	d, root, owner, run := selectedDaemonFixture(t)
	ctx := context.Background()
	sample := selectedSample(t, run)
	store := run.Target.Worktree().Store()
	prior := store + ".selected-prior"
	if err := os.Rename(store, prior); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(store); _ = os.Rename(prior, store) })
	if err := os.WriteFile(store, []byte("different physical store"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := d.ObserveSelected(ctx, root, sample); err != nil {
		t.Fatal("cheap descriptor admission did physical I/O", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		status, err := d.Status(ctx, root)
		if err != nil {
			t.Fatal(err)
		}
		if status.ObservationDelivery == "degraded" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("physical source failure not reported degraded")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, present, e := owner.LastObservation(sample.Selection); e != nil || present {
		t.Fatal("changed physical source produced retained sample", present, e)
	}
}

func TestSelectedObservationWithoutRuntimeHasNoConfiguredCapability(t *testing.T) {
	root := daemonRoot(t)
	d := daemon(t, nil, infra.DaemonOptions{})
	_, _ = startDaemon(t, d, root)
	status, err := d.Status(context.Background(), root)
	if err != nil || status.ObservationDelivery != "unavailable" || slices.Contains(status.Capabilities, "observe_selected_v1") {
		t.Fatal("missing owner advertised intake", status, err)
	}
}

func TestSelectedObservationSharedQueuePressureDoesNotBlockOwnerAdmission(t *testing.T) {
	d, root, owner, run := selectedDaemonFixture(t, infra.DaemonOptions{ObservationFrames: 1})
	ctx := context.Background()
	sample := selectedSample(t, run)
	lock, err := unix.Open(filepath.Join(root, "registry.lock"), unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unix.Flock(lock, unix.LOCK_UN); _ = unix.Close(lock) }()
	if err = unix.Flock(lock, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	metadata, err := infra.OpenMetadataStore(root, infra.MaxRegistryBytes)
	if err != nil {
		t.Fatal(err)
	}
	blocked, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	_, err = metadata.Export(blocked)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("fixture failed to hold the actual physical metadata lock: %v", err)
	}
	accepted := 0
	busy := false
	last := ""
	deadline := time.Now().Add(2 * time.Second)
	for attempt := 0; attempt < 50; attempt++ {
		state := sample.Metadata.Snapshot()
		state.EventID = fmt.Sprintf("spool:pressure-%d", attempt)
		sample.Metadata, err = domain.DerivedObservationOf(state)
		if err != nil {
			t.Fatal(err)
		}
		err = d.ObserveSelected(ctx, root, sample)
		switch {
		case err == nil:
			accepted++
			last = state.EventID
		case errors.Is(err, app.ErrRuntimeBusy):
			busy = true
		default:
			t.Fatal("in-memory admission waited for the physical metadata lock", err)
		}
		if accepted == 2 && busy {
			break
		}
		if accepted > 2 || time.Now().After(deadline) {
			t.Fatalf("queue/in-flight bound changed: accepted=%d busy=%v", accepted, busy)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if accepted != 2 || !busy {
		t.Fatalf("bounded queue not observed: accepted=%d busy=%v", accepted, busy)
	}
	if _, present, e := owner.LastObservation(sample.Selection); e != nil || present {
		t.Fatal("admission was mistaken for physical consumption", present, e)
	}
	status, err := d.Status(ctx, root)
	if err != nil || status.ObservationDelivery != "degraded" {
		t.Fatal("queue pressure not visible", status, err)
	}
	if err = unix.Flock(lock, unix.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(10 * time.Second)
	for {
		value, present, e := owner.LastObservation(sample.Selection)
		if e != nil {
			t.Fatal(e)
		}
		if present && value.Snapshot().EventID == last {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("admitted queue did not consume after physical lock release")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
