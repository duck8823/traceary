//go:build !darwin && !linux

package sqlite

import (
	"context"
)

type advisoryLease interface{ Close() error }

func validateStoreLinkIdentity(string) error {
	return ErrPreparedUpgradeUnsupported
}

func acquireAdvisoryLease(context.Context, string, bool) (advisoryLease, error) {
	return nil, ErrPreparedUpgradeUnsupported
}
