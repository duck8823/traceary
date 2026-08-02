//go:build !darwin && !linux

package sqlite

import (
	"context"
	"errors"
)

type advisoryLease interface{ Close() error }

func validateStoreLinkIdentity(string) error {
	return errors.New("cross-process store lease is unsupported on this operating system")
}

func acquireAdvisoryLease(context.Context, string, bool) (advisoryLease, error) {
	return nil, errors.New("cross-process store lease is unsupported on this operating system")
}
