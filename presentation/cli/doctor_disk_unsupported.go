//go:build !unix

package cli

import "errors"

func inspectDoctorDiskFree(string) (int64, error) {
	return 0, errors.New("disk headroom inspection unsupported")
}
