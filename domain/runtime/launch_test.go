package runtime_test

import (
	"errors"
	"strings"
	"testing"

	domain "github.com/duck8823/traceary/domain/runtime"
)

func launchProfile(t *testing.T) domain.HostProfile {
	t.Helper()
	p, err := domain.HostProfileFrom(domain.HostProfileState{ID: "codex-fixture", Host: "codex", Executable: "/owned/agent", ExecutableWitness: "fixture-owned-inode-sha256", Version: "fixture 1"})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func launchTarget(t *testing.T) domain.Target {
	t.Helper()
	p, err := domain.ProjectOf("/project", "/project/.git", "project-witness")
	if err != nil {
		t.Fatal(err)
	}
	w, err := domain.WorktreeOf(p.ID(), "/project", "/project/.git", "/store.db", "worktree-witness")
	if err != nil {
		t.Fatal(err)
	}
	r := domain.NewRegistry()
	if err = r.AddProject(p); err != nil {
		t.Fatal(err)
	}
	if err = r.AddWorktree(w); err != nil {
		t.Fatal(err)
	}
	target, err := r.Target(w.ID(), "codex-fixture")
	if err != nil {
		t.Fatal(err)
	}
	return target
}
func TestHostProfilePreservesFixedOperatorPolicyAndRejectsUnsafeValues(t *testing.T) {
	p := launchProfile(t)
	s := p.Snapshot()
	s.Executable = "/other"
	if p.Snapshot().Executable != "/owned/agent" {
		t.Fatal("profile rebound")
	}
	for _, change := range []func(*domain.HostProfileState){func(s *domain.HostProfileState) { s.ID = "../bad" }, func(s *domain.HostProfileState) { s.Host = "shell" }, func(s *domain.HostProfileState) { s.Executable = "relative" }, func(s *domain.HostProfileState) { s.Version = "" }, func(s *domain.HostProfileState) { s.Version = strings.Repeat("x", 257) }, func(s *domain.HostProfileState) { s.ExecutableWitness = "" }} {
		s = p.Snapshot()
		change(&s)
		if _, err := domain.HostProfileFrom(s); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("unsafe profile: %v", err)
		}
	}
}
func TestLaunchDescriptorTransitionsNeverRebindTargetOrGeneration(t *testing.T) {
	target := launchTarget(t)
	profile := launchProfile(t)
	id := domain.LaunchIdentity{Run: strings.Repeat("1", 32), Generation: strings.Repeat("2", 32), Epoch: strings.Repeat("3", 32)}
	d, err := domain.LaunchDescriptorOf(id, target, profile)
	if err != nil {
		t.Fatal(err)
	}
	if d.State() != domain.RunReserved {
		t.Fatal("not reserved")
	}
	if err = d.Started(321); err != nil {
		t.Fatal(err)
	}
	if err = d.Started(999); !errors.Is(err, domain.ErrChanged) {
		t.Fatalf("rebound child: %v", err)
	}
	if err = d.MarkUncertain(); err != nil {
		t.Fatal(err)
	}
	if err = d.Exit(7); err != nil {
		t.Fatal(err)
	}
	if d.State() != domain.RunExited || d.Snapshot().ExitCode != 7 || d.Identity() != id || d.Target() != target {
		t.Fatal("incorrect immutable exit snapshot")
	}
	if err = d.Exit(0); !errors.Is(err, domain.ErrChanged) {
		t.Fatalf("duplicate exit mutated outcome: %v", err)
	}
	if _, err = domain.LaunchDescriptorOf(domain.LaunchIdentity{}, target, profile); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("invalid launch identity: %v", err)
	}
}
