package cli

import "os"

// ResolveDBPath はテスト用に resolveDBPath を公開します。
var ResolveDBPath = resolveDBPath

// SetUserConfigDirFunc はテスト用にユーザー設定ディレクトリ取得関数を差し替えます。
func SetUserConfigDirFunc(f func() (string, error)) {
	userConfigDirFunc = f
}

// ResetUserConfigDirFunc はテスト用にユーザー設定ディレクトリ取得関数を戻します。
func ResetUserConfigDirFunc() {
	userConfigDirFunc = os.UserConfigDir
}
