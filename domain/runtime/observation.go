package runtime

// ObservationKind classifies untrusted telemetry, never native readiness or process exit.
type ObservationKind string

// Boundary kinds classify telemetry only; the selected envelope remains independently byte-bounded.
const (
	ObservationPromptBoundary   ObservationKind = "prompt_boundary"
	ObservationToolBoundary     ObservationKind = "tool_boundary"
	ObservationTurnBoundary     ObservationKind = "turn_boundary"
	ObservationSessionBoundary  ObservationKind = "session_boundary"
	MaxSelectedObservationBytes                 = 4096
)

// DerivedObservationState contains correlation metadata only, not native bodies or decisions.
type DerivedObservationState struct {
	Host, HostVersion, EventID, NativeSessionID, NativeTurnID string
	Kind                                                      ObservationKind
}

// DerivedObservation is a closed immutable untrusted sample. It grants no native authority.
type DerivedObservation struct{ state DerivedObservationState }

func observationCorrelation(value string, optional bool) bool {
	if value == "" {
		return optional
	}
	if len(value) > 256 {
		return false
	}
	for _, r := range value {
		valid := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.' || r == ':'
		if !valid {
			return false
		}
	}
	return true
}

// DerivedObservationOf validates bounded metadata without reading files or native state.
func DerivedObservationOf(s DerivedObservationState) (DerivedObservation, error) {
	switch s.Host {
	case "codex", "claude", "antigravity", "grok":
	default:
		return DerivedObservation{}, ErrInvalid
	}
	switch s.Kind {
	case ObservationPromptBoundary, ObservationToolBoundary, ObservationTurnBoundary, ObservationSessionBoundary:
	default:
		return DerivedObservation{}, ErrInvalid
	}
	if !printableLaunchValue(s.HostVersion, 256) || !observationCorrelation(s.EventID, false) || !observationCorrelation(s.NativeSessionID, true) || !observationCorrelation(s.NativeTurnID, true) {
		return DerivedObservation{}, ErrInvalid
	}
	return DerivedObservation{state: s}, nil
}

// Snapshot returns a value copy, not evidence of current interaction or permission.
func (o DerivedObservation) Snapshot() DerivedObservationState { return o.state }
