// Package usecase — bundle usecase implements the v0.9 portability
// primitive introduced for #572: a local-first, encrypted,
// content-verifiable archive that operators can move between their
// machines through any file-transport they already have (AirDrop,
// scp, Syncthing, etc.). Traceary never ships its own transport.
//
// Portability covers all five tables — events, sessions, command_audits,
// memories, and memory_edges — see docs/operations/cross-machine-handoff
// for the operator guide.
package usecase

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"sort"
	"time"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func encodeSessionsNDJSON(sessions []*model.Session) (*bytes.Buffer, error) {
	buf := &bytes.Buffer{}
	sorted := append([]*model.Session(nil), sessions...)
	sort.Slice(sorted, func(i, j int) bool {
		leftParent := sorted[i].ParentSessionID().String()
		rightParent := sorted[j].ParentSessionID().String()
		if (leftParent == "") != (rightParent == "") {
			return leftParent == ""
		}
		if leftParent != rightParent {
			return leftParent < rightParent
		}
		return sorted[i].SessionID().String() < sorted[j].SessionID().String()
	})
	enc := json.NewEncoder(buf)
	for _, session := range sorted {
		row := bundleSessionRow{
			SessionID:       session.SessionID().String(),
			StartedAt:       session.StartedAt().UTC().Format(time.RFC3339Nano),
			Client:          session.Client().String(),
			Agent:           session.Agent().String(),
			Workspace:       session.Workspace().String(),
			Label:           session.Label(),
			Summary:         session.Summary(),
			ParentSessionID: session.ParentSessionID().String(),
			SpawnEventID:    session.SpawnEventID().String(),
			SubagentKind:    session.SubagentKind(),
			RuntimeMode:     session.RuntimeMode().String(),
		}
		if endedAt, ok := session.EndedAt().Value(); ok {
			row.EndedAt = endedAt.UTC().Format(time.RFC3339Nano)
		}
		if spawnOrder, ok := session.SpawnOrder().Value(); ok {
			row.SpawnOrder = &spawnOrder
		}
		if terminalReason, ok := session.TerminalReason().Value(); ok {
			row.TerminalReason = terminalReason.String()
		}
		if err := enc.Encode(row); err != nil {
			return nil, xerrors.Errorf("encode session: %w", err)
		}
	}
	return buf, nil
}

func encodeEventsNDJSON(events []*model.Event) (*bytes.Buffer, error) {
	buf := &bytes.Buffer{}
	// Sort to make output reproducible for a given filter set.
	sort.Slice(events, func(i, j int) bool {
		if !events[i].CreatedAt().Equal(events[j].CreatedAt()) {
			return events[i].CreatedAt().Before(events[j].CreatedAt())
		}
		return events[i].EventID().String() < events[j].EventID().String()
	})
	enc := json.NewEncoder(buf)
	for _, event := range events {
		if err := enc.Encode(bundleEventRow{
			EventID:    event.EventID().String(),
			Kind:       event.Kind().String(),
			Client:     event.Client().String(),
			Agent:      event.Agent().String(),
			SessionID:  event.SessionID().String(),
			Workspace:  event.Workspace().String(),
			Body:       event.Body(),
			SourceHook: event.SourceHook(),
			CreatedAt:  event.CreatedAt().UTC().Format(time.RFC3339Nano),
		}); err != nil {
			return nil, xerrors.Errorf("encode event: %w", err)
		}
	}
	return buf, nil
}

