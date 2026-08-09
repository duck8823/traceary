package types

// LegacySearchRetireReport is the operator-facing result of
// `traceary store search-retire`. DROP returns pages to the free list; the
// file does not shrink until `store compact plan/apply`.
type LegacySearchRetireReport struct {
	AlreadyRemoved                bool  `json:"already_removed"`
	LogicalBytesBefore            int64 `json:"logical_bytes_before"`
	LogicalBytesAfter             int64 `json:"logical_bytes_after"`
	PhysicalBytesBefore           int64 `json:"physical_bytes_before"`
	PhysicalBytesAfter            int64 `json:"physical_bytes_after"`
	FileSizeUnchangedUntilCompact bool  `json:"file_size_unchanged_until_compact"`
}
