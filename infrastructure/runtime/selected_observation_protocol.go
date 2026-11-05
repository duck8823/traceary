package runtime

import (
	"bytes"
	"encoding/json"
	"io"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
)

const selectedObservationOperation = "observe_selected_v1"

type derivedObservationRecord struct {
	Host            string `json:"host"`
	HostVersion     string `json:"host_version"`
	EventID         string `json:"event_id"`
	Kind            string `json:"kind"`
	NativeSessionID string `json:"native_session_id,omitempty"`
	NativeTurnID    string `json:"native_turn_id,omitempty"`
}
type selectedObservationRecord struct {
	Selection *outputSelectionRecord    `json:"selection"`
	Metadata  *derivedObservationRecord `json:"metadata"`
}

// UnmarshalJSON bounds the actual nested bytes, including whitespace and escapes.
// The outer protocol decoder additionally rejects duplicate keys, null and invalid UTF-8.
func (r *selectedObservationRecord) UnmarshalJSON(data []byte) error {
	if len(data) > domain.MaxSelectedObservationBytes {
		return app.ErrRuntimeProtocol
	}
	fields, err := selectedObjectFields(data, []string{"selection", "metadata"}, nil)
	if err != nil {
		return err
	}
	if _, err = selectedObjectFields(fields["selection"], []string{"epoch", "run", "generation", "worktree", "profile"}, nil); err != nil {
		return err
	}
	if _, err = selectedObjectFields(fields["metadata"], []string{"host", "host_version", "event_id", "kind"}, []string{"native_session_id", "native_turn_id"}); err != nil {
		return err
	}
	type plain selectedObservationRecord
	var decoded plain
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&decoded) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return app.ErrRuntimeProtocol
	}
	*r = selectedObservationRecord(decoded)
	return nil
}
func recordSelectedObservation(o app.SelectedObservation) *selectedObservationRecord {
	selection := recordSelection(o.Selection)
	v := o.Metadata.Snapshot()
	return &selectedObservationRecord{Selection: &selection, Metadata: &derivedObservationRecord{Host: v.Host, HostVersion: v.HostVersion, EventID: v.EventID, Kind: string(v.Kind), NativeSessionID: v.NativeSessionID, NativeTurnID: v.NativeTurnID}}
}
func (r *selectedObservationRecord) observation() (app.SelectedObservation, error) {
	if r == nil || r.Selection == nil || r.Metadata == nil {
		return app.SelectedObservation{}, app.ErrRuntimeProtocol
	}
	selection, err := r.Selection.selection()
	if err != nil {
		return app.SelectedObservation{}, app.ErrRuntimeProtocol
	}
	v := r.Metadata
	metadata, err := domain.DerivedObservationOf(domain.DerivedObservationState{Host: v.Host, HostVersion: v.HostVersion, EventID: v.EventID, Kind: domain.ObservationKind(v.Kind), NativeSessionID: v.NativeSessionID, NativeTurnID: v.NativeTurnID})
	if err != nil {
		return app.SelectedObservation{}, app.ErrRuntimeProtocol
	}
	return app.SelectedObservation{Selection: selection, Metadata: metadata}, nil
}

// acknowledgmentOnly rejects every data/role arm, including future comparable fields.
// Adding a non-comparable response field must update this closed guard explicitly.
func (r runtimeResponse) acknowledgmentOnly() bool {
	return r == (runtimeResponse{Version: r.Version, ID: r.ID, Code: r.Code})
}

var _ app.SelectedObservationClient = (*LocalDaemon)(nil)

// selectedObjectFields enforces exact canonical names for the new closed variant.
// Go's case folding otherwise aliases some lowercase Unicode names to ASCII tags.
// This does not change the common outer decoder or persisted/legacy schemas.
func selectedObjectFields(data []byte, required, optional []string) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return nil, app.ErrRuntimeProtocol
	}
	allowed := make(map[string]struct{}, len(required)+len(optional))
	for _, name := range required {
		if _, ok := fields[name]; !ok {
			return nil, app.ErrRuntimeProtocol
		}
		allowed[name] = struct{}{}
	}
	for _, name := range optional {
		allowed[name] = struct{}{}
	}
	for name := range fields {
		if _, ok := allowed[name]; !ok {
			return nil, app.ErrRuntimeProtocol
		}
	}
	return fields, nil
}

// UnmarshalJSON preserves legacy decoding and closes the new selected request variant.
// Canonical-name checks are conditional on its decoded arm, avoiding additional
// raw-map copies of legacy manual terminal input. Outer version/null/duplicate
// validation remains in the unchanged common protocol decoder.
func (r *runtimeRequest) UnmarshalJSON(data []byte) error {
	type plain runtimeRequest
	var decoded plain
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&decoded) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return app.ErrRuntimeProtocol
	}
	if decoded.SelectedObservation != nil {
		if _, err := selectedObjectFields(data, []string{"protocol_version", "request_id", "operation", "selected_observation"}, nil); err != nil {
			return err
		}
	}
	if decoded.Resume != nil {
		if _, err := selectedObjectFields(data, []string{"protocol_version", "request_id", "operation", "resume"}, nil); err != nil {
			return err
		}
	}
	*r = runtimeRequest(decoded)
	return nil
}

// UnmarshalJSON preserves data/error responses and closes admission-only ACK headers.
// No newly accepted response, protocol downgrade or nullable field is introduced.
func (r *runtimeResponse) UnmarshalJSON(data []byte) error {
	type plain runtimeResponse
	var decoded plain
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&decoded) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return app.ErrRuntimeProtocol
	}
	value := runtimeResponse(decoded)
	if value.Code == "queued" && value.acknowledgmentOnly() {
		if _, err := selectedObjectFields(data, []string{"protocol_version", "request_id", "code"}, nil); err != nil {
			return err
		}
	}
	if decoded.Resume != nil {
		if _, err := selectedObjectFields(data, []string{"protocol_version", "request_id", "code", "resume"}, nil); err != nil {
			return err
		}
	}
	*r = value
	return nil
}
