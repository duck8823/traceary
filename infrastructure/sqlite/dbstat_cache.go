package sqlite

import (
	"encoding/json"
	"os"
	"strings"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
)

const dbstatInspectBudget = 3 * time.Second

type dbstatCacheRecord struct {
	Size          int64                     `json:"size"`
	MtimeUnixNano int64                     `json:"mtime_unix_nano"`
	GeneratedAt   string                    `json:"generated_at"`
	Objects       []apptypes.CapacityObject `json:"objects"`
}

func dbstatCachePath(dbPath string) string {
	return dbPath + ".dbstat-cache.json"
}

func loadMatchingDBStatCache(dbPath string) (dbstatCacheRecord, bool) {
	info, err := os.Stat(dbPath)
	if err != nil {
		return dbstatCacheRecord{}, false
	}
	raw, err := os.ReadFile(dbstatCachePath(dbPath))
	if err != nil {
		return dbstatCacheRecord{}, false
	}
	var record dbstatCacheRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return dbstatCacheRecord{}, false
	}
	if record.Size != info.Size() || record.MtimeUnixNano != info.ModTime().UnixNano() {
		return dbstatCacheRecord{}, false
	}
	return record, true
}

func storeDBStatCache(dbPath string, objects []apptypes.CapacityObject) {
	info, err := os.Stat(dbPath)
	if err != nil {
		return
	}
	record := dbstatCacheRecord{
		Size:          info.Size(),
		MtimeUnixNano: info.ModTime().UnixNano(),
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339Nano),
		Objects:       objects,
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return
	}
	_ = os.WriteFile(dbstatCachePath(dbPath), raw, 0o600)
}

func searchProjectionFamilyBytesFromObjects(objects []apptypes.CapacityObject) int64 {
	var total int64
	for _, object := range objects {
		if searchProjectionFamilyObjectName(object.Name) {
			total += object.Bytes
		}
	}
	return total
}

// searchProjectionFamilyObjectName mirrors select_search_projection_family_total.sql
// membership (name or tbl_name GLOB search_projection_* / literal_search_*).
// Capacity cache rows have no tbl_name, so SQLite index/autoindex names that
// belong to those tables must be included by their own name.
func searchProjectionFamilyObjectName(name string) bool {
	return searchProjectionFamilyNameOrIndex(name, "search_projection_") ||
		searchProjectionFamilyNameOrIndex(name, "literal_search_")
}

func searchProjectionFamilyNameOrIndex(name, prefix string) bool {
	return strings.HasPrefix(name, prefix) ||
		strings.HasPrefix(name, "idx_"+prefix) ||
		strings.HasPrefix(name, "sqlite_autoindex_"+prefix)
}
