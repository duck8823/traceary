package cli

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

const (
	hookMemoryDecayEnvKey      = "TRACEARY_MEMORY_DECAY"
	hookMemoryDecayAfterEnvKey = "TRACEARY_MEMORY_DECAY_AFTER"
	hookMemoryDecayLimit       = 500
)

// runHookMemoryDecayBestEffort applies non-destructive candidate decay after
// primary session boundary work. Failures are logged and never fail the hook.
func (c *RootCLI) runHookMemoryDecayBestEffort(ctx context.Context, dbPath string) {
	if c.memory == nil || c.storeManagement == nil {
		return
	}
	if !hookMemoryDecayEnabled() {
		return
	}
	olderThan := hookMemoryDecayOlderThan()
	// Re-apply DB path and initialize in case extract worker changed nothing.
	c.applyDatabasePath(dbPath)
	if err := c.storeManagement.Initialize(ctx); err != nil {
		slog.Debug("hook memory decay initialize failed", "error", err)
		return
	}
	result, err := c.memory.Decay(ctx, apptypes.MemoryDecayCriteria{
		OlderThan: olderThan,
		Limit:     hookMemoryDecayLimit,
		Apply:     true,
		Dedupe:    true,
	})
	if err != nil {
		slog.Debug("hook memory decay failed", "error", err)
		return
	}
	if len(result.ExpiredIDs) > 0 || len(result.SupersededIDs) > 0 {
		slog.Debug("hook memory decay applied",
			"expired", len(result.ExpiredIDs),
			"superseded", len(result.SupersededIDs),
			"remaining", result.RemainingAfter,
		)
	}
}

func hookMemoryDecayEnabled() bool {
	raw, ok := os.LookupEnv(hookMemoryDecayEnvKey)
	if !ok {
		return true // default ON
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "0", "false", "off", "no", "disabled":
		return false
	default:
		return true
	}
}

func hookMemoryDecayOlderThan() time.Duration {
	raw := strings.TrimSpace(os.Getenv(hookMemoryDecayAfterEnvKey))
	if raw == "" {
		return domtypes.DefaultMemoryDecayOlderThan
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return domtypes.DefaultMemoryDecayOlderThan
	}
	return d
}