func encodeCommandAuditsNDJSON(audits []*model.CommandAudit) (*bytes.Buffer, error) {
	buf := &bytes.Buffer{}
	sorted := append([]*model.CommandAudit(nil), audits...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].EventID().String() < sorted[j].EventID().String()
	})
	enc := json.NewEncoder(buf)
	for _, audit := range sorted {
		row := bundleCommandAuditRow{
			EventID:             audit.EventID().String(),
			Command:             audit.Command(),
			CommandName:         audit.CommandIdentity().Command().String(),
			Input:               audit.Input(),
			Output:              audit.Output(),
			InputTruncated:      audit.InputTruncated(),
			OutputTruncated:     audit.OutputTruncated(),
			InputOriginalBytes:  audit.InputOriginalBytes(),
			OutputOriginalBytes: audit.OutputOriginalBytes(),
			Failed:              audit.Failed(),
			FailureReason:       audit.FailureReason().String(),
		}
		if wrapper, ok := audit.CommandIdentity().Wrapper().Value(); ok {
			row.Wrapper = wrapper.String()
		}
		if exitCode, ok := audit.ExitCode().Value(); ok {
			row.ExitCode = &exitCode
		}
		if err := enc.Encode(row); err != nil {
			return nil, xerrors.Errorf("encode command audit: %w", err)
		}
	}
	return buf, nil
}

func filterSessionsForBundleExport(sessions []*model.Session, events []*model.Event, opts BundleExportOptions) []*model.Session {
	referencedSessionIDs := make(map[string]struct{}, len(events))
	for _, event := range events {
		referencedSessionIDs[event.SessionID().String()] = struct{}{}
	}

	filtered := make([]*model.Session, 0, len(sessions))
	includedSessionIDs := make(map[string]struct{}, len(sessions))
	for _, session := range sessions {
		if sessionMatchesBundleExportFilters(session, opts) {
			filtered = append(filtered, session)
			includedSessionIDs[session.SessionID().String()] = struct{}{}
		}
	}

	for _, session := range sessions {
		sessionID := session.SessionID().String()
		if _, referenced := referencedSessionIDs[sessionID]; !referenced {
			continue
		}
		if _, alreadyIncluded := includedSessionIDs[sessionID]; alreadyIncluded {
			continue
		}
		filtered = append(filtered, session)
		includedSessionIDs[sessionID] = struct{}{}
	}
	return filtered
}

func sessionMatchesBundleExportFilters(session *model.Session, opts BundleExportOptions) bool {
	if opts.Workspace.String() != "" && session.Workspace() != opts.Workspace {
		return false
	}
	if !opts.Since.IsZero() && session.StartedAt().Before(opts.Since) {
		return false
	}
	if !opts.Until.IsZero() && !session.StartedAt().Before(opts.Until) {
		return false
	}
	return true
}

func filterCommandAuditsForEvents(audits []*model.CommandAudit, events []*model.Event) []*model.CommandAudit {
	eventIDs := make(map[string]struct{}, len(events))
	for _, event := range events {
		eventIDs[event.EventID().String()] = struct{}{}
	}
	filtered := make([]*model.CommandAudit, 0, len(audits))
	for _, audit := range audits {
		if _, ok := eventIDs[audit.EventID().String()]; ok {
			filtered = append(filtered, audit)
		}
	}
	return filtered
}

func encodeMemoriesNDJSON(memories []apptypes.MemoryDetails) (*bytes.Buffer, error) {
	buf := &bytes.Buffer{}
	memories = topologicallySortMemoryDetails(memories)
	enc := json.NewEncoder(buf)
	for _, details := range memories {
		summary := details.Summary()
		row := bundleMemoryRow{
			MemoryID:     summary.MemoryID().String(),
			Type:         summary.MemoryType().String(),
			ScopeKind:    summary.Scope().Kind().String(),
			ScopeValue:   summary.Scope().Key(),
			Fact:         summary.Fact(),
			Status:       summary.Status().String(),
			Confidence:   summary.Confidence().String(),
			Source:       summary.Source().String(),
			EvidenceRefs: refsToBundleEvidenceRows(details.EvidenceRefs()),
			ArtifactRefs: refsToBundleArtifactRows(details.ArtifactRefs()),
			ValidFrom:    summary.ValidFrom().UTC().Format(time.RFC3339Nano),
			CreatedAt:    summary.CreatedAt().UTC().Format(time.RFC3339Nano),
			UpdatedAt:    summary.UpdatedAt().UTC().Format(time.RFC3339Nano),
		}
		if supersedes, ok := summary.Supersedes().Value(); ok {
			row.SupersedesMemoryID = supersedes.String()
		}
		if expiresAt, ok := summary.ExpiresAt().Value(); ok {
			row.ExpiresAt = expiresAt.UTC().Format(time.RFC3339Nano)
		}
		if validTo, ok := summary.ValidTo().Value(); ok {
			row.ValidTo = validTo.UTC().Format(time.RFC3339Nano)
		}
		if err := enc.Encode(row); err != nil {
			return nil, xerrors.Errorf("encode memory: %w", err)
		}
	}
	return buf, nil
}

