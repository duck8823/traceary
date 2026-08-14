package types

// FoldGateReport is the #1879 measurement of the two v0.34-unmeasurable rows.
// SchemaVersion is traceary.fold_gate/v1.
type FoldGateReport struct {
	SchemaVersion        string             `json:"schema_version"`
	ThresholdBytes       int64              `json:"threshold_bytes"`
	WakeBudgetBytes      int64              `json:"wake_budget_bytes"`
	SessionCount         int64              `json:"session_count"`
	WorthFoldingCount    int64              `json:"worth_folding_count"`
	RefinementCount      int64              `json:"refinement_count"`
	RefinementRatio      float64            `json:"refinement_ratio,omitempty"`
	RefinementGate       string             `json:"refinement_gate"`
	AgentRefinementCount int64              `json:"agent_refinement_count"`
	Wake                 []FoldGateWakeHost `json:"wake"`
	WakeGate             string             `json:"wake_gate"`
	Content              FoldGateContent    `json:"content"`
	WorthFoldingRule     string             `json:"worth_folding_rule"`
	Evidence             FoldGateEvidence   `json:"evidence"`
}

// FoldGateWakeHost is wake eligibility for one sessions.client.
type FoldGateWakeHost struct {
	Client            string `json:"client"`
	EligibleCount     int64  `json:"eligible_count"`
	FitsBudgetCount   int64  `json:"fits_budget_count"`
	InjectionPossible bool   `json:"injection_possible"`
	Status            string `json:"status"`
}

// FoldGateContent is a structural sample against the #1874 ask.
// It does not claim to parse motivation/change semantically.
type FoldGateContent struct {
	Sampled            int64 `json:"sampled"`
	Nonempty           int64 `json:"nonempty"`
	MechanicalTemplate int64 `json:"mechanical_template"`
	ContentProxyOK     int64 `json:"content_proxy_ok"`
	SampleLimit        int   `json:"sample_limit"`
}

// FoldGateEvidence states how complete the measurement is.
type FoldGateEvidence struct {
	Status string `json:"status"`
	Method string `json:"method"`
	Reason string `json:"reason,omitempty"`
}
