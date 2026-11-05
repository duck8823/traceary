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

func equalLifecycleHistoryMetadataJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}
func TestLifecycleAndHistoryReadersAcceptEveryForgetPrefix(t *testing.T) {
	for at := 0; at <= 3; at++ {
		for _, after := range []bool{false, true} {
			if at == 0 && after {
				continue
			}
			t.Run(string(rune('0'+at))+map[bool]string{false: "-before", true: "-after"}[after], func(t *testing.T) {
				ctx := context.Background()
				m, store, root := metadataAuditFixture(t)
				epoch := strings.Repeat("a", 32)
				runs, err := store.ListLaunches(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if runs[0].Identity.Epoch == epoch {
					epoch = strings.Repeat("b", 32)
				}
				state, err := store.RecordEpochStart(ctx, epoch)
				if err != nil || len(state.Interrupted) != 1 {
					t.Fatal("actual lifecycle writer", err)
				}
				if err = store.SetAutoStartOptIn(ctx, true); err != nil {
					t.Fatal(err)
				}
				if err = store.SaveUpdateDeferral(ctx, epoch, domain.UpdateDeferralState{Target: domain.UpdateTarget{Version: "v2", Protocol: 2}, Started: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), Notified24h: true}); err != nil {
					t.Fatal(err)
				}
				binding, err := domain.ShutdownBindingFrom(domain.ShutdownBindingState{Epoch: epoch, PreviewID: strings.Repeat("c", 32), Revision: 7, RosterDigest: strings.Repeat("d", 64), TotalRuns: 1})
				if err != nil {
					t.Fatal(err)
				}
				if err = store.ReserveShutdown(ctx, binding); err != nil {
					t.Fatal(err)
				}
				if err = store.SaveShutdown(ctx, app.ShutdownResult{Binding: binding.Snapshot(), Outcome: domain.ShutdownUnknown}); err != nil {
					t.Fatal(err)
				}
				if err = infra.WriteLifecycleEndpointsForTest(root, epoch); err != nil {
					t.Fatal(err)
				}
				if err = infra.ValidateLifecycleHistoryReadersForTest(ctx, root); err != nil {
					t.Fatal("initial pinned readers", err)
				}
				retained := readMetadataFixture(t, root, "lifecycle.json")
				delete(retained, "revision")
				delete(retained, "interrupted")
				endpoints, err := os.ReadFile(filepath.Join(root, "endpoints.json"))
				if err != nil {
					t.Fatal(err)
				}
				p, err := m.PreviewForget(ctx)
				if err != nil {
					t.Fatal("audit actual writer output", err)
				}
				c, err := domain.MetadataForgetConfirmationOf(p.Snapshot.Digest, true, true)
				if err != nil {
					t.Fatal(err)
				}
				if at > 0 {
					infra.FailMetadataCommitForTest(m, at, after)
				}
				r, err := m.Forget(ctx, c)
				if at == 0 && err != nil || at > 0 && (err == nil || r.Outcome != "" || errors.Is(err, app.ErrMetadataForgetUncertain) != (at > 1 || after)) {
					t.Fatal("wrong actual commit result", err)
				}
				if err = infra.ValidateLifecycleHistoryReadersForTest(ctx, root); err != nil {
					t.Fatal("persisted prefix rejected by actual pinned readers", err)
				}
				fresh, err := infra.OpenMetadataStore(root, infra.MaxRegistryBytes)
				if err != nil {
					t.Fatal(err)
				}
				p, err = fresh.PreviewForget(ctx)
				if err != nil {
					t.Fatal(err)
				}
				c, err = domain.MetadataForgetConfirmationOf(p.Snapshot.Digest, true, true)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = fresh.Forget(ctx, c); err != nil {
					t.Fatal("fresh preview recovery", err)
				}
				if err = infra.ValidateLifecycleHistoryReadersForTest(ctx, root); err != nil {
					t.Fatal("recovery rejected by pinned readers", err)
				}
				final, err := store.ReadLifecycle(ctx)
				_, hasUpdate := final.UpdateDeferral.Value()
				if err != nil || len(final.Interrupted) != 0 || len(final.Shutdowns) != 1 || !final.AutoStartOptIn || !hasUpdate {
					t.Fatal("retained policies/audit lost", err)
				}
				got := readMetadataFixture(t, root, "lifecycle.json")
				delete(got, "revision")
				delete(got, "interrupted")
				if !equalLifecycleHistoryMetadataJSON(retained, got) {
					t.Fatal("retained lifecycle values changed")
				}
				current, err := os.ReadFile(filepath.Join(root, "endpoints.json"))
				if err != nil || !bytes.Equal(endpoints, current) {
					t.Fatal("endpoint evidence mutated", err)
				}
				before, err := os.ReadFile(filepath.Join(root, "lifecycle.json"))
				if err != nil {
					t.Fatal(err)
				}
				p, err = fresh.PreviewForget(ctx)
				if err != nil {
					t.Fatal(err)
				}
				c, err = domain.MetadataForgetConfirmationOf(p.Snapshot.Digest, true, true)
				if err != nil {
					t.Fatal(err)
				}
				r, err = fresh.Forget(ctx, c)
				if err != nil || r.Outcome != "unchanged" {
					t.Fatal("no-op changed metadata", err)
				}
				current, err = os.ReadFile(filepath.Join(root, "lifecycle.json"))
				if err != nil || !bytes.Equal(before, current) {
					t.Fatal("no-op rewrote retained lifecycle", err)
				}
			})
		}
	}
}
