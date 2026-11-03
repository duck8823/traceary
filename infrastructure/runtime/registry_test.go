//go:build unix

package runtime_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	infra "github.com/duck8823/traceary/infrastructure/runtime"
)

func fixture(t *testing.T) (string, string, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(root, "repo")
	if err = os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "init", "--quiet")
	git(t, repo, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "--quiet", "-m", "fixture")
	store := filepath.Join(root, "store.db")
	if err = os.WriteFile(store, []byte("synthetic store identity"), 0600); err != nil {
		t.Fatal(err)
	}
	return root, repo, store
}
func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	for _, e := range os.Environ() {
		k, _, _ := strings.Cut(e, "=")
		if !strings.HasPrefix(k, "GIT_") {
			cmd.Env = append(cmd.Env, e)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git fixture: %v %s", err, output)
	}
}
func service(t *testing.T, root string) (*app.Registry, *infra.RegistryStore) {
	t.Helper()
	store, err := infra.NewRegistryStore(filepath.Join(root, "metadata"), infra.MaxRegistryBytes)
	if err != nil {
		t.Fatal(err)
	}
	s, err := app.NewRegistry(store, infra.RegistryResolver{})
	if err != nil {
		t.Fatal(err)
	}
	return s, store
}
func TestRegistryServiceCanonicalAliasesMembershipAndPinnedSelections(t *testing.T) {
	ctx := context.Background()
	root, repo, db := fixture(t)
	s, _ := service(t, root)
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(repo, alias); err != nil {
		t.Fatal(err)
	}
	p, err := s.RegisterProject(ctx, alias)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RegisterProject(ctx, repo); !errors.Is(err, domain.ErrDuplicate) {
		t.Fatalf("alias duplicate: %v", err)
	}
	w, err := s.RegisterWorktree(ctx, p.ID(), alias, db)
	if err != nil {
		t.Fatal(err)
	}
	claude, err := s.Resolve(ctx, w.ID(), "claude")
	if err != nil {
		t.Fatal(err)
	}
	codex, err := s.Resolve(ctx, w.ID(), "codex")
	if err != nil {
		t.Fatal(err)
	}
	if claude.Worktree() != codex.Worktree() {
		t.Fatal("target selection mutated binding")
	}
	if err = s.Revalidate(ctx, claude, "codex"); !errors.Is(err, domain.ErrChanged) {
		t.Fatalf("wrong profile: %v", err)
	}
	other := filepath.Join(root, "other")
	if err = os.Mkdir(other, 0700); err != nil {
		t.Fatal(err)
	}
	git(t, other, "init", "--quiet")
	if _, err = s.RegisterWorktree(ctx, p.ID(), other, db); !errors.Is(err, domain.ErrForeign) {
		t.Fatalf("foreign worktree: %v", err)
	}
	linked := filepath.Join(root, "linked")
	git(t, repo, "worktree", "add", "--quiet", "-b", "fixture-linked", linked)
	if _, err = s.RegisterWorktree(ctx, p.ID(), linked, db); err != nil {
		t.Fatalf("linked worktree: %v", err)
	}
	if err = os.Rename(db, db+".original"); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(db, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = s.Revalidate(ctx, claude, "claude"); !errors.Is(err, domain.ErrChanged) {
		t.Fatalf("replaced store: %v", err)
	}
}
func TestRegistryServiceDeletePreviewCancelAndMetadataOnlyRemoval(t *testing.T) {
	ctx := context.Background()
	root, repo, db := fixture(t)
	s, _ := service(t, root)
	p, err := s.RegisterProject(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.RegisterWorktree(ctx, p.ID(), repo, db)
	if err != nil {
		t.Fatal(err)
	}
	target, err := s.Resolve(ctx, w.ID(), "codex")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.PlanDeleteWorktree(ctx, w.ID())
	if err != nil {
		t.Fatal(err)
	}
	exported, err := s.Export(ctx)
	if err != nil || len(exported.Worktrees) != 1 {
		t.Fatalf("preview/export: %v", err)
	}
	if err = s.Revalidate(ctx, target, "codex"); err != nil {
		t.Fatal("preview changed selection", err)
	}
	if err = s.ApplyDelete(ctx, plan); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{repo, db, filepath.Join(repo, ".git")} {
		if _, err = os.Stat(path); err != nil {
			t.Fatalf("delete touched user files: %v", err)
		}
	}
	if err = s.Revalidate(ctx, target, "codex"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("removed target still valid: %v", err)
	}
	plan, err = s.PlanDeleteProject(ctx, p.ID())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ApplyDelete(ctx, plan); err != nil {
		t.Fatal(err)
	}
}
func TestRegistryStoreRejectsUnsafeVersionAndCorruptionWithoutOverwrite(t *testing.T) {
	for _, test := range []struct {
		name, body string
		want       error
	}{
		{"new schema", `{"version":2,"revision":0,"projects":[],"worktrees":[]}`, domain.ErrUnsupported},
		{"unknown field", `{"version":1,"revision":0,"extra":true}`, domain.ErrInvalid},
		{"trailing object", `{"version":1,"revision":0} {}`, domain.ErrInvalid},
		{"duplicate version", `{"version":2,"version":1,"revision":0}`, domain.ErrInvalid},
		{"bad identity", `{"version":1,"revision":1,"projects":[{"id":"forged","root":"/repo","common":"/git","witness":"identity"}]}`, domain.ErrInvalid},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			root = filepath.Join(root, "metadata")
			store, err := infra.NewRegistryStore(root, infra.MaxRegistryBytes)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "registry.json")
			if err = os.WriteFile(path, []byte(test.body), 0600); err != nil {
				t.Fatal(err)
			}
			err = store.Update(context.Background(), func(r *domain.Registry) error {
				p, e := domain.ProjectOf("/repo", "/git", "identity")
				if e != nil {
					return fmt.Errorf("create registry fixture: %w", e)
				}
				return r.AddProject(p)
			})
			if !errors.Is(err, test.want) {
				t.Fatalf("wanted %v got %v", test.want, err)
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != test.body {
				t.Fatal("invalid metadata overwritten", err)
			}
		})
	}
}
func TestRegistryStoreSymlinkPermissionsAndBudgetFailures(t *testing.T) {
	root, repo, _ := fixture(t)
	s, store := service(t, root)
	if _, err := s.RegisterProject(context.Background(), repo); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "metadata", "registry.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tight, err := infra.NewRegistryStore(filepath.Dir(path), int64(len(before)+10))
	if err != nil {
		t.Fatal(err)
	}
	err = tight.Update(context.Background(), func(r *domain.Registry) error {
		p, e := domain.ProjectOf("/another", "/another/.git", "new identity")
		if e != nil {
			return fmt.Errorf("create registry fixture: %w", e)
		}
		return r.AddProject(p)
	})
	if !errors.Is(err, domain.ErrLimit) {
		t.Fatalf("budget: %v", err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("overflow replaced good metadata")
	}
	if err = os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Load(context.Background()); !errors.Is(err, domain.ErrUnsafe) {
		t.Fatalf("permissions: %v", err)
	}
	if err = os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(path, path+".owned"); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(path+".owned", path); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Load(context.Background()); !errors.Is(err, domain.ErrUnsafe) {
		t.Fatalf("leaf symlink: %v", err)
	}
	alias := filepath.Join(root, "alias")
	if err = os.Symlink(filepath.Dir(path), alias); err != nil {
		t.Fatal(err)
	}
	if _, err = infra.NewRegistryStore(alias, infra.MaxRegistryBytes); !errors.Is(err, domain.ErrUnsafe) {
		t.Fatalf("root symlink: %v", err)
	}
}
func TestRegistryIndependentWritersAndCanceledUpdate(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Join(root, "metadata")
	a, err := infra.NewRegistryStore(root, infra.MaxRegistryBytes)
	if err != nil {
		t.Fatal(err)
	}
	b, err := infra.NewRegistryStore(root, infra.MaxRegistryBytes)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			store := a
			if i%2 == 1 {
				store = b
			}
			errs <- store.Update(context.Background(), func(r *domain.Registry) error {
				p, e := domain.ProjectOf(fmt.Sprintf("/repo%d", i), fmt.Sprintf("/git%d", i), "identity")
				if e != nil {
					return fmt.Errorf("create registry fixture: %w", e)
				}
				return r.AddProject(p)
			})
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	r, err := a.Load(context.Background())
	if err != nil || len(r.Snapshot().Projects) != 20 {
		t.Fatalf("lost writer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	err = a.Update(ctx, func(r *domain.Registry) error {
		cancel()
		p, e := domain.ProjectOf("/cancel", "/cancel.git", "identity")
		if e != nil {
			return fmt.Errorf("create registry fixture: %w", e)
		}
		return r.AddProject(p)
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	r, err = a.Load(context.Background())
	if err != nil || len(r.Snapshot().Projects) != 20 {
		t.Fatal("canceled mutation committed", err)
	}
}

func TestRegistryIgnoresInheritedGitSelectionAndRevalidatesRoot(t *testing.T) {
	root, repo, db := fixture(t)
	s, _ := service(t, root)
	t.Setenv("GIT_DIR", filepath.Join(root, "nonexistent.git"))
	t.Setenv("GIT_WORK_TREE", filepath.Join(root, "foreign"))
	p, err := s.RegisterProject(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.RegisterWorktree(context.Background(), p.ID(), repo, db)
	if err != nil {
		t.Fatal(err)
	}
	target, err := s.Resolve(context.Background(), w.ID(), "codex")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(repo, repo+".old"); err != nil {
		t.Fatal(err)
	}
	if err = s.Revalidate(context.Background(), target, "codex"); err == nil {
		t.Fatal("missing target accepted")
	}
	if err = os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "init", "--quiet")
	if err = s.Revalidate(context.Background(), target, "codex"); err == nil {
		t.Fatal("replacement target accepted")
	}
}
func TestRegistryStoreRejectsRootReplacementAndLockSymlink(t *testing.T) {
	root, repo, _ := fixture(t)
	s, store := service(t, root)
	if _, err := s.RegisterProject(context.Background(), repo); err != nil {
		t.Fatal(err)
	}
	metadata := filepath.Join(root, "metadata")
	if err := os.Rename(metadata, metadata+".original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(metadata, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(context.Background()); !errors.Is(err, domain.ErrChanged) {
		t.Fatalf("root replaced: %v", err)
	}
	fresh, err := infra.NewRegistryStore(metadata, infra.MaxRegistryBytes)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "marker")
	if err = os.WriteFile(marker, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(marker, filepath.Join(metadata, "registry.lock")); err != nil {
		t.Fatal(err)
	}
	if _, err = fresh.Load(context.Background()); !errors.Is(err, domain.ErrUnsafe) {
		t.Fatalf("lock symlink: %v", err)
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "untouched" {
		t.Fatal("lock opened foreign file", err)
	}
	if err = os.Chmod(metadata, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err = infra.NewRegistryStore(metadata, infra.MaxRegistryBytes); !errors.Is(err, domain.ErrUnsafe) {
		t.Fatalf("unsafe root mode: %v", err)
	}
}
func TestRegistryStoreProcessHelper(t *testing.T) {
	if os.Getenv("TRACEARY_TEST_REGISTRY_CHILD") != "1" {
		t.Skip("subprocess fixture only")
	}
	store, err := infra.NewRegistryStore(os.Getenv("TRACEARY_TEST_REGISTRY_ROOT"), infra.MaxRegistryBytes)
	if err != nil {
		t.Fatal(err)
	}
	prefix := os.Getenv("TRACEARY_TEST_REGISTRY_PREFIX")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for i := 0; i < 5; i++ {
		err = store.Update(ctx, func(r *domain.Registry) error {
			p, e := domain.ProjectOf(fmt.Sprintf("/repo%s-%d", prefix, i), fmt.Sprintf("/git%s-%d", prefix, i), "identity")
			if e != nil {
				return fmt.Errorf("create registry fixture: %w", e)
			}
			return r.AddProject(p)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
func TestRegistryStoreSerializesIndependentProcesses(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Join(root, "metadata")
	store, err := infra.NewRegistryStore(root, infra.MaxRegistryBytes)
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	commands := make([]*exec.Cmd, 4)
	for i := range commands {
		cmd := exec.CommandContext(ctx, binary, "-test.run=^TestRegistryStoreProcessHelper$")
		cmd.Env = append(os.Environ(), "TRACEARY_TEST_REGISTRY_CHILD=1", "TRACEARY_TEST_REGISTRY_ROOT="+root, fmt.Sprintf("TRACEARY_TEST_REGISTRY_PREFIX=%d", i))
		if err = cmd.Start(); err != nil {
			t.Fatal(err)
		}
		commands[i] = cmd
	}
	for _, cmd := range commands {
		if err = cmd.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	r, err := store.Load(context.Background())
	if err != nil || len(r.Snapshot().Projects) != 20 {
		t.Fatalf("lost subprocess writes: %v", err)
	}
}
