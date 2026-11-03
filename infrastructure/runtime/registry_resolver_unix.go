//go:build unix

// Package runtime implements local runtime I/O without changing core store rules.
package runtime

import (
	"context"
	"fmt"
	"golang.org/x/xerrors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	domain "github.com/duck8823/traceary/domain/runtime"
)

// RegistryResolver checks canonical owned Git worktree/store identities.
type RegistryResolver struct{}

// ResolveProject requires a Git worktree root, not an arbitrary subdirectory.
func (RegistryResolver) ResolveProject(ctx context.Context, path string) (domain.Project, error) {
	root, common, err := gitIdentity(ctx, path)
	if err != nil {
		return domain.Project{}, err
	}
	rootID, err := ownedIdentity(root, true)
	if err != nil {
		return domain.Project{}, err
	}
	commonID, err := ownedIdentity(common, true)
	if err != nil {
		return domain.Project{}, err
	}
	p, err := domain.ProjectOf(root, common, rootID+"/"+commonID)
	if err != nil {
		return domain.Project{}, xerrors.Errorf("construct project identity: %w", err)
	}
	return p, nil
}

// ResolveWorktree checks project membership and an existing owned regular core store.
func (RegistryResolver) ResolveWorktree(ctx context.Context, p domain.Project, path, store string) (domain.Worktree, error) {
	root, common, err := gitIdentity(ctx, path)
	if err != nil {
		return domain.Worktree{}, err
	}
	if common != p.CommonDir() {
		return domain.Worktree{}, domain.ErrForeign
	}
	rootID, err := ownedIdentity(root, true)
	if err != nil {
		return domain.Worktree{}, err
	}
	commonID, err := ownedIdentity(common, true)
	if err != nil {
		return domain.Worktree{}, err
	}
	canonicalStore, err := canonical(store)
	if err != nil {
		return domain.Worktree{}, err
	}
	storeID, err := ownedIdentity(canonicalStore, false)
	if err != nil {
		return domain.Worktree{}, err
	}
	w, err := domain.WorktreeOf(p.ID(), root, common, canonicalStore, rootID+"/"+commonID+"/"+storeID)
	if err != nil {
		return domain.Worktree{}, xerrors.Errorf("construct worktree identity: %w", err)
	}
	return w, nil
}
func canonical(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", domain.ErrInvalid
	}
	s, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", domain.ErrUnsafe
	}
	return s, nil
}
func ownedIdentity(path string, directory bool) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", domain.ErrUnsafe
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 {
		return "", domain.ErrUnsafe
	}
	if directory && !info.IsDir() || !directory && !info.Mode().IsRegular() {
		return "", domain.ErrUnsafe
	}
	return fmt.Sprintf("%d:%d:%d", stat.Uid, stat.Dev, stat.Ino), nil
}
func gitIdentity(ctx context.Context, path string) (string, string, error) {
	canonicalPath, err := canonical(path)
	if err != nil {
		return "", "", err
	}
	if _, err = ownedIdentity(canonicalPath, true); err != nil {
		return "", "", err
	}
	// Fixed local plumbing only; no hooks, network, inherited repository selection or config.
	git := func(args ...string) (string, error) {
		bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		cmd := exec.CommandContext(bounded, "git", append([]string{"-C", canonicalPath, "rev-parse"}, args...)...)
		cmd.Env = make([]string, 0, len(os.Environ()))
		for _, v := range os.Environ() {
			key, _, _ := strings.Cut(v, "=")
			if !strings.HasPrefix(key, "GIT_") {
				cmd.Env = append(cmd.Env, v)
			}
		}
		cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_OPTIONAL_LOCKS=0")
		output, err := cmd.Output()
		if err != nil {
			if bounded.Err() != nil {
				return "", bounded.Err()
			}
			return "", domain.ErrUnsafe
		}
		if len(output) > 8192 {
			return "", domain.ErrLimit
		}
		return strings.TrimSuffix(string(output), "\n"), nil
	}
	top, err := git("--show-toplevel")
	if err != nil {
		return "", "", err
	}
	top, err = canonical(top)
	if err != nil {
		return "", "", err
	}
	if top != canonicalPath {
		return "", "", domain.ErrInvalid
	}
	common, err := git("--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", "", err
	}
	common, err = canonical(common)
	if err != nil {
		return "", "", err
	}
	return top, common, nil
}
