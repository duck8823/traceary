package application

import "context"

// AttestationAnchorInspectOptions selects whether the inspector may open SQLite.
type AttestationAnchorInspectOptions struct {
	StorePath string
	// OpenStore walks the chain and may publish a missing/behind file.
	// Large-store doctor leaves this false and only reads the sidecar.
	OpenStore bool
}

// AttestationAnchorState is the doctor-facing view of the sidecar and head.
type AttestationAnchorState struct {
	Path        string
	FilePresent bool
	FileSeq     int64
	FileHead    string
	StoreSeq    int64
	StoreHead   string
	ChainOK     bool
	Published   bool
	Relation    string
}

// AttestationAnchorInspector reads (and optionally heals) the sidecar.
type AttestationAnchorInspector interface {
	InspectAttestationAnchor(context.Context, AttestationAnchorInspectOptions) (AttestationAnchorState, error)
}
