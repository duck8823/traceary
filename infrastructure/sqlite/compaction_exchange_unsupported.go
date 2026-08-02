//go:build !darwin && !linux

package sqlite

import "errors"

func atomicExchange(_, _ string) error {
	return errors.New("atomic store exchange is unsupported on this platform")
}
