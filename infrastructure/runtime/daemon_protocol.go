package runtime

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"unicode/utf8"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
)

type runtimeRequest struct {
	Attach      *outputAttachRecord   `json:"attach,omitempty"`
	Version     int                   `json:"protocol_version"`
	ID          string                `json:"request_id"`
	Operation   string                `json:"operation"`
	Role        string                `json:"role,omitempty"`
	Observation *observationRecord    `json:"observation,omitempty"`
	Launch      *launchRequestRecord  `json:"launch,omitempty"`
	Page        *runPageRequestRecord `json:"page,omitempty"`
}
type observationRecord struct {
	Host            string `json:"host"`
	HostVersion     string `json:"host_version"`
	RunID           string `json:"run_id"`
	Generation      string `json:"generation"`
	Epoch           string `json:"epoch"`
	TargetID        string `json:"target_id"`
	EventID         string `json:"event_id"`
	NativeSessionID string `json:"native_session_id"`
	NativeTurnID    string `json:"native_turn_id,omitempty"`
	Kind            string `json:"kind"`
}
type runtimeResponse struct {
	Output  *outputRecord        `json:"output,omitempty"`
	Version int                  `json:"protocol_version"`
	ID      string               `json:"request_id"`
	Code    string               `json:"code"`
	Role    string               `json:"role,omitempty"`
	Status  *runtimeStatusRecord `json:"status,omitempty"`
	Run     *launchRecord        `json:"run,omitempty"`
	Page    *runPageRecord       `json:"page,omitempty"`
}
type launchRequestRecord struct {
	Epoch     string `json:"epoch"`
	Worktree  string `json:"worktree"`
	Profile   string `json:"profile"`
	RequestID string `json:"request_id"`
}
type runPageRequestRecord struct {
	Epoch    string `json:"epoch"`
	Cursor   string `json:"cursor,omitempty"`
	Revision uint64 `json:"revision"`
}
type runPageRecord struct {
	Epoch    string         `json:"epoch"`
	Revision uint64         `json:"revision"`
	Runs     []launchRecord `json:"runs"`
	Next     string         `json:"next,omitempty"`
}
type runtimeStatusRecord struct {
	Version             int      `json:"protocol_version"`
	Epoch               string   `json:"epoch"`
	State               string   `json:"state"`
	Capabilities        []string `json:"capabilities"`
	ObservationDelivery string   `json:"observation_delivery"`
}

func readRuntimeFrame(r io.Reader) ([]byte, error) {
	var prefix [4]byte
	if _, err := io.ReadFull(r, prefix[:]); err != nil {
		return nil, app.ErrRuntimeProtocol
	}
	n := binary.BigEndian.Uint32(prefix[:])
	if n == 0 || n > MaxRuntimeFrameBytes {
		return nil, app.ErrRuntimeProtocol
	}
	data := make([]byte, int(n))
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, app.ErrRuntimeProtocol
	}
	return data, nil
}
func writeRuntimeFrame(w io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil || len(data) == 0 || len(data) > MaxRuntimeFrameBytes {
		return app.ErrRuntimeProtocol
	}
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(data)))
	for _, part := range [][]byte{prefix[:], data} {
		for len(part) > 0 {
			n, err := w.Write(part)
			if err != nil || n <= 0 {
				return app.ErrDaemonUnavailable
			}
			part = part[n:]
		}
	}
	return nil
}
func decodeRuntimeJSON(data []byte, value any) error {
	if !utf8.Valid(data) {
		return app.ErrRuntimeProtocol
	}
	strict := json.NewDecoder(bytes.NewReader(data))
	if err := uniqueJSONValue(strict, 0); err != nil {
		return app.ErrRuntimeProtocol
	}
	var header struct {
		Version *int `json:"protocol_version"`
	}
	if err := json.Unmarshal(data, &header); err != nil || header.Version == nil {
		return app.ErrRuntimeProtocol
	}
	if *header.Version != RuntimeProtocolVersion {
		return app.ErrRuntimeUnsupported
	}
	// V1 has optional omitted fields, not nullable values. JSON null must not
	// silently become a zero value that bypasses role/type checks.
	tokens := json.NewDecoder(bytes.NewReader(data))
	for {
		token, err := tokens.Token()
		if err == io.EOF {
			break
		}
		if err != nil || token == nil {
			return app.ErrRuntimeProtocol
		}
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return app.ErrRuntimeProtocol
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return app.ErrRuntimeProtocol
	}
	return nil
}
func boundedRuntimeID(value string, optional bool) bool {
	if len(value) == 0 {
		return optional
	}
	if len(value) > 256 {
		return false
	}
	for _, r := range value {
		if r < 33 || r > 126 {
			return false
		}
	}
	return true
}
func validObservation(v app.ObservationMetadata) bool {
	for _, value := range []string{v.Host, v.HostVersion, v.RunID, v.Generation, v.Epoch, v.TargetID, v.EventID, v.NativeSessionID, v.Kind} {
		if !boundedRuntimeID(value, false) {
			return false
		}
	}
	return boundedRuntimeID(v.NativeTurnID, true)
}
func recordObservation(v app.ObservationMetadata) *observationRecord {
	return &observationRecord{v.Host, v.HostVersion, v.RunID, v.Generation, v.Epoch, v.TargetID, v.EventID, v.NativeSessionID, v.NativeTurnID, v.Kind}
}
func (v observationRecord) metadata() app.ObservationMetadata {
	return app.ObservationMetadata{Host: v.Host, HostVersion: v.HostVersion, RunID: v.RunID, Generation: v.Generation, Epoch: v.Epoch, TargetID: v.TargetID, EventID: v.EventID, NativeSessionID: v.NativeSessionID, NativeTurnID: v.NativeTurnID, Kind: v.Kind}
}
func responseError(code string) error {
	switch code {
	case "output_cursor":
		return domain.ErrOutputCursor
	case "launch_unknown":
		return app.ErrLaunchUnknown
	case "changed":
		return domain.ErrChanged
	case "limit":
		return domain.ErrLimit
	case "not_found":
		return domain.ErrNotFound
	case "rejected":
		return app.ErrRuntimeRejected
	case "unsupported":
		return app.ErrRuntimeUnsupported
	case "busy":
		return app.ErrRuntimeBusy
	case "unsafe":
		return app.ErrUnsafeRuntime
	case "invalid":
		return app.ErrRuntimeProtocol
	default:
		return app.ErrDaemonUnavailable
	}
}
