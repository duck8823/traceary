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

// CallUserHomeDirFunc exposes the current user-home-directory lookup for
// tests. It always reads the active value so overrides installed after
// construction still apply.
func CallUserHomeDirFunc() (string, error) {
	return userHomeDirFunc()
}

// ResetUserHomeDirFunc restores the default home-directory lookup function for tests.
func ResetUserHomeDirFunc() {
	userHomeDirFunc = os.UserHomeDir
}

// SetAntigravityBundleExistsFunc replaces the Antigravity bundle existence
// probe for tests so the not_installed / installed capability path can be
// exercised deterministically regardless of the host machine.
func SetAntigravityBundleExistsFunc(f func(string) bool) {
	antigravityBundleExistsFunc = f
}

// ResetAntigravityBundleExistsFunc restores the default Antigravity bundle
// existence probe.
func ResetAntigravityBundleExistsFunc() {
	antigravityBundleExistsFunc = func(path string) bool {
		_, err := os.Stat(path)
		return err == nil
	}
}

// SetGCNowFunc replaces the current-time function for tests.
func SetGCNowFunc(f func() time.Time) {
	gcNowFunc = f
}

// ResetGCNowFunc restores the default current-time function for tests.
func ResetGCNowFunc() {
	gcNowFunc = time.Now
}

// SetTopNowFunc replaces the current-time function used by top snapshot and
// dashboard loading for tests.
func SetTopNowFunc(f func() time.Time) {
	topNowFunc = f
}

// ResetTopNowFunc restores the default top current-time function for tests.
func ResetTopNowFunc() {
	topNowFunc = time.Now
}

// SetDetectRepoContextFunc replaces the work-context resolver for tests.
func SetDetectRepoContextFunc(f func(context.Context) (string, error)) {
	detectRepoContextFunc = f
}

// ResetDetectRepoContextFunc restores the default work-context resolver for tests.
func ResetDetectRepoContextFunc() {
	detectRepoContextFunc = detectRepoContext
}
