package types

type refIdentity[K comparable] struct {
	kind  K
	value string
}

type refWithIdentity[K comparable] interface {
	Kind() K
	Value() string
}

func appendUniqueRefs[T refWithIdentity[K], K comparable](dst []T, refs []T) []T {
	seen := make(map[refIdentity[K]]struct{}, len(dst)+len(refs))
	result := make([]T, 0, len(dst)+len(refs))
	result = appendUniqueRefsInto(result, seen, dst)
	result = appendUniqueRefsInto(result, seen, refs)
	return result
}

func appendUniqueRefsInto[T refWithIdentity[K], K comparable](result []T, seen map[refIdentity[K]]struct{}, refs []T) []T {
	for _, ref := range refs {
		key := refIdentity[K]{kind: ref.Kind(), value: ref.Value()}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, ref)
	}
	return result
}

// AppendEvidenceRefs appends evidence refs while preserving order and removing
// duplicate kind/value pairs.
func AppendEvidenceRefs(dst []EvidenceRef, refs []EvidenceRef) []EvidenceRef {
	return appendUniqueRefs[EvidenceRef, EvidenceRefKind](dst, refs)
}

// AppendArtifactRefs appends artifact refs while preserving order and removing
// duplicate kind/value pairs.
func AppendArtifactRefs(dst []ArtifactRef, refs []ArtifactRef) []ArtifactRef {
	return appendUniqueRefs[ArtifactRef, ArtifactRefKind](dst, refs)
}
