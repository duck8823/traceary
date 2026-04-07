package cli

import "os"

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
