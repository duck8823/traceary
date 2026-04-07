package types

import (
	"strings"

	"golang.org/x/xerrors"
)

// Agent はイベントを発生させた主体を表す値オブジェクトです。
type Agent string

// AgentOf は文字列から Agent を生成します。
func AgentOf(value string) (Agent, error) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return Agent(""), xerrors.Errorf("agent は空にできません")
	}
	return Agent(trimmedValue), nil
}

// String は Agent を文字列化します。
func (a Agent) String() string { return string(a) }
