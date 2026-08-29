package cli

// doctorReclaimableWarnBytes is the floor `doctor` uses before it names
// reclaimable pages as a store-growth reason. Doctor is on-demand and
// informational, so it speaks sooner than the post-run trailer, whose floor is
// the operator-visible compact.reclaim_warn_bytes (default 1 GiB).
const doctorReclaimableWarnBytes int64 = 256 << 20

// reclaimableWarnRatioPercent keeps a reclaim report proportional: free pages
// worth less than this share of the store are not worth a rewrite.
const reclaimableWarnRatioPercent = 10

// reclaimableWarrantsCompact is the single reclaim rule shared by doctor's
// store-size checks and the post-run stderr trailer. reclaimable is always
// page_size * freelist_count (the O(1) pragma signal); compareAgainst is the
// larger of the filesystem size and page_size * page_count. A floor of zero or
// less disables the rule, which is how compact.reclaim_warn_bytes: 0 turns the
// trailer off.
func reclaimableWarrantsCompact(reclaimable, compareAgainst, floor int64) bool {
	if floor <= 0 || reclaimable < floor {
		return false
	}
	return ratioAtLeast(reclaimable, compareAgainst, reclaimableWarnRatioPercent)
}
