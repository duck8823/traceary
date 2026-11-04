//go:build unix

package runtime_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	infra "github.com/duck8823/traceary/infrastructure/runtime"
)

func TestHistorySourceReopensStoreReplacementWithoutRenewingNativeTarget(t *testing.T) {
	root, repo, path := fixture(t)
	registry, metadata := service(t, root)
	ctx := context.Background()
	p, err := registry.RegisterProject(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	w, err := registry.RegisterWorktree(ctx, p.ID(), repo, path)
	if err != nil {
		t.Fatal(err)
	}
	target, err := registry.Resolve(ctx, w.ID(), "fixture")
	if err != nil {
		t.Fatal(err)
	}
	sources, err := app.NewRegisteredHistorySources(metadata, infra.RegistryResolver{})
	if err != nil {
		t.Fatal(err)
	}
	before, err := sources.ResolveHistorySource(ctx, w.ID())
	if err != nil || before.StoreIdentityChanged() {
		t.Fatal(err)
	}
	if err = os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("replacement store"), 0600); err != nil {
		t.Fatal(err)
	}
	after, err := sources.ResolveHistorySource(ctx, w.ID())
	if err != nil || !after.StoreIdentityChanged() || after.Worktree() != w || after.Witness() == before.Witness() {
		t.Fatal("read-only store replacement lost", err)
	}
	if err = registry.Revalidate(ctx, target, "fixture"); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("history renewed native store authority", err)
	}
	if err = sources.RevalidateHistorySource(ctx, before); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("stale read source accepted", err)
	}
	state, err := registry.List(ctx)
	if err != nil || len(state.Worktrees) != 1 || state.Worktrees[0] != w {
		t.Fatal("read-only resolver rebound Registry", err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(path+".old", path); err != nil {
		t.Fatal(err)
	}
	if _, err = sources.ResolveHistorySource(ctx, w.ID()); err == nil {
		t.Fatal("history followed store symlink")
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = os.Link(path+".old", path); err != nil {
		t.Fatal(err)
	}
	if _, err = sources.ResolveHistorySource(ctx, w.ID()); err == nil {
		t.Fatal("history accepted hardlinked store")
	}
}

func TestHistorySourceRejectsReplacedGitWorktreeAndInvalidWitness(t *testing.T) {
	root, repo, path := fixture(t)
	registry, _ := service(t, root)
	ctx := context.Background()
	p, err := registry.RegisterProject(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	w, err := registry.RegisterWorktree(ctx, p.ID(), repo, path)
	if err != nil {
		t.Fatal(err)
	}
	bad, err := domain.WorktreeOf(p.ID(), w.Path(), w.CommonDir(), w.Store(), strings.Repeat("x", 40))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = (infra.RegistryResolver{}).ResolveHistorySource(ctx, p, bad); err == nil {
		t.Fatal("unknown physical witness format accepted")
	}
	if err = os.Rename(repo, repo+".old"); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "init", "--quiet")
	if _, err = (infra.RegistryResolver{}).ResolveHistorySource(ctx, p, w); err == nil {
		t.Fatal("replacement Git root accepted")
	}
}
