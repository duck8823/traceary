package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/presentation"
)

const compactReclaimWarnInterval = 24 * time.Hour

func (c *RootCLI) attachCompactReclaimWarning(root *cobra.Command) {
	root.PersistentPostRun = func(cmd *cobra.Command, _ []string) {
		c.emitCompactReclaimWarning(cmd)
	}
}

func (c *RootCLI) emitCompactReclaimWarning(cmd *cobra.Command) {
	if shouldSkipCompactReclaimWarning(cmd) {
		return
	}
	cfg := presentation.LoadConfig()
	floor := cfg.Compact.ReclaimWarnBytes
	if floor <= 0 {
		return
	}
	path, err := resolveDBPath(lookupDBPathFlag(cmd))
	if err != nil || path == "" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	// A file smaller than the floor cannot hold `floor` reclaimable bytes, so
	// the cheap stat still short-circuits before any store open. It is no
	// longer sufficient on its own: free pages, not file size, decide.
	if info.Size() < floor {
		return
	}
	now := compactReclaimNow()
	if !shouldEmitCompactReclaimWarning(path, now) {
		return
	}
	meta, err := c.inspectReclaimWarningPageMetadata(cmd, path)
	if err != nil {
		// Unknown is not a reason to interrupt: doctor is where uncertainty is
		// reported, this trailer is uninvited output on unrelated commands.
		return
	}
	if !reclaimableWarrantsCompact(meta.ReclaimableBytes, maxInt64(info.Size(), meta.DatabaseBytes), floor) {
		return
	}
	message := compactReclaimWarningMessage(path, info.Size(), meta.ReclaimableBytes)
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s\n", message)
	_ = recordCompactReclaimWarning(path, now)
}

func (c *RootCLI) inspectReclaimWarningPageMetadata(cmd *cobra.Command, path string) (apptypes.StorePageMetadata, error) {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	return c.inspectLargeStorePageMetadata(ctx, path)
}

func compactReclaimWarningMessage(storePath string, storeSize, reclaimableBytes int64) string {
	size := formatByteSize(reclaimableBytes)
	if storeSize > 0 {
		if free, err := volumeAvailableBytes(filepath.Dir(storePath)); err == nil && free < uint64(storeSize) {
			return localizef(
				"TRACEARY: store can reclaim about %s, but this volume does not have enough free bytes for a compact replica; attach another disk before running `traceary store compact`",
				"TRACEARY: ストアに回収できる領域が約 %s ありますが、このボリュームには compact レプリカ分の空きがありません。別ディスクを接続してから `traceary store compact` を実行してください",
				size,
			)
		}
	}
	return localizef(
		"TRACEARY: store can reclaim about %s; run `traceary store compact`",
		"TRACEARY: ストアに回収できる領域が約 %s あります。`traceary store compact` を実行してください",
		size,
	)
}

func shouldEmitCompactReclaimWarning(storePath string, now time.Time) bool {
	raw, err := os.ReadFile(compactReclaimWarnStatePath(storePath))
	if err != nil {
		return true
	}
	last, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(raw)))
	if err != nil {
		return true
	}
	return !last.After(now) && now.Sub(last) >= compactReclaimWarnInterval
}

func recordCompactReclaimWarning(storePath string, now time.Time) error {
	path := compactReclaimWarnStatePath(storePath)
	if err := os.WriteFile(path, []byte(now.UTC().Format(time.RFC3339Nano)+"\n"), 0o600); err != nil {
		return fmt.Errorf("write compact reclaim warn state: %w", err)
	}
	return nil
}

func compactReclaimWarnStatePath(storePath string) string {
	return storePath + ".reclaim-warn"
}

func shouldSkipCompactReclaimWarning(cmd *cobra.Command) bool {
	for current := cmd; current != nil; current = current.Parent() {
		if current.Hidden {
			return true
		}
		switch current.Name() {
		case "hook", "compact", "doctor":
			return true
		}
	}
	return false
}

func lookupDBPathFlag(cmd *cobra.Command) string {
	for current := cmd; current != nil; current = current.Parent() {
		if flag := current.Flags().Lookup("db-path"); flag != nil && flag.Changed {
			return flag.Value.String()
		}
	}
	return ""
}

var compactReclaimNow = time.Now
