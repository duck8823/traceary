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
	ctx := context.Background()
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
		WallTimeLimit:      6 * time.Hour,
		PublishLockLimit:   time.Hour,
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
