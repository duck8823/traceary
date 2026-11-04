//go:build darwin || linux

package runtime_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	app "github.com/duck8823/traceary/application/runtime"
	infra "github.com/duck8823/traceary/infrastructure/runtime"
)

func TestManagedResumeTransportRefusesUnqualifiedHostAndObservationEndpoint(t *testing.T) {
	d, root, owner, run := selectedDaemonFixture(t)
	ctx := context.Background()
	metadata, err := infra.OpenMetadataStore(root, infra.MaxRegistryBytes)
	if err != nil {
		t.Fatal(err)
	}
	before, err := metadata.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	status, err := d.Status(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(status.Capabilities, "resume") {
		t.Fatal("unqualified normal PTY advertised native resume")
	}
	request := app.ResumeRequest{Epoch: status.Epoch, Selection: app.OutputSelection{Identity: run.Identity, Worktree: run.Target.Worktree().ID(), Profile: run.Profile.ID()}, RequestID: "unsupported-native-resume", WorkLossAcknowledged: true}
	if _, err := d.Resume(ctx, root, request); !errors.Is(err, app.ErrRuntimeUnsupported) {
		t.Fatalf("unqualified native adapter accepted: %v", err)
	}
	selection := map[string]any{"epoch": run.Identity.Epoch, "run": run.Identity.Run, "generation": run.Identity.Generation, "worktree": string(run.Target.Worktree().ID()), "profile": string(run.Profile.ID())}
	for _, mode := range []string{"observations", "input override", "mixed launch"} {
		endpoint, role := "daemon.sock", "management"
		if mode == "observations" {
			endpoint, role = "observations.sock", "observations"
		}
		conn := rawEndpoint(t, root, endpoint, role)
		resume := map[string]any{"epoch": status.Epoch, "request_id": "original-explicit-request", "selection": selection, "work_loss": true}
		wire := map[string]any{"protocol_version": 1, "request_id": "transport-resume-" + strings.ReplaceAll(mode, " ", "-"), "operation": "resume", "resume": resume}
		if mode == "input override" {
			resume["input"] = "synthetic-private-pending-input"
		}
		if mode == "mixed launch" {
			wire["launch"] = map[string]any{"epoch": status.Epoch}
		}
		data, err := json.Marshal(wire)
		if err != nil {
			t.Fatal(err)
		}
		rawFrame(t, conn, string(data))
		got := rawCode(t, conn)
		want := "invalid"
		if mode == "observations" {
			want = "unsupported"
		}
		if got != want {
			t.Fatalf("%s admitted: %s", mode, got)
		}
	}
	after, err := metadata.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if before.Digest != after.Digest || len(owner.List()) != 1 {
		t.Fatal("rejected resume mutated metadata or created another owner")
	}
}
