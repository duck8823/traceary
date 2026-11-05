//go:build darwin || linux

package runtime_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	infra "github.com/duck8823/traceary/infrastructure/runtime"
)

func TestUpdateHandoffJournalFinalizesAndRetiresInOneEnvelope(t *testing.T) {
	ctx := context.Background()
	root := daemonRoot(t)
	store, err := infra.NewExecutionStore(root, infra.MaxRegistryBytes)
	if err != nil {
		t.Fatal(err)
	}
	epoch := strings.Repeat("a", 32)
	if _, err = store.RecordEpochStart(ctx, epoch); err != nil {
		t.Fatal(err)
	}
	target := domain.UpdateTarget{Version: "v2", Protocol: 2}
	state := domain.UpdateDeferralState{Target: target, Started: time.Now().UTC().Round(0).Add(-25 * time.Hour), Notified24h: true}
	if err = store.SaveUpdateDeferral(ctx, epoch, state); err != nil {
		t.Fatal(err)
	}
	digest, err := app.ShutdownRosterDigest(nil)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := domain.ShutdownBindingFrom(domain.ShutdownBindingState{Epoch: epoch, PreviewID: strings.Repeat("b", 32), Revision: 1, RosterDigest: digest})
	if err != nil {
		t.Fatal(err)
	}
	result := app.ShutdownResult{Binding: binding.Snapshot(), Outcome: domain.ShutdownDraining}
	path := filepath.Join(root, "lifecycle.json")
	read := func() []byte {
		t.Helper()
		b, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		return b
	}
	before := read()
	if err = store.SaveUpdateHandoff(ctx, result, target); !errors.Is(err, domain.ErrNotFound) || !bytes.Equal(before, read()) {
		t.Fatal("unreserved handoff retired or changed metadata", err)
	}
	if err = store.ReserveShutdown(ctx, binding); err != nil {
		t.Fatal(err)
	}
	pending := read()
	if err = store.SaveUpdateHandoff(ctx, result, domain.UpdateTarget{Version: "v3", Protocol: 3}); !errors.Is(err, domain.ErrChanged) || !bytes.Equal(pending, read()) {
		t.Fatal("handoff cleared another target", err)
	}
	wrong := result
	wrong.ClosedTerminals = 1
	if err = store.SaveUpdateHandoff(ctx, wrong, target); !errors.Is(err, domain.ErrInvalid) || !bytes.Equal(pending, read()) {
		t.Fatal("nonempty/effectful handoff changed metadata", err)
	}
	if err = store.SaveUpdateHandoff(ctx, result, target); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.ReadLifecycle(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, found := snapshot.UpdateDeferral.Value(); found || len(snapshot.Shutdowns) != 1 || snapshot.Shutdowns[0] != result || snapshot.Clean {
		t.Fatal("outcome and deferral retirement split or fabricated clean")
	}
	if err = store.SaveUpdateHandoff(ctx, result, target); err != nil {
		t.Fatal("exact terminal metadata replay rejected", err)
	}
	if err = store.RecordCleanShutdown(ctx, epoch); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveUpdateHandoff(ctx, result, target); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("retirement mutated a completed epoch", err)
	}
}
