//go:build unix

package runtime

import (
	"context"
	"fmt"
	"os"
	"strings"
	"syscall"

	domain "github.com/duck8823/traceary/domain/runtime"
	"golang.org/x/xerrors"
)

// ResolveHistorySource permits only store-inode replacement for read-only history.
// Native Registry.Resolve/Revalidate and immutable launch targets remain unchanged.
func (r RegistryResolver) ResolveHistorySource(ctx context.Context, p domain.Project, w domain.Worktree) (domain.HistorySource, error) {
	if w.Project() != p.ID() || w.CommonDir() != p.CommonDir() {
		return domain.HistorySource{}, domain.ErrForeign
	}
	fresh, err := r.ResolveProject(ctx, p.Root())
	if err != nil {
		return domain.HistorySource{}, xerrors.Errorf("inspect history project: %w", err)
	}
	if fresh != p {
		return domain.HistorySource{}, domain.ErrChanged
	}
	root, common, err := gitIdentity(ctx, w.Path())
	if err != nil {
		return domain.HistorySource{}, err
	}
	if root != w.Path() || common != w.CommonDir() {
		return domain.HistorySource{}, domain.ErrChanged
	}
	rootID, err := ownedIdentity(root, true)
	if err != nil {
		return domain.HistorySource{}, err
	}
	commonID, err := ownedIdentity(common, true)
	if err != nil {
		return domain.HistorySource{}, err
	}
	parts := strings.Split(w.Witness(), "/")
	if len(parts) != 3 {
		return domain.HistorySource{}, domain.ErrUnsupported
	}
	if parts[0] != rootID || parts[1] != commonID {
		return domain.HistorySource{}, domain.ErrChanged
	}
	store, err := canonical(w.Store())
	if err != nil || store != w.Store() {
		return domain.HistorySource{}, domain.ErrUnsafe
	}
	info, err := os.Lstat(store)
	if err != nil {
		return domain.HistorySource{}, domain.ErrUnsafe
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 || info.Mode().Perm()&0022 != 0 {
		return domain.HistorySource{}, domain.ErrUnsafe
	}
	storeID := fmt.Sprintf("%d:%d:%d", stat.Uid, stat.Dev, stat.Ino)
	source, err := domain.HistorySourceOf(w, rootID+"/"+commonID+"/"+storeID)
	if err != nil {
		return domain.HistorySource{}, xerrors.Errorf("construct read-only history source: %w", err)
	}
	if err = ctx.Err(); err != nil {
		return domain.HistorySource{}, xerrors.Errorf("inspect history source budget: %w", err)
	}
	return source, nil
}