func encodeMemoryEdgesNDJSON(edges []*model.MemoryEdge) (*bytes.Buffer, error) {
	buf := &bytes.Buffer{}
	edges = append([]*model.MemoryEdge(nil), edges...)
	sort.Slice(edges, func(i, j int) bool {
		if !edges[i].ValidFrom().Equal(edges[j].ValidFrom()) {
			return edges[i].ValidFrom().Before(edges[j].ValidFrom())
		}
		return edges[i].EdgeID().String() < edges[j].EdgeID().String()
	})
	enc := json.NewEncoder(buf)
	for _, edge := range edges {
		row := bundleMemoryEdgeRow{
			EdgeID:       edge.EdgeID().String(),
			FromMemoryID: edge.FromMemoryID().String(),
			ToMemoryID:   edge.ToMemoryID().String(),
			RelationType: edge.RelationType().String(),
			ValidFrom:    edge.ValidFrom().UTC().Format(time.RFC3339Nano),
			CreatedAt:    edge.CreatedAt().UTC().Format(time.RFC3339Nano),
		}
		if validTo, ok := edge.ValidTo().Value(); ok {
			row.ValidTo = validTo.UTC().Format(time.RFC3339Nano)
		}
		if err := enc.Encode(row); err != nil {
			return nil, xerrors.Errorf("encode memory edge: %w", err)
		}
	}
	return buf, nil
}

func topologicallySortBundleMemoryRows(rows []bundleRow) ([]bundleMemoryRow, error) {
	memories := make([]bundleMemoryRow, 0, len(rows))
	for _, generic := range rows {
		row, ok := generic.(bundleMemoryRow)
		if !ok {
			return nil, xerrors.Errorf("unexpected memories row type %T", generic)
		}
		memories = append(memories, row)
	}
	sortedIndexes, err := topologicallySortMemoryIndexes(
		len(memories),
		func(i int) string { return memories[i].MemoryID },
		func(i int) string { return memories[i].SupersedesMemoryID },
	)
	if err != nil {
		return nil, err
	}
	sorted := make([]bundleMemoryRow, 0, len(memories))
	for _, idx := range sortedIndexes {
		sorted = append(sorted, memories[idx])
	}
	return sorted, nil
}

func sortBundleSessionRows(rows []bundleRow) ([]bundleSessionRow, error) {
	sessions := make([]bundleSessionRow, 0, len(rows))
	for _, generic := range rows {
		row, ok := generic.(bundleSessionRow)
		if !ok {
			return nil, xerrors.Errorf("unexpected sessions row type %T", generic)
		}
		sessions = append(sessions, row)
	}
	sort.Slice(sessions, func(i, j int) bool {
		leftParent := sessions[i].ParentSessionID
		rightParent := sessions[j].ParentSessionID
		if (leftParent == "") != (rightParent == "") {
			return leftParent == ""
		}
		if leftParent != rightParent {
			return leftParent < rightParent
		}
		return sessions[i].SessionID < sessions[j].SessionID
	})
	return sessions, nil
}

