package cli

import (
	"context"
	"os"
	"time"
)

// ResolveDBPath はテスト用に resolveDBPath を公開します。
var ResolveDBPath = resolveDBPath

// SetUserHomeDirFunc はテスト用にユーザーホームディレクトリ取得関数を差し替えます。
func SetUserHomeDirFunc(f func() (string, error)) {
	userHomeDirFunc = f
}

// ResetUserHomeDirFunc はテスト用にユーザーホームディレクトリ取得関数を戻します。
func ResetUserHomeDirFunc() {
	userHomeDirFunc = os.UserHomeDir
}

// SetGCNowFunc はテスト用に現在時刻関数を差し替えます。
func SetGCNowFunc(f func() time.Time) {
	gcNowFunc = f
}

// ResetGCNowFunc はテスト用に現在時刻関数を戻します。
func ResetGCNowFunc() {
	gcNowFunc = time.Now
}

// SetDetectRepoContextFunc はテスト用に work context 解決関数を差し替えます。
func SetDetectRepoContextFunc(f func(context.Context) (string, error)) {
	detectRepoContextFunc = f
}

// ResetDetectRepoContextFunc はテスト用に work context 解決関数を戻します。
func ResetDetectRepoContextFunc() {
	detectRepoContextFunc = detectRepoContext
}
