package sqlite

import (
	"os"
	"path/filepath"
	"strings"
)

// planAvailableBytes is the free-space consult used by Plan/Recheck. Tests
// replace it to simulate a full volume before copy starts.
var planAvailableBytes = availableBytes

// inspectSpoolReserveBytes lets tests inject a spool-reserve size.
var inspectSpoolReserveBytes func(storePath string) uint64

const spoolReserveFloorBytes = 64 << 20

func spoolReserveBytes(storePath string) uint64 {
	if inspectSpoolReserveBytes != nil {
		return inspectSpoolReserveBytes(storePath)
	}
	dir := hookSpoolDirForStore()
	size := directorySize(dir)
	if size < spoolReserveFloorBytes {
		return spoolReserveFloorBytes
	}
	return size
}

func hookSpoolDirForStore() string {
	if env := strings.TrimSpace(os.Getenv("TRACEARY_HOOK_STATE_DIR")); env != "" {
		return filepath.Join(env, "spool")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "traceary", "hooks", "spool")
}

func directorySize(dir string) uint64 {
	if dir == "" {
		return 0
	}
	var total uint64
	_ = filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		if info.Size() > 0 {
			total += uint64(info.Size())
		}
		return nil
	})
	return total
}
