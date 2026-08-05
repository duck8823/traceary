//go:build !darwin && !linux

package sqlite

func renameSegmentAtNoReplace(_ int, _, _ string) error { return ErrPreparedUpgradeUnsupported }
