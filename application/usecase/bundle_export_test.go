package usecase

import (
	"encoding/json"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
)

// BundleSessionRoundTripForTest exposes the observable codec round-trip
// without making the private NDJSON row part of the production API.
func BundleSessionRoundTripForTest(session *model.Session) (*model.Session, string, string, error) {
	encoded, err := encodeSessionsNDJSON([]*model.Session{session})
	if err != nil {
		return nil, "", "", err
	}
	var row bundleSessionRow
	if err := json.Unmarshal(encoded.Bytes(), &row); err != nil {
		return nil, "", "", xerrors.Errorf("decode bundle session: %w", err)
	}
	restored, err := row.toSession()
	return restored, row.RuntimeMode, row.TerminalReason, err
}

// LegacyBundleSessionFromJSONForTest exposes compatibility restoration while
// keeping the private bundle row type out of external tests.
func LegacyBundleSessionFromJSONForTest(raw []byte) (*model.Session, error) {
	var row bundleSessionRow
	if err := json.Unmarshal(raw, &row); err != nil {
		return nil, xerrors.Errorf("decode legacy bundle session: %w", err)
	}
	return row.toSession()
}
