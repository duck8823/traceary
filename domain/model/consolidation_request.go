package model

import (
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/types"
)

// ErrInvalidConsolidationRequest is returned when a request fact is incomplete.
var ErrInvalidConsolidationRequest = xerrors.New("invalid consolidation request")

// ConsolidationRequest is one metadata-only fold-request fact.
type ConsolidationRequest struct {
	sessionID      types.SessionID
	client         string
	requestedAt    time.Time
	atEventID      types.EventID
	signal         string
	pressureValue  int64
	thresholdValue int64
	reRequest      bool
	delivery       types.ConsolidationDelivery
}

// NewConsolidationRequest builds an unsaved request fact.
func NewConsolidationRequest(
	sessionID types.SessionID,
	client string,
	requestedAt time.Time,
	atEventID types.EventID,
	signal string,
	pressureValue, thresholdValue int64,
	reRequest bool,
	delivery types.ConsolidationDelivery,
) (*ConsolidationRequest, error) {
	request := &ConsolidationRequest{
		sessionID:      sessionID,
		client:         strings.TrimSpace(client),
		requestedAt:    requestedAt.UTC(),
		atEventID:      atEventID,
		signal:         strings.TrimSpace(signal),
		pressureValue:  pressureValue,
		thresholdValue: thresholdValue,
		reRequest:      reRequest,
		delivery:       delivery,
	}
	if err := request.validate(); err != nil {
		return nil, err
	}
	return request, nil
}

func (r *ConsolidationRequest) validate() error {
	if strings.TrimSpace(r.sessionID.String()) == "" {
		return xerrors.Errorf("session id must not be empty: %w", ErrInvalidConsolidationRequest)
	}
	if r.client == "" {
		return xerrors.Errorf("client must not be empty: %w", ErrInvalidConsolidationRequest)
	}
	if strings.TrimSpace(r.atEventID.String()) == "" {
		return xerrors.Errorf("at event id must not be empty: %w", ErrInvalidConsolidationRequest)
	}
	if r.signal == "" {
		return xerrors.Errorf("signal must not be empty: %w", ErrInvalidConsolidationRequest)
	}
	if r.thresholdValue <= 0 {
		return xerrors.Errorf("threshold must be positive: %w", ErrInvalidConsolidationRequest)
	}
	if r.pressureValue < 0 {
		return xerrors.Errorf("pressure must not be negative: %w", ErrInvalidConsolidationRequest)
	}
	if _, err := types.ConsolidationDeliveryFrom(r.delivery.String()); err != nil {
		return xerrors.Errorf("delivery: %w", err)
	}
	return nil
}

// SessionID returns the asked session.
func (r *ConsolidationRequest) SessionID() types.SessionID { return r.sessionID }

// Client returns the host that was asked.
func (r *ConsolidationRequest) Client() string { return r.client }

// RequestedAt returns when the request was emitted.
func (r *ConsolidationRequest) RequestedAt() time.Time { return r.requestedAt }

// AtEventID returns the session's latest event when asked.
func (r *ConsolidationRequest) AtEventID() types.EventID { return r.atEventID }

// Signal returns the pressure signal name.
func (r *ConsolidationRequest) Signal() string { return r.signal }

// PressureValue returns the measured pressure.
func (r *ConsolidationRequest) PressureValue() int64 { return r.pressureValue }

// ThresholdValue returns the configured threshold.
func (r *ConsolidationRequest) ThresholdValue() int64 { return r.thresholdValue }

// ReRequest reports whether an earlier open request existed.
func (r *ConsolidationRequest) ReRequest() bool { return r.reRequest }

// Delivery returns the delivery path.
func (r *ConsolidationRequest) Delivery() types.ConsolidationDelivery { return r.delivery }
