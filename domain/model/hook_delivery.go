package model

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/types"
)

// HookDeliveryEvidence is stable, body-independent host identity plus the two
// fingerprints needed to separate logical delivery from mutable attribution.
// It contains hashes only; raw event bodies remain on Event.
type HookDeliveryEvidence struct {
	reportedID             string
	deliveryFingerprint    string
	attributionFingerprint string
	rawWorkspace           string
}

// NewHookDeliveryEvidence builds validated evidence for a host delivery.
// Workspace is deliberately excluded from the delivery fingerprint and kept
// in the attribution fingerprint so a retry can improve attribution without
// creating another logical event.
func NewHookDeliveryEvidence(event *Event, nativeID, rawWorkspace string, semanticFields ...string) (HookDeliveryEvidence, error) {
	if event == nil {
		return HookDeliveryEvidence{}, xerrors.Errorf("event must not be nil")
	}
	trimmedNativeID := strings.TrimSpace(nativeID)
	if trimmedNativeID == "" {
		return HookDeliveryEvidence{}, xerrors.Errorf("native delivery ID must not be empty")
	}
	host := rootAgentName(event.Agent().String())
	if host == "" || strings.TrimSpace(event.SourceHook()) == "" || event.SessionID().String() == "" {
		return HookDeliveryEvidence{}, xerrors.Errorf("hook delivery requires host, source hook, and session ID")
	}
	reportedID := strings.Join([]string{host, event.SourceHook(), event.SessionID().String(), trimmedNativeID}, ":")
	deliveryFields := []string{
		event.Kind().String(),
		event.Client().String(),
		event.Agent().String(),
		event.SessionID().String(),
		event.SourceHook(),
		event.Body(),
	}
	deliveryFields = append(deliveryFields, semanticFields...)
	deliveryFingerprint := digestFields(deliveryFields...)
	attributionFingerprint := digestFields(event.Workspace().String(), rawWorkspace)
	return HookDeliveryEvidence{
		reportedID:             reportedID,
		deliveryFingerprint:    deliveryFingerprint,
		attributionFingerprint: attributionFingerprint,
		rawWorkspace:           rawWorkspace,
	}, nil
}

// ReportedID returns the namespaced host delivery identity.
func (e HookDeliveryEvidence) ReportedID() string { return e.reportedID }

// DeliveryFingerprint returns the semantic delivery fingerprint.
func (e HookDeliveryEvidence) DeliveryFingerprint() string { return e.deliveryFingerprint }

// AttributionFingerprint returns the workspace attribution fingerprint.
func (e HookDeliveryEvidence) AttributionFingerprint() string { return e.attributionFingerprint }

// RawWorkspace returns unnormalized host workspace evidence.
func (e HookDeliveryEvidence) RawWorkspace() string { return e.rawWorkspace }

// DeliveryRecordID returns the deterministic ledger row ID.
func (e HookDeliveryEvidence) DeliveryRecordID() string {
	return "delivery:" + digestFields(e.reportedID, e.deliveryFingerprint)
}

// ObservationID returns the deterministic observation ID for this delivery
// and attribution pair.
func (e HookDeliveryEvidence) ObservationID() string {
	return "observation:" + digestFields(e.DeliveryRecordID(), e.attributionFingerprint)
}

func rootAgentName(value string) string {
	root, _, _ := strings.Cut(strings.TrimSpace(value), "/")
	return strings.TrimSpace(root)
}

func digestFields(values ...string) string {
	h := sha256.New()
	var length [8]byte
	for _, value := range values {
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = h.Write(length[:])
		_, _ = h.Write([]byte(value))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// WorkspaceAttributionFingerprint returns a stable digest for normalized and
// raw workspace evidence. It is safe to persist because it contains no body.
func WorkspaceAttributionFingerprint(workspace types.Workspace, rawWorkspace string) string {
	return digestFields(workspace.String(), rawWorkspace)
}

// WorkspaceRelationship classifies effective attribution relative to the
// immutable session canonical workspace.
type WorkspaceRelationship string

// WorkspaceRelationshipExact and the related constants enumerate every
// persisted canonical/effective workspace classification.
const (
	WorkspaceRelationshipExact         WorkspaceRelationship = "exact"
	WorkspaceRelationshipDescendant    WorkspaceRelationship = "descendant"
	WorkspaceRelationshipAncestor      WorkspaceRelationship = "ancestor"
	WorkspaceRelationshipExplicitAlias WorkspaceRelationship = "explicit_alias"
	WorkspaceRelationshipConflict      WorkspaceRelationship = "conflict"
	WorkspaceRelationshipUnknown       WorkspaceRelationship = "unknown"
)

// ClassifyWorkspaceRelationship applies the v0.30 canonical/effective rule.
func ClassifyWorkspaceRelationship(canonical, effective types.Workspace) WorkspaceRelationship {
	canonicalValue := strings.TrimSpace(canonical.String())
	effectiveValue := strings.TrimSpace(effective.String())
	if canonicalValue == "" || effectiveValue == "" {
		return WorkspaceRelationshipUnknown
	}
	canonicalNormalized, canonicalLocal := normalizeAbsoluteWorkspacePath(canonicalValue)
	effectiveNormalized, effectiveLocal := normalizeAbsoluteWorkspacePath(effectiveValue)
	if canonicalLocal && effectiveLocal {
		if canonicalNormalized == effectiveNormalized {
			return WorkspaceRelationshipExact
		}
		if strings.HasPrefix(effectiveNormalized, canonicalNormalized+"/") {
			return WorkspaceRelationshipDescendant
		}
		if strings.HasPrefix(canonicalNormalized, effectiveNormalized+"/") {
			return WorkspaceRelationshipAncestor
		}
		return WorkspaceRelationshipConflict
	}
	if canonicalValue == effectiveValue {
		return WorkspaceRelationshipExact
	}
	return WorkspaceRelationshipConflict
}

func normalizeAbsoluteWorkspacePath(value string) (string, bool) {
	normalized := strings.ReplaceAll(strings.TrimSpace(value), `\`, "/")
	isUnixAbsolute := strings.HasPrefix(normalized, "/")
	isWindowsDriveAbsolute := len(normalized) >= 3 && normalized[1] == ':' && normalized[2] == '/'
	isUNCAbsolute := strings.HasPrefix(normalized, "//")
	if !isUnixAbsolute && !isWindowsDriveAbsolute && !isUNCAbsolute {
		return "", false
	}
	parts := strings.Split(normalized, "/")
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part {
		case "", ".":
			continue
		case "..":
			if len(cleaned) > 0 {
				cleaned = cleaned[:len(cleaned)-1]
			}
		default:
			cleaned = append(cleaned, part)
		}
	}
	prefix := "/"
	if isWindowsDriveAbsolute {
		prefix = strings.ToLower(cleaned[0]) + "/"
		cleaned = cleaned[1:]
	} else if isUNCAbsolute {
		prefix = "//"
	}
	if isWindowsDriveAbsolute || isUNCAbsolute {
		for i := range cleaned {
			cleaned[i] = strings.ToLower(cleaned[i])
		}
	}
	return strings.TrimSuffix(prefix+strings.Join(cleaned, "/"), "/"), true
}
