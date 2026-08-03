//go:build !darwin && !linux

package sqlite

func atomicExchange(_, _ string) error {
	return ErrPreparedUpgradeUnsupported
}
func atomicExchangeSupported() bool     { return false }
func renameNoReplace(_, _ string) error { return ErrPreparedUpgradeUnsupported }
