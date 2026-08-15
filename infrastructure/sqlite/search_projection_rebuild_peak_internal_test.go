package sqlite

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestSearchProjectionRebuildPeakExceedsConfiguredBudget is the #1753
// scratch measurement. Two generations coexist until cleanup, so the family
// can exceed the *new* generation's --index-family-bytes. Accepting that peak
// is the release promise: reclaim-first would drop the previous complete
// family before the new one is searchable.
func TestSearchProjectionRebuildPeakExceedsConfiguredBudget(t *testing.T) {
	var events []struct{ id, body, created string }
	for i := 0; i < 12; i++ {
		events = append(events, struct{ id, body, created string }{
			id:      "peak-" + itoa(i),
			body:    strings.Repeat("rebuild peak corpus token ", 120),
			created: time.Date(2026, 6, 1, i, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
		})
	}
	store, db := newCapacityTestStore(t, events)
	now := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	ctx := context.Background()
	driveToCompletion(t, store, capacityBudget(64<<20), now)

	gen1Family, ev := store.measureSearchProjectionFamilyBytes(ctx, db)
	if ev.Status != searchProjectionEvidenceMeasured || gen1Family <= 0 {
		t.Fatalf("gen1 family=%d evidence=%+v", gen1Family, ev)
	}
	gen1File := mustStoreFileSize(t, store)
	t.Logf("rebuild peak sample gen1_complete family=%d file=%d", gen1Family, gen1File)

	// Budget below the resident complete family, but above the non-recent
	// reserve, so Start does not empty the new recent tier.
	_, nonRecentScoped, nonRecentShared, splitEv := store.measureSearchProjectionFamilySplit(ctx, db)
	if splitEv.Status != searchProjectionEvidenceMeasured {
		t.Fatalf("split evidence=%+v", splitEv)
	}
	reserve := nonRecentScoped + nonRecentShared
	gen2Budget := gen1Family - 32<<10
	if gen2Budget <= reserve {
		gen2Budget = reserve + 32<<10
	}
	if gen2Budget >= gen1Family {
		t.Fatalf("could not pick a gen2 budget below gen1 family=%d reserve=%d", gen1Family, reserve)
	}
	b := capacityBudget(gen2Budget)
	if _, err := store.Start(ctx, b, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	afterStart, afterStartEv := store.measureSearchProjectionFamilyBytes(ctx, db)
	if afterStartEv.Status != searchProjectionEvidenceMeasured {
		t.Fatalf("after start evidence=%+v", afterStartEv)
	}
	if afterStart < gen1Family {
		t.Fatalf("family after Start %d < gen1 complete %d; old generation was reclaimed before the new one is searchable",
			afterStart, gen1Family)
	}

	var peakFamily, peakFile int64
	var samples int
	for i := 0; i < 200; i++ {
		family, evidence := store.measureSearchProjectionFamilyBytes(ctx, db)
		if evidence.Status == searchProjectionEvidenceMeasured && family > peakFamily {
			peakFamily = family
		}
		if n := mustStoreFileSize(t, store); n > peakFile {
			peakFile = n
		}
		samples++
		p, err := resumeProjection(ctx, store, b, now.Add(time.Hour))
		if err != nil {
			t.Fatalf("resume %d: %v", i, err)
		}
		if p.Completed {
			break
		}
		if i == 199 {
			t.Fatal("gen2 did not complete")
		}
	}
	gen2Family, ev2 := store.measureSearchProjectionFamilyBytes(ctx, db)
	if ev2.Status != searchProjectionEvidenceMeasured {
		t.Fatalf("gen2 family evidence=%+v", ev2)
	}
	gen2File := mustStoreFileSize(t, store)
	t.Logf("rebuild peak sample gen2_budget=%d after_start_family=%d peak_family=%d peak_file=%d gen2_complete family=%d file=%d samples=%d",
		gen2Budget, afterStart, peakFamily, peakFile, gen2Family, gen2File, samples)

	if peakFamily < gen1Family {
		t.Fatalf("rebuild peak family %d < gen1 complete %d; old generation disappeared during rebuild",
			peakFamily, gen1Family)
	}
	if peakFamily <= gen2Budget {
		t.Fatalf("rebuild peak family %d <= configured budget %d; peak was bounded by --index-family-bytes",
			peakFamily, gen2Budget)
	}
}

func mustStoreFileSize(t *testing.T, store *Database) int64 {
	t.Helper()
	info, err := os.Stat(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}
