//go:build !unix

package cli

import "errors"

func volumeAvailableBytes(string) (uint64, error) {
	return 0, errors.New("volume free-space probe is unavailable on this platform")
}
