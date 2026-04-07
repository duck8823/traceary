package model

import "time"

// SetNowFunc はテスト用に現在時刻関数を差し替えます。
func SetNowFunc(f func() time.Time) {
	nowFunc = f
}

// ResetNowFunc はテスト用に現在時刻関数をデフォルトへ戻します。
func ResetNowFunc() {
	nowFunc = time.Now
}
