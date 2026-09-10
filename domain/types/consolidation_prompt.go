package types

import (
	"strings"

	"golang.org/x/xerrors"
)

// ConsolidationPromptRequestID is the stable durable identity of one prompt handoff.
type ConsolidationPromptRequestID string

func ConsolidationPromptRequestIDFrom(value string) (ConsolidationPromptRequestID, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", xerrors.New("consolidation prompt request id must not be empty")
	}
	return ConsolidationPromptRequestID(value), nil
}
func (id ConsolidationPromptRequestID) String() string { return string(id) }

// ConsolidationPromptClaimToken identifies one short-lived delivery lease.
type ConsolidationPromptClaimToken string

func ConsolidationPromptClaimTokenFrom(value string) (ConsolidationPromptClaimToken, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", xerrors.New("consolidation prompt claim token must not be empty")
	}
	return ConsolidationPromptClaimToken(value), nil
}
func (token ConsolidationPromptClaimToken) String() string { return string(token) }
