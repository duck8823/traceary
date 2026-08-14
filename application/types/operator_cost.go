package types

// OperatorCostReport is the operator's own measured store cost.
// SchemaVersion is traceary.operator_cost/v1.
type OperatorCostReport struct {
	SchemaVersion                       string               `json:"schema_version"`
	WindowDays                          int                  `json:"window_days"`
	ResidentBytes                       int64                `json:"resident_bytes"`
	EventCount                          int64                `json:"event_count"`
	SessionCount                        int64                `json:"session_count"`
	ResidentBytesPerEvent               float64              `json:"resident_bytes_per_event,omitempty"`
	ResidentBytesPerSession             float64              `json:"resident_bytes_per_session,omitempty"`
	RetainedSourceBytes                 int64                `json:"retained_source_bytes"`
	UndiscardableSourceBytes            int64                `json:"undiscardable_source_bytes"`
	FoldableSourceBytes                 int64                `json:"foldable_source_bytes"`
	UndiscardableBytesPerEvent          float64              `json:"undiscardable_bytes_per_event,omitempty"`
	UndiscardableBytesPerSession        float64              `json:"undiscardable_bytes_per_session,omitempty"`
	Amplification                       float64              `json:"amplification,omitempty"`
	WindowEventCount                    int64                `json:"window_event_count"`
	WindowSessionCount                  int64                `json:"window_session_count"`
	EventsPerDay                        float64              `json:"events_per_day,omitempty"`
	SessionsPerDay                      float64              `json:"sessions_per_day,omitempty"`
	ProjectedUndiscardableBytesPerMonth int64                `json:"projected_undiscardable_bytes_per_month,omitempty"`
	Evidence                            OperatorCostEvidence `json:"evidence"`
}

// OperatorCostEvidence states how complete the measurement is.
type OperatorCostEvidence struct {
	Status string `json:"status"`
	Method string `json:"method"`
	Reason string `json:"reason,omitempty"`
}
