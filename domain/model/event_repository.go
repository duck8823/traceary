package model

import "context"

// EventRepository はイベント永続化のインターフェースです。
type EventRepository interface {
	// Save はイベントを保存します。
	Save(ctx context.Context, event *Event) error
}
