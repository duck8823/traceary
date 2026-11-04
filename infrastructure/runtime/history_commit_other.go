//go:build !darwin && !linux

package runtime

func validHistoryHintRoot(string) bool { return false }
