package runtime_test

import (
	"errors"
	"fmt"
	"testing"

	rt "github.com/duck8823/traceary/domain/runtime"
)

func project(t *testing.T) rt.Project {
	t.Helper()
	p, err := rt.ProjectOf("/repo", "/repo/.git", "owner:device:inode")
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func worktree(t *testing.T, p rt.Project, path string) rt.Worktree {
	t.Helper()
	w, err := rt.WorktreeOf(p.ID(), path, p.CommonDir(), "/store.db", "worktree:store:identity")
	if err != nil {
		t.Fatal(err)
	}
	return w
}
func TestRegistryCanonicalDuplicatesAndProfileTargets(t *testing.T) {
	r := rt.NewRegistry()
	p := project(t)
	if err := r.AddProject(p); err != nil {
		t.Fatal(err)
	}
	if err := r.AddProject(p); !errors.Is(err, rt.ErrDuplicate) {
		t.Fatalf("duplicate: %v", err)
	}
	w := worktree(t, p, "/repo")
	if err := r.AddWorktree(w); err != nil {
		t.Fatal(err)
	}
	if err := r.AddWorktree(w); !errors.Is(err, rt.ErrDuplicate) {
		t.Fatalf("duplicate: %v", err)
	}
	a, err := r.Target(w.ID(), "claude")
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.Target(w.ID(), "codex")
	if err != nil {
		t.Fatal(err)
	}
	if a.Worktree() != b.Worktree() || a.Profile() == b.Profile() {
		t.Fatal("profiles must share the same immutable worktree")
	}
	snapshot := r.Snapshot()
	snapshot.Projects[0] = rt.Project{}
	if got, err := r.Project(p.ID()); err != nil || got != p {
		t.Fatal("snapshot mutated registry")
	}
}
func TestRegistryDeleteIsExplicitRevisionBoundAndDoesNotCascade(t *testing.T) {
	r := rt.NewRegistry()
	p := project(t)
	_ = r.AddProject(p)
	w := worktree(t, p, "/repo")
	_ = r.AddWorktree(w)
	if _, err := r.PlanDeleteProject(p.ID()); !errors.Is(err, rt.ErrInUse) {
		t.Fatalf("project children: %v", err)
	}
	plan, err := r.PlanDeleteWorktree(w.ID())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Snapshot().Worktrees) != 1 {
		t.Fatal("planning deleted worktree")
	}
	_ = r.AddWorktree(worktree(t, p, "/other"))
	if err := r.ApplyDelete(plan); !errors.Is(err, rt.ErrChanged) {
		t.Fatalf("stale plan: %v", err)
	}
	plan, err = r.PlanDeleteWorktree(w.ID())
	if err != nil {
		t.Fatal(err)
	}
	if err = r.ApplyDelete(plan); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Worktree(w.ID()); !errors.Is(err, rt.ErrNotFound) {
		t.Fatal(err)
	}
}
func TestRegistryBoundsAndInvalidRestoration(t *testing.T) {
	r := rt.NewRegistry()
	for i := 0; i < rt.MaxProjects; i++ {
		p, err := rt.ProjectOf("/repo/"+string(rune(1000+i)), "/git/"+string(rune(1000+i)), "identity")
		if err != nil {
			t.Fatal(err)
		}
		if err = r.AddProject(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.AddProject(project(t)); !errors.Is(err, rt.ErrLimit) {
		t.Fatalf("limit: %v", err)
	}
	state := r.Snapshot()
	state.Projects = append(state.Projects, project(t))
	if _, err := rt.RegistryFrom(state); !errors.Is(err, rt.ErrLimit) {
		t.Fatalf("restore: %v", err)
	}
	for _, s := range []string{"", "bad profile", "../escape"} {
		if _, err := rt.ProfileIDFrom(s); err == nil {
			t.Fatalf("accepted %q", s)
		}
	}
}

func TestRegistryWorktreeBoundAndForeignCommonDirectory(t *testing.T) {
	r := rt.NewRegistry()
	p := project(t)
	if err := r.AddProject(p); err != nil {
		t.Fatal(err)
	}
	foreign, err := rt.WorktreeOf(p.ID(), "/foreign", "/other.git", "/store.db", "identity")
	if err != nil {
		t.Fatal(err)
	}
	if err = r.AddWorktree(foreign); !errors.Is(err, rt.ErrForeign) {
		t.Fatalf("foreign common dir: %v", err)
	}
	for i := 0; i < rt.MaxWorktrees; i++ {
		if err = r.AddWorktree(worktree(t, p, fmt.Sprintf("/worktree/%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	if err = r.AddWorktree(worktree(t, p, "/overflow")); !errors.Is(err, rt.ErrLimit) {
		t.Fatalf("worktree limit: %v", err)
	}
}
