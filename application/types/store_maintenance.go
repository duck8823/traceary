package types

// StoreMaintenancePendingError is returned when a read-write opener meets a
// live compact-pending marker. Hook writers must persist to the spool and
// return host success instead of waiting on the shared lease.
type StoreMaintenancePendingError struct {
	StorePath string
}

func (e *StoreMaintenancePendingError) Error() string {
	return "store upgrade in progress; hook write deferred to spool"
}
