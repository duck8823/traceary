package model

import (
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/types"
)

var nowFunc = time.Now

// Event は traceary に記録される最小単位の履歴を表すエンティティです。
type Event struct {
	eventID   types.EventID
	kind      types.EventKind
	agent     types.Agent
	sessionID types.SessionID
	body      string
	createdAt time.Time
}

// NewEvent は新しい Event を生成します。
func NewEvent(
	eventID types.EventID,
	kind types.EventKind,
	agent types.Agent,
	sessionID types.SessionID,
	body string,
) (*Event, error) {
	trimmedBody := strings.TrimSpace(body)
	if trimmedBody == "" {
		return nil, xerrors.Errorf("イベント本文は空にできません")
	}
	return &Event{
		eventID:   eventID,
		kind:      kind,
		agent:     agent,
		sessionID: sessionID,
		body:      trimmedBody,
		createdAt: nowFunc(),
	}, nil
}

// EventOf は復元用に Event を生成します。
func EventOf(
	eventID types.EventID,
	kind types.EventKind,
	agent types.Agent,
	sessionID types.SessionID,
	body string,
	createdAt time.Time,
) *Event {
	return &Event{
		eventID:   eventID,
		kind:      kind,
		agent:     agent,
		sessionID: sessionID,
		body:      body,
		createdAt: createdAt,
	}
}

// EventID はイベント ID を返します。
func (e *Event) EventID() types.EventID { return e.eventID }

// Kind はイベント種別を返します。
func (e *Event) Kind() types.EventKind { return e.kind }

// Agent はイベントを発生させた主体を返します。
func (e *Event) Agent() types.Agent { return e.agent }

// SessionID はイベントが属するセッション ID を返します。
func (e *Event) SessionID() types.SessionID { return e.sessionID }

// Body はイベント本文を返します。
func (e *Event) Body() string { return e.body }

// CreatedAt はイベント作成時刻を返します。
func (e *Event) CreatedAt() time.Time { return e.createdAt }
