package types

import "time"

// WorkspaceIdentityCoverage reports current-event primary-observation coverage.
type WorkspaceIdentityCoverage struct {
	EventCount       int     `json:"event_count"`
	CoveredEvents    int     `json:"covered_events"`
	MissingEvents    int     `json:"missing_events"`
	CoverageRate     float64 `json:"coverage_rate"`
	ObservationCount int     `json:"observation_count"`
}

// WorkspaceRelationshipCounts is a fixed projection of current relationship facts.
type WorkspaceRelationshipCounts struct {
	Exact         int `json:"exact"`
	Descendant    int `json:"descendant"`
	Ancestor      int `json:"ancestor"`
	ExplicitAlias int `json:"explicit_alias"`
	Conflict      int `json:"conflict"`
	Unknown       int `json:"unknown"`
}

// WorkspaceIdentitySourceReport groups attribution and delivery facts by host/hook.
type WorkspaceIdentitySourceReport struct {
	Client                 string                      `json:"client"`
	SourceHook             string                      `json:"source_hook"`
	ObservationCount       int                         `json:"observation_count"`
	Relationships          WorkspaceRelationshipCounts `json:"relationships"`
	IngestedConflictCount  int                         `json:"ingested_conflict_count"`
	KnownRelationshipCount int                         `json:"known_relationship_count"`
	ConflictRate           float64                     `json:"conflict_rate"`
	DeliveryAttemptCount   int                         `json:"delivery_attempt_count"`
	RuntimeAttemptCount    int                         `json:"runtime_attempt_count"`
	BackfilledAttemptCount int                         `json:"backfilled_attempt_count"`
	AcceptedDeliveryCount  int                         `json:"accepted_delivery_count"`
	IdentityConflictCount  int                         `json:"identity_conflict_count"`
	ExactRedeliveryCount   int                         `json:"exact_redelivery_count"`
	ExactRedeliveryRate    float64                     `json:"exact_redelivery_rate"`
}

// WorkspaceConflictSample is a body-free pointer for operator review.
type WorkspaceConflictSample struct {
	EventID    string `json:"event_id"`
	SessionID  string `json:"session_id"`
	Client     string `json:"client"`
	SourceHook string `json:"source_hook"`
}

// WorkspaceAliasSummary is the read-side projection of a reviewed alias.
type WorkspaceAliasSummary struct {
	SessionID  string    `json:"session_id"`
	Workspace  string    `json:"workspace"`
	ReviewedAt time.Time `json:"reviewed_at"`
	ReviewedBy string    `json:"reviewed_by"`
	Note       string    `json:"note,omitempty"`
}

// WorkspaceIdentityReport contains body-free identity diagnostics.
type WorkspaceIdentityReport struct {
	Coverage        WorkspaceIdentityCoverage       `json:"coverage"`
	Sources         []WorkspaceIdentitySourceReport `json:"sources"`
	ConflictSamples []WorkspaceConflictSample       `json:"conflict_samples"`
	Aliases         []WorkspaceAliasSummary         `json:"aliases"`
}
