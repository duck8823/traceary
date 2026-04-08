package cli

import (
	"context"
	"os"
	"time"
)

// ResolveDBPath exposes resolveDBPath for tests.
var ResolveDBPath = resolveDBPath

// SetUserHomeDirFunc replaces the home-directory lookup function for tests.
func SetUserHomeDirFunc(f func() (string, error)) {
	userHomeDirFunc = f
}

// ResetUserHomeDirFunc restores the default home-directory lookup function for tests.
func ResetUserHomeDirFunc() {
	userHomeDirFunc = os.UserHomeDir
}

// SetGCNowFunc replaces the current-time function for tests.
func SetGCNowFunc(f func() time.Time) {
	gcNowFunc = f
}

// ResetGCNowFunc restores the default current-time function for tests.
func ResetGCNowFunc() {
	gcNowFunc = time.Now
}

// SetDetectRepoContextFunc replaces the work-context resolver for tests.
func SetDetectRepoContextFunc(f func(context.Context) (string, error)) {
	detectRepoContextFunc = f
}

// ResetDetectRepoContextFunc restores the default work-context resolver for tests.
func ResetDetectRepoContextFunc() {
	detectRepoContextFunc = detectRepoContext
}
