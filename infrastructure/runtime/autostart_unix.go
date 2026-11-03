//go:build darwin || linux

package runtime

import (
	"context"
	"path/filepath"
	"unicode"
	"unicode/utf8"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"golang.org/x/xerrors"
)

// OpenAutoStart reads an existing private root without constructing native launch services.
func (LocalOperatorFactory) OpenAutoStart(ctx context.Context, root string) (*app.AutoStart, error) {
	return openAutoStart(ctx, root, false)
}

// CreateAutoStart permits only explicitly selected local preference metadata creation.
func (LocalOperatorFactory) CreateAutoStart(ctx context.Context, root string) (*app.AutoStart, error) {
	return openAutoStart(ctx, root, true)
}
func printableAutoStartPath(path string) bool {
	if !utf8.ValidString(path) {
		return false
	}
	for _, r := range path {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}
func openAutoStart(ctx context.Context, root string, create bool) (*app.AutoStart, error) {
	if err := ctx.Err(); err != nil {
		return nil, xerrors.Errorf("open auto-start budget: %w", err)
	}
	if !validRuntimeRoot(root) || !printableAutoStartPath(root) {
		return nil, domain.ErrUnsafe
	}
	var store *ExecutionStore
	var err error
	if create {
		store, err = NewExecutionStore(root, MaxRegistryBytes)
	} else {
		store, err = OpenExecutionStore(root, MaxRegistryBytes)
	}
	if err != nil {
		return nil, err
	}
	// The no-follow store constructor already rejected symlink parents before
	// any mkdir. This additional canonical check never normalizes the target.
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil || canonical != root {
		return nil, domain.ErrUnsafe
	}
	service, err := app.NewAutoStart(store, store, localAutoStartDescriptor{root: root, witness: store.metadata.witness})
	if err != nil {
		return nil, xerrors.Errorf("compose auto-start metadata: %w", err)
	}
	return service, nil
}

// localAutoStartDescriptor binds rendering to the store's physical root identity.
// Executable/platform selection is never accepted from a CLI argument or metadata.
type localAutoStartDescriptor struct{ root, witness string }
