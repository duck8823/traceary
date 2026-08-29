package cli

import (
	"math"
	"testing"
)

func TestReclaimableWarrantsCompact(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name           string
		reclaimable    int64
		compareAgainst int64
		floor          int64
		want           bool
	}{
		{name: "one byte below floor, ratio irrelevant", reclaimable: 256<<20 - 1, compareAgainst: 1 << 30, floor: 256 << 20, want: false},
		{name: "exactly at floor, ratio met 25 percent", reclaimable: 256 << 20, compareAgainst: 1 << 30, floor: 256 << 20, want: true},
		{name: "exactly at warning floor, ratio met 25 percent", reclaimable: 1 << 30, compareAgainst: 4 << 30, floor: 1 << 30, want: true},
		{name: "above floor, below ratio 2 percent", reclaimable: 2 << 30, compareAgainst: 100 << 30, floor: 1 << 30, want: false},
		{name: "exactly at the 10 percent boundary", reclaimable: 1 << 30, compareAgainst: 10 << 30, floor: 1 << 30, want: true},
		{name: "one byte under the 10 percent boundary", reclaimable: 1 << 30, compareAgainst: 10<<30 + 10, floor: 1 << 30, want: false},
		{name: "floor zero disables", reclaimable: 10 << 30, compareAgainst: 20 << 30, floor: 0, want: false},
		{name: "negative floor disables", reclaimable: 10 << 30, compareAgainst: 20 << 30, floor: -1, want: false},
		{name: "zero compareAgainst", reclaimable: 1 << 30, compareAgainst: 0, floor: 1 << 30, want: false},
		{name: "overflow-safe maximum", reclaimable: math.MaxInt64/10 + 1, compareAgainst: math.MaxInt64, floor: 1 << 30, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := reclaimableWarrantsCompact(tc.reclaimable, tc.compareAgainst, tc.floor)
			if got != tc.want {
				t.Fatalf("reclaimableWarrantsCompact(%d, %d, %d) = %v, want %v", tc.reclaimable, tc.compareAgainst, tc.floor, got, tc.want)
			}
		})
	}
}
