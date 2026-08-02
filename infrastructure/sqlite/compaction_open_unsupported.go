//go:build !unix

package sqlite

import (
	"errors"
	"os"
)

func openFileNoFollow(string, int, os.FileMode) (*os.File, error) {
	return nil, errors.New("no-follow file opening unsupported")
}
