//go:build darwin || linux

package runtime_test

import (
	"context"
	"errors"
	"testing"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	infra "github.com/duck8823/traceary/infrastructure/runtime"
)

func TestHistorySDKUsesRegisteredSourceAndCoarseHintsWithoutNativeReceiver(t *testing.T) {
	f := historySQLite(t)
	ctx := context.Background()
	root := daemonRoot(t)
	store, err := infra.OpenRegistryStore(root, infra.MaxRegistryBytes)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := app.NewRegistry(store, infra.RegistryResolver{})
	if err != nil {
		t.Fatal(err)
	}
	p, err := registry.RegisterProject(ctx, f.repo)
	if err != nil {
		t.Fatal(err)
	}
	w, err := registry.RegisterWorktree(ctx, p.ID(), f.repo, f.path)
	if err != nil {
		t.Fatal(err)
	}
	if w.ID() != f.request.Worktree {
		t.Fatal("different registry target")
	}
	d := daemon(t, nil, infra.DaemonOptions{HistoryFactory: infra.NewHistoryService})
	_, _ = startDaemon(t, d, root)
	status, err := d.Status(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	request := app.RemoteHistoryRequest{Epoch: status.Epoch, Request: f.request}
	f.save(t, "a", f.base, f.repo)
	f.save(t, "b", f.base, f.repo)
	f.save(t, "c", f.base, f.repo)
	first, err := d.HistoryPage(ctx, root, request)
	if err != nil || len(first.Page.Events()) != 2 || !first.Page.More() || first.Hint.Revision != 0 {
		t.Fatal("SDK lost committed source/page", err)
	}
	if err = d.HintHistory(ctx, root); err != nil {
		t.Fatal("coarse hint depended on native receiver", err)
	}
	second, err := d.HistoryPage(ctx, root, request)
	if err != nil || second.Hint.Revision != 1 || second.Page.Events()[0].Snapshot().ID != first.Page.Events()[0].Snapshot().ID {
		t.Fatal("hint mutated data or was not advisory", err)
	}
	request.Request.Selection, err = domain.HistorySelectionOf(f.request.Selection.From(), f.request.Selection.To(), 2, first.Page.Next())
	if err != nil {
		t.Fatal(err)
	}
	request.Request.ExpectedWitness = first.Page.Source().Witness()
	last, err := d.HistoryPage(ctx, root, request)
	if err != nil || last.Page.More() || len(last.Page.Events()) != 1 || last.Page.Events()[0].Snapshot().ID != "a" {
		t.Fatal("continuation skipped row", err)
	}
	request.Epoch = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, err = d.HistoryPage(ctx, root, request); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("wrong epoch accepted", err)
	}
	if status.ObservationDelivery != "unavailable" || len(status.Capabilities) > 16 {
		t.Fatal("history invented native observation/configuration")
	}
}
func TestHistorySDKAbsentFactoryAndDaemonNeverInitializeAStore(t *testing.T) {
	d := daemon(t, nil, infra.DaemonOptions{})
	root := daemonRoot(t)
	_, _ = startDaemon(t, d, root)
	if err := d.HintHistory(context.Background(), root); !errors.Is(err, app.ErrRuntimeUnsupported) {
		t.Fatal("unconfigured history accepted hint", err)
	}
}
