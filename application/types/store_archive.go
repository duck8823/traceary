package types

import "time"

// StoreArchiveCreateParams configures archive create / archive-then-gc.
type StoreArchiveCreateParams struct {
	OutputPath        string
	Before            time.Time
	KeepDays          int
	Target            GarbageCollectionTarget
	DryRun            bool
	DeleteAfterVerify bool
	Passphrase        []byte
	ToolVersion       string
	SourceDBPath      string
}

// StoreArchiveTableCount is per-table row counts in a plan or package.
type StoreArchiveTableCount struct {
	Name     string
	RowCount int
}

// StoreArchiveResult is the outcome of create (and optional delete).
type StoreArchiveResult struct {
	DryRun            bool
	Path              string
	Tables            []StoreArchiveTableCount
	TotalRows         int
	DeletedCount      int
	Verified          bool
	DeletedAfterVerify bool
}

// StoreArchiveRestoreResult is the outcome of restore.
type StoreArchiveRestoreResult struct {
	DryRun       bool
	Inserted     int
	Skipped      int
	Conflicts    int
	TotalInArchive int
}
