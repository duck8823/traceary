//go:build !unix

package sqlite

import "errors"

func availableBytes(string) (uint64, error) {
	return 0, errors.New("filesystem capacity inspection unsupported")
}
