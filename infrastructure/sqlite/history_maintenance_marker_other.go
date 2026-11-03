//go:build !darwin && !linux

package sqlite

func consultHistoryMaintenanceMarker(string) error { return ErrPreparedUpgradeUnsupported }
