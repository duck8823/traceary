package usecase

import (
	apptypes "github.com/duck8823/traceary/application/types"
	"golang.org/x/xerrors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const projectionIntegerBytes int64 = 8

// PlanProjectionBatch is pure: the same snapshot and budget produce the same plan.
func PlanProjectionBatch(s apptypes.ProjectionSnapshot, b apptypes.SearchProjectionBudget) (apptypes.ProjectionBatchPlan, error) {
	p := apptypes.ProjectionBatchPlan{GenerationID: s.Generation.GenerationID, Phase: s.Phase, ExpectedRevision: s.Generation.SourceRevision, ExpectedCheckpoint: s.Generation.Checkpoint, NextCheckpoint: s.Generation.Checkpoint, AllowRevisionDrift: s.CleanupAll, ContinueState: "rebuilding"}
	if s.CleanupAll {
		p.ContinueState = "drifted"
	}
	p.Ledger.LogicalWriteBytes = projectionCheckpointLogicalBytes(p)
	checkpointBytes := p.Ledger.LogicalWriteBytes
	if p.Ledger.LogicalWriteBytes > b.WriteBytes {
		return p, &apptypes.SearchProjectionOversizeError{Class: "write_bytes", Bytes: p.Ledger.LogicalWriteBytes, Limit: b.WriteBytes}
	}
	if s.Phase != "source" {
		retentionBudget := b
		retentionBudget.WriteBytes -= p.Ledger.LogicalWriteBytes
		rp, err := PlanProjectionRetention(s.Cleanup, retentionBudget)
		if err != nil {
			return p, err
		}
		p.Cleanup = rp.Candidates
		p.Ledger.Rows = rp.Ledger.Rows
		p.Ledger.LogicalWriteBytes += rp.Ledger.LogicalWriteBytes
		if p.Ledger.LogicalWriteBytes > b.WriteBytes {
			return p, &apptypes.SearchProjectionOversizeError{Class: "cleanup_bytes", Bytes: p.Ledger.LogicalWriteBytes, Limit: b.WriteBytes}
		}
		if s.CleanupDone && len(s.Cleanup) == len(p.Cleanup) {
			if s.Phase == "eviction" {
				p.NextPhase = "cleanup"
			} else {
				p.NextPhase = "complete"
				p.Completed = true
				if s.CleanupAll {
					p.FinalState = "drifted"
				} else {
					p.FinalState = "complete"
				}
			}
		}
		return p, nil
	}
	summaries := map[string]string{}
	var blockedExclusionBytes int64
	for _, d := range s.Documents {
		class, bytes, limit, err := classifyProjectionExclusion(d, b)
		if err != nil {
			return p, err
		}
		if class != "" {
			exclusion := apptypes.ProjectionExclusion{Sequence: d.Sequence, EventID: d.EventID, Class: class, MeasuredBytes: bytes, ByteLimit: limit}
			if p.Ledger.LogicalWriteBytes+projectionExclusionLogicalBytes(p.GenerationID, exclusion) > b.WriteBytes {
				blockedExclusionBytes = projectionExclusionLogicalBytes(p.GenerationID, exclusion)
				break
			}
			p.Exclusions = append(p.Exclusions, exclusion)
			p.Ledger.LogicalWriteBytes += projectionExclusionLogicalBytes(p.GenerationID, exclusion)
			p.NextCheckpoint = d.Sequence
			continue
		}
		if p.Ledger.Rows >= b.Rows || p.Ledger.StoredBytes+d.StoredBytes > b.StoredBytes || p.Ledger.DecodedBytes+d.DecodedBytes > b.DecodedBytes {
			break
		}
		w := apptypes.ProjectionWrite{Document: d, Keywords: map[string]int{}}
		w.LiteralFingerprints = apptypes.CharacterizeLiteralQuery(d.Text).Fingerprints()
		if created, err := time.Parse(time.RFC3339Nano, d.CreatedAt); err == nil {
			// Keep created_at_norm > max(ageCutoff, byteCutoff). Strict greater
			// errs under budget when timestamps tie (#1679 D4).
			cutoff := s.Now.Add(-b.RecentAge)
			if s.RecentCutoffNorm != "" {
				if byteCutoff, parseErr := time.Parse(time.RFC3339Nano, s.RecentCutoffNorm); parseErr == nil && byteCutoff.After(cutoff) {
					cutoff = byteCutoff
				}
			}
			w.RetainRecent = created.After(cutoff)
		}
		base, ok := summaries[d.SessionID]
		if !ok {
			base = d.PreviousSummary
		}
		w.Summary = truncateProjection(strings.TrimSpace(base+"\n"+d.Text), 4096)
		for _, k := range projectionTokens(strings.ToLower(d.Text)) {
			w.Keywords[k]++
		}
		w.LogicalBytes = projectionWriteLogicalBytes(p.GenerationID, w)
		if checkpointBytes+w.LogicalBytes > b.WriteBytes {
			exclusion := apptypes.ProjectionExclusion{Sequence: d.Sequence, EventID: d.EventID, Class: "write_bytes", MeasuredBytes: w.LogicalBytes, ByteLimit: b.WriteBytes}
			if p.Ledger.LogicalWriteBytes+projectionExclusionLogicalBytes(p.GenerationID, exclusion) > b.WriteBytes {
				blockedExclusionBytes = projectionExclusionLogicalBytes(p.GenerationID, exclusion)
				break
			}
			p.Exclusions = append(p.Exclusions, exclusion)
			p.Ledger.LogicalWriteBytes += projectionExclusionLogicalBytes(p.GenerationID, exclusion)
			p.NextCheckpoint = d.Sequence
			continue
		}
		if p.Ledger.LogicalWriteBytes+w.LogicalBytes > b.WriteBytes {
			break
		}
		p.Writes = append(p.Writes, w)
		summaries[d.SessionID] = w.Summary
		p.NextCheckpoint = d.Sequence
		p.Ledger.Rows++
		p.Ledger.StoredBytes += d.StoredBytes
		p.Ledger.DecodedBytes += d.DecodedBytes
		p.Ledger.LogicalWriteBytes += w.LogicalBytes
	}
	if len(s.Documents) > 0 && len(p.Writes) == 0 && len(p.Exclusions) == 0 {
		if blockedExclusionBytes > 0 {
			return p, &apptypes.SearchProjectionOversizeError{Class: "write_bytes", Bytes: p.Ledger.LogicalWriteBytes + blockedExclusionBytes, Limit: b.WriteBytes}
		}
		return p, &apptypes.SearchProjectionNoProgressError{Reason: "resource budget prevented the first row"}
	}
	if s.SourceDone && len(p.Writes)+len(p.Exclusions) == len(s.Documents) {
		p.NextPhase = "eviction"
	}
	return p, nil
}

// classifyProjectionExclusion derives the durable reason from measured values.
// Disposition only prevents hydration; it does not identify the exceeded limit.
func classifyProjectionExclusion(d apptypes.ProjectionDocument, b apptypes.SearchProjectionBudget) (string, int64, int64, error) {
	if d.StoredBytes > b.StoredBytes {
		return "stored_bytes", d.StoredBytes, b.StoredBytes, nil
	}
	if d.DecodedBytes > b.DecodedBytes {
		return "decoded_bytes", d.DecodedBytes, b.DecodedBytes, nil
	}
	if d.Disposition == apptypes.ProjectionDispositionExcluded {
		return "", 0, 0, xerrors.Errorf("excluded projection row %q exceeds no source budget", d.EventID)
	}
	return "", 0, 0, nil
}

func projectionExclusionLogicalBytes(generation string, e apptypes.ProjectionExclusion) int64 {
	return int64(len(generation) + len(e.EventID) + len(e.Class) + 32)
}

// PlanProjectionRetention creates a bounded, deterministic cleanup decision.
func PlanProjectionRetention(in []apptypes.ProjectionCleanupCandidate, b apptypes.SearchProjectionBudget) (apptypes.ProjectionRetentionPlan, error) {
	out := apptypes.ProjectionRetentionPlan{}
	for _, c := range in {
		if out.Ledger.Rows >= b.Rows || out.Ledger.LogicalWriteBytes+c.LogicalBytes > b.WriteBytes {
			break
		}
		out.Candidates = append(out.Candidates, c)
		out.Ledger.Rows++
		out.Ledger.LogicalWriteBytes += c.LogicalBytes
	}
	if len(in) > 0 && len(out.Candidates) == 0 {
		return out, &apptypes.SearchProjectionOversizeError{Class: "cleanup_bytes", Bytes: in[0].LogicalBytes, Limit: b.WriteBytes}
	}
	return out, nil
}

// projectionWriteLogicalBytes counts every logical column mutation. Recent
// text is counted once for its source row and once for the external FTS index.
func projectionWriteLogicalBytes(generation string, w apptypes.ProjectionWrite) int64 {
	d := w.Document
	// Summary mutates generation/session, event_count, summary, and two
	// versions. Aggregate mutates generation/session and two counters.
	n := int64(len(generation)+len(d.SessionID))*2 + projectionIntegerBytes*5 + int64(len(w.Summary))
	if w.RetainRecent {
		n += int64(len(generation)+len(d.EventID)+len(d.CreatedAt)+len(d.Text)*2) + projectionIntegerBytes*4
	}
	for keyword := range w.Keywords {
		n += int64(len(generation)+len(d.SessionID)+len(keyword)) + projectionIntegerBytes*2
	}
	for range w.LiteralFingerprints {
		n += int64(len(generation)+len(d.EventID)+apptypes.LiteralFingerprintBytes) + projectionIntegerBytes*2
	}
	return n
}

// projectionCheckpointLogicalBytes counts checkpoint, phase, state, elapsed
// milliseconds, and the normalized timestamp updated by every batch.
func projectionCheckpointLogicalBytes(p apptypes.ProjectionBatchPlan) int64 {
	return projectionIntegerBytes*2 + int64(len(p.GenerationID)+len(p.Phase)+len(p.ContinueState)+30)
}
func truncateProjection(v string, n int) string {
	if len(v) <= n {
		return v
	}
	v = v[:n]
	for !utf8.ValidString(v) {
		v = v[:len(v)-1]
	}
	return v
}
func projectionTokens(v string) []string {
	f := strings.FieldsFunc(v, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9') && !strings.ContainsRune("_./:@+-", r)
	})
	out := f[:0]
	for _, s := range f {
		if utf8.RuneCountInString(s) >= 12 {
			var a, d, y bool
			for _, r := range s {
				if r >= 'a' && r <= 'z' {
					a = true
				} else if r >= '0' && r <= '9' {
					d = true
				} else {
					y = true
				}
			}
			if a && (d || y) {
				out = append(out, s)
			}
		}
	}
	sort.Strings(out)
	return out
}
