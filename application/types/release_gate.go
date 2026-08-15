package types

// ReleaseGateReport is the #1873 evaluation of rows still called a gate.
// SchemaVersion is traceary.release_gate/v1.
type ReleaseGateReport struct {
	SchemaVersion string                   `json:"schema_version"`
	Passed        bool                     `json:"passed"`
	StorePath     string                   `json:"store_path,omitempty"`
	Gates         []ReleaseGateResult      `json:"gates"`
	Measurements  []ReleaseGateMeasurement `json:"measurements"`
	Corpus        string                   `json:"corpus"`
}

// ReleaseGateResult is one evaluated gate. Status is pass, miss, or skip.
type ReleaseGateResult struct {
	ID        string  `json:"id"`
	Kind      string  `json:"kind"`
	Status    string  `json:"status"`
	Observed  float64 `json:"observed,omitempty"`
	Threshold float64 `json:"threshold,omitempty"`
	Unit      string  `json:"unit,omitempty"`
	Reason    string  `json:"reason,omitempty"`
}

// ReleaseGateMeasurement is a #1620 absolute byte count. It is published with
// its corpus and never fails a release.
type ReleaseGateMeasurement struct {
	ID                   string  `json:"id"`
	Unit                 string  `json:"unit"`
	ObservedBytesPerUnit float64 `json:"observed_bytes_per_unit,omitempty"`
	PublishedBound       string  `json:"published_bound"`
	Corpus               string  `json:"corpus"`
}
