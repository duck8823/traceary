//go:build !darwin && !linux

package sqlite

func describeStoreLeaseHolders(string) string { return "" }