func topologicallySortMemoryDetails(memories []apptypes.MemoryDetails) []apptypes.MemoryDetails {
	sortedIndexes, err := topologicallySortMemoryIndexes(
		len(memories),
		func(i int) string { return memories[i].Summary().MemoryID().String() },
		func(i int) string {
			if supersedes, ok := memories[i].Summary().Supersedes().Value(); ok {
				return supersedes.String()
			}
			return ""
		},
	)
	if err != nil {
		// Export reads trusted local state. If that state contains impossible
		// cycles or duplicate IDs, keep deterministic ID order rather than
		// failing bundle creation here; repository constraints own that invariant.
		sorted := append([]apptypes.MemoryDetails(nil), memories...)
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i].Summary().MemoryID().String() < sorted[j].Summary().MemoryID().String()
		})
		return sorted
	}
	sorted := make([]apptypes.MemoryDetails, 0, len(memories))
	for _, idx := range sortedIndexes {
		sorted = append(sorted, memories[idx])
	}
	return sorted
}

func topologicallySortMemoryIndexes(
	count int,
	idAt func(int) string,
	supersedesAt func(int) string,
) ([]int, error) {
	indexByID := make(map[string]int, count)
	for i := 0; i < count; i++ {
		id := idAt(i)
		if id == "" {
			return nil, xerrors.Errorf("memory row has empty memory_id")
		}
		if _, exists := indexByID[id]; exists {
			return nil, xerrors.Errorf("bundle contains duplicate memory_id %s", id)
		}
		indexByID[id] = i
	}

	childrenByParent := make(map[string][]int, count)
	indegree := make([]int, count)
	for i := 0; i < count; i++ {
		parentID := supersedesAt(i)
		if parentID == "" {
			continue
		}
		if _, parentInBundle := indexByID[parentID]; !parentInBundle {
			continue
		}
		childrenByParent[parentID] = append(childrenByParent[parentID], i)
		indegree[i]++
	}
	for parentID := range childrenByParent {
		sort.Slice(childrenByParent[parentID], func(i, j int) bool {
			return idAt(childrenByParent[parentID][i]) < idAt(childrenByParent[parentID][j])
		})
	}

	ready := make([]int, 0, count)
	for i := 0; i < count; i++ {
		if indegree[i] == 0 {
			ready = append(ready, i)
		}
	}
	sort.Slice(ready, func(i, j int) bool {
		leftRoot := supersedesAt(ready[i]) == ""
		rightRoot := supersedesAt(ready[j]) == ""
		if leftRoot != rightRoot {
			return leftRoot
		}
		return idAt(ready[i]) < idAt(ready[j])
	})

	sorted := make([]int, 0, count)
	for len(ready) > 0 {
		current := ready[0]
		ready = ready[1:]
		sorted = append(sorted, current)
		for _, child := range childrenByParent[idAt(current)] {
			indegree[child]--
			if indegree[child] == 0 {
				ready = append(ready, child)
			}
		}
		sort.Slice(ready, func(i, j int) bool {
			leftRoot := supersedesAt(ready[i]) == ""
			rightRoot := supersedesAt(ready[j]) == ""
			if leftRoot != rightRoot {
				return leftRoot
			}
			return idAt(ready[i]) < idAt(ready[j])
		})
	}
	if len(sorted) != count {
		return nil, xerrors.Errorf("bundle memories contain a supersession cycle")
	}
	return sorted, nil
}

func refsToBundleEvidenceRows(refs []types.EvidenceRef) []bundleRefRow {
	rows := make([]bundleRefRow, 0, len(refs))
	for _, ref := range refs {
		rows = append(rows, bundleRefRow{Kind: ref.Kind().String(), Value: ref.Value()})
	}
	return rows
}

func refsToBundleArtifactRows(refs []types.ArtifactRef) []bundleRefRow {
	rows := make([]bundleRefRow, 0, len(refs))
	for _, ref := range refs {
		rows = append(rows, bundleRefRow{Kind: ref.Kind().String(), Value: ref.Value()})
	}
	return rows
}

