//go:build unix

package cli

import (
	"math"
	"testing"
)

func TestCheckedDoctorDiskFreeRejectsInvalidAndOverflow(t *testing.T) {
	for _, tc := range []struct {
		blocks, size int64
		ok           bool
	}{{0, 4096, true}, {1, 4096, true}, {-1, 4096, false}, {1, -1, false}, {math.MaxInt64, 2, false}, {math.MaxInt64, 1, true}} {
		got, err := checkedDoctorDiskFree(tc.blocks, tc.size)
		if (err == nil) != tc.ok {
			t.Fatalf("blocks=%d size=%d got=%d err=%v", tc.blocks, tc.size, got, err)
		}
	}
}
