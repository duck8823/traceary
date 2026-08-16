package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/duck8823/traceary/presentation"
)

const compactReclaimWarnInterval = 24 * time.Hour

func attachCompactReclaimWarning(root *cobra.Command) {
	root.PersistentPostRun = func(cmd *cobra.Command, _ []string) {
		emitCompactReclaimWarning(cmd)
	}
}

func emitCompactReclaimWarning(cmd *cobra.Command) {
	if shouldSkipCompactReclaimWarning(cmd) {
		return
	}
	cfg := presentation.LoadConfig()
	if cfg.Compact.ReclaimWarnBytes <= 0 {
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
	if info.Size() < cfg.Compact.ReclaimWarnBytes {
		return
	}
	now := compactReclaimNow()
	if !shouldEmitCompactReclaimWarning(path, now) {
		return
	}
	message := compactReclaimWarningMessage(path, info.Size())
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s\n", message)
	_ = recordCompactReclaimWarning(path, now)
}

func compactReclaimWarningMessage(storePath string, storeSize int64) string {
	if storeSize > 0 {
		if free, err := volumeAvailableBytes(filepath.Dir(storePath)); err == nil && free < uint64(storeSize) {
			return Localize(
				"TRACEARY: store can reclaim space, but this volume does not have enough free bytes for a compact replica; attach another disk before running `traceary store compact`",
				"TRACEARY: ストアに回収できる領域がありますが、このボリュームには compact レプリカ分の空きがありません。別ディスクを接続してから `traceary store compact` を実行してください",
			)
		}
	}
	return Localize(
		"TRACEARY: store can reclaim space; run `traceary store compact`",
		"TRACEARY: ストアに回収できる領域があります。`traceary store compact` を実行してください",
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
