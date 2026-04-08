package model

import "time"

// SetNowFunc replaces the current-time function for tests.
func SetNowFunc(f func() time.Time) {
	nowFunc = f
}

// ResetNowFunc restores the default current-time function for tests.
func ResetNowFunc() {
	nowFunc = time.Now
}
