//go:build dogfood2323

package sqlite_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/duck8823/traceary/domain"
	sqliteschema "github.com/duck8823/traceary/schema/sqlite"
)

func TestOperatorCopyDogfood2323(t *testing.T) {
	target := os.Getenv("TRACEARY_DOGFOOD_2323")
	if target == "" {
		t.Fatal("TRACEARY_DOGFOOD_2323 is required")
	}
	// 2026-09-05 operator-copy run on the 12 GiB store
	// (/tmp/traceary-2323-dogfood/upgrade.log): TestOperatorCopyDogfood2323
	// failed at 3600.01s with
	// "digest source command_audits.command: scan command_audits.command:
	// context deadline exceeded". Decode+VACUUM finished in ~36m and the
	// journal reached scrub_in_progress; the 1h PublishLockLimit then fired
	// during the source-digest scan. RunUpgrade uses PublishLockLimit as the
	// exclusive-lease context for plan+prepare+verify+publish, so this is
	// the real wall budget. A single source lane scan needs >1h; four
	// decode lanes are digested on source (zstd) and candidate. 24h is the
	// test budget for this 12 GiB copy; product doctor budgets are unchanged.
	const dogfoodDeadline = 24 * time.Hour
	ctx, cancel := context.WithTimeout(context.Background(), dogfoodDeadline)
	defer cancel()
	all, err := sqliteschema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	size := uint64(info.Size())
	budget := domain.PreparedStoreUpgradeBudget{
		WallTimeLimit:      dogfoodDeadline,
		PublishLockLimit:   dogfoodDeadline,
		OwnedDiskByteLimit: size*8 + 1<<30,
		WALByteLimit:       size*2 + 1<<30,
		TemporaryByteLimit: size*4 + 1<<30,
		SafetyMarginBytes:  64 << 20,
	}
	started := time.Now()
	receipt := runUpgradeOnWithBudget(ctx, t, filepath.Dir(target), target, all, budget)
	fmt.Fprintf(os.Stderr, "dogfood elapsed=%s rollback=%s peak_owned=%d peak_wal=%d build_ms=%d events=%d audits=%d canonical=%s schema=%s\n",
		time.Since(started), receipt.RollbackPath, receipt.Evidence.PeakOwnedBytes, receipt.Evidence.PeakWALBytes, receipt.Evidence.BuildMilliseconds,
		receipt.Evidence.Canonical.EventCount, receipt.Evidence.Canonical.AuditCount, receipt.Evidence.Canonical.Digest, receipt.Evidence.SchemaDigest)
}