func parseOptionalBundleTime(value, field string) (types.Optional[time.Time], error) {
	if value == "" {
		return types.None[time.Time](), nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return types.None[time.Time](), xerrors.Errorf("%s: %w", field, err)
	}
	return types.Some(parsed), nil
}

func encodeTarGz(files map[string][]byte) ([]byte, error) {
	buf := &bytes.Buffer{}
	gzw := gzip.NewWriter(buf)
	tw := tar.NewWriter(gzw)
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, name := range keys {
		data := files[name]
		if err := tw.WriteHeader(&tar.Header{
			Name: name,
			Mode: 0o600,
			Size: int64(len(data)),
		}); err != nil {
			return nil, xerrors.Errorf("tar header for %s: %w", name, err)
		}
		if _, err := tw.Write(data); err != nil {
			return nil, xerrors.Errorf("tar write %s: %w", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		return nil, xerrors.Errorf("tar close: %w", err)
	}
	if err := gzw.Close(); err != nil {
		return nil, xerrors.Errorf("gzip close: %w", err)
	}
	return buf.Bytes(), nil
}

func decodeTarGz(data []byte) (map[string][]byte, error) {
	gzr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, xerrors.Errorf("gzip reader: %w", err)
	}
	defer func() { _ = gzr.Close() }()
	tr := tar.NewReader(gzr)
	out := make(map[string][]byte)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, xerrors.Errorf("tar next: %w", err)
		}
		content, err := io.ReadAll(tr)
		if err != nil {
			return nil, xerrors.Errorf("tar read %s: %w", hdr.Name, err)
		}
		out[hdr.Name] = content
	}
	return out, nil
}

// sealBundle encrypts plaintext with a key derived from the
// passphrase via Argon2id, using XChaCha20-Poly1305 (24-byte nonce)
// so we can safely generate nonces randomly per bundle. Argon2id
// parameters (3 iterations, 64 MiB, 4 lanes) follow the OWASP
// general-purpose recommendation and cost ~100ms on typical hardware.
func sealBundle(plaintext, passphrase []byte) ([]byte, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, xerrors.Errorf("salt: %w", err)
	}
	key := argon2.IDKey(passphrase, salt, 3, 64*1024, 4, chacha20poly1305.KeySize)
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, xerrors.Errorf("aead init: %w", err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, xerrors.Errorf("nonce: %w", err)
	}
	ciphertext := aead.Seal(nil, nonce, plaintext, bundleMagic)

	out := &bytes.Buffer{}
	out.Write(bundleMagic)
	out.WriteByte(bundleEnvelope)
	out.Write(salt)
	out.Write(nonce)
	out.Write(ciphertext)
	return out.Bytes(), nil
}

func openBundle(data, passphrase []byte) ([]byte, error) {
	headerSize := len(bundleMagic) + 1 + 16 + 24
	if len(data) < headerSize {
		return nil, xerrors.Errorf("bundle is too short to be a Traceary archive")
	}
	if !bytes.Equal(data[:len(bundleMagic)], bundleMagic) {
		return nil, xerrors.Errorf("bundle does not have the Traceary magic prefix")
	}
	cursor := len(bundleMagic)
	version := data[cursor]
	cursor++
	if version != bundleEnvelope {
		return nil, xerrors.Errorf("unsupported bundle envelope version %d", version)
	}
	salt := data[cursor : cursor+16]
	cursor += 16
	key := argon2.IDKey(passphrase, salt, 3, 64*1024, 4, chacha20poly1305.KeySize)
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, xerrors.Errorf("aead init: %w", err)
	}
	nonce := data[cursor : cursor+aead.NonceSize()]
	cursor += aead.NonceSize()
	ciphertext := data[cursor:]
	plaintext, err := aead.Open(nil, nonce, ciphertext, bundleMagic)
	if err != nil {
		return nil, xerrors.Errorf("decryption failed (wrong passphrase or corrupt bundle): %w", err)
	}
	return plaintext, nil
}

func hashSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func formatOptionalTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}
