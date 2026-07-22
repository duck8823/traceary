package usecase

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
)

const retentionPlanSchemaVersion = "retention-plan/v1"

var (
	canonicalUnsignedInteger = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)
	lowerHexDigest           = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

func encodeRetentionPlan(plan apptypes.RetentionPlan) ([]byte, error) {
	normalizeRetentionPlan(&plan)
	canonical, err := canonicalRetentionPayload(plan.CanonicalPayload)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(canonical)
	plan.PlanID = hex.EncodeToString(digest[:])
	if err := validateRetentionPlan(plan); err != nil {
		return nil, err
	}
	encoded, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return nil, xerrors.Errorf("marshal retention plan: %w", err)
	}
	return append(encoded, '\n'), nil
}

func decodeRetentionPlan(data []byte) (apptypes.RetentionPlan, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var plan apptypes.RetentionPlan
	if err := decoder.Decode(&plan); err != nil {
		return apptypes.RetentionPlan{}, xerrors.Errorf("parse retention plan: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return apptypes.RetentionPlan{}, err
	}
	if err := validateRetentionPlan(plan); err != nil {
		return apptypes.RetentionPlan{}, err
	}
	canonical, err := canonicalRetentionPayload(plan.CanonicalPayload)
	if err != nil {
		return apptypes.RetentionPlan{}, err
	}
	digest := sha256.Sum256(canonical)
	if hex.EncodeToString(digest[:]) != plan.PlanID {
		return apptypes.RetentionPlan{}, xerrors.Errorf("retention plan ID does not match canonical payload")
	}
	normalized, err := cloneRetentionPlan(plan)
	if err != nil {
		return apptypes.RetentionPlan{}, err
	}
	normalizeRetentionPlan(&normalized)
	if !retentionPlanOrderEqual(plan, normalized) {
		return apptypes.RetentionPlan{}, xerrors.Errorf("retention plan arrays are not in canonical order")
	}
	return plan, nil
}

func cloneRetentionPlan(plan apptypes.RetentionPlan) (apptypes.RetentionPlan, error) {
	data, err := json.Marshal(plan)
	if err != nil {
		return apptypes.RetentionPlan{}, xerrors.Errorf("clone retention plan: %w", err)
	}
	var cloned apptypes.RetentionPlan
	if err := json.Unmarshal(data, &cloned); err != nil {
		return apptypes.RetentionPlan{}, xerrors.Errorf("restore cloned retention plan: %w", err)
	}
	return cloned, nil
}

func canonicalRetentionPayload(payload apptypes.RetentionCanonicalPayload) ([]byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, xerrors.Errorf("marshal canonical payload: %w", err)
	}
	return canonicalizeJSON(raw)
}

func canonicalizeJSON(raw []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, xerrors.Errorf("decode canonical JSON input: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, err
	}
	var output bytes.Buffer
	if err := appendCanonicalJSON(&output, value); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func appendCanonicalJSON(output *bytes.Buffer, value any) error {
	switch typed := value.(type) {
	case nil:
		output.WriteString("null")
	case bool:
		if typed {
			output.WriteString("true")
		} else {
			output.WriteString("false")
		}
	case string:
		encoded, err := canonicalJSONString(typed)
		if err != nil {
			return err
		}
		output.Write(encoded)
	case json.Number:
		if !canonicalUnsignedInteger.MatchString(typed.String()) {
			return xerrors.Errorf("canonical retention JSON supports unsigned integer numbers only: %s", typed)
		}
		output.WriteString(typed.String())
	case []any:
		output.WriteByte('[')
		for index, item := range typed {
			if index > 0 {
				output.WriteByte(',')
			}
			if err := appendCanonicalJSON(output, item); err != nil {
				return err
			}
		}
		output.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			if !isASCII(key) {
				return xerrors.Errorf("canonical retention object key must be ASCII: %q", key)
			}
			keys = append(keys, key)
		}
		sort.Strings(keys)
		output.WriteByte('{')
		for index, key := range keys {
			if index > 0 {
				output.WriteByte(',')
			}
			encoded, err := canonicalJSONString(key)
			if err != nil {
				return err
			}
			output.Write(encoded)
			output.WriteByte(':')
			if err := appendCanonicalJSON(output, typed[key]); err != nil {
				return err
			}
		}
		output.WriteByte('}')
	default:
		return xerrors.Errorf("unsupported canonical JSON value %T", value)
	}
	return nil
}

func canonicalJSONString(value string) ([]byte, error) {
	if !utf8.ValidString(value) {
		return nil, xerrors.Errorf("canonical JSON string is not valid UTF-8")
	}
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, xerrors.Errorf("encode canonical JSON string: %w", err)
	}
	encoded := bytes.TrimSuffix(output.Bytes(), []byte{'\n'})
	encoded = bytes.ReplaceAll(encoded, []byte(`\u2028`), []byte("\xe2\x80\xa8"))
	encoded = bytes.ReplaceAll(encoded, []byte(`\u2029`), []byte("\xe2\x80\xa9"))
	return encoded, nil
}

func validateRetentionPlan(plan apptypes.RetentionPlan) error {
	if !lowerHexDigest.MatchString(plan.PlanID) {
		return xerrors.Errorf("retention plan ID must be a lowercase SHA-256 digest")
	}
	payload := plan.CanonicalPayload
	if payload.SchemaVersion != retentionPlanSchemaVersion {
		return xerrors.Errorf("unsupported retention plan schema %q", payload.SchemaVersion)
	}
	if payload.Source.DatabaseIdentity == "" || !lowerHexDigest.MatchString(payload.Source.DatabaseIdentity) {
		return xerrors.Errorf("retention plan database identity must be a lowercase SHA-256 digest")
	}
	if err := validateUTCInstant(payload.CreatedAt); err != nil {
		return xerrors.Errorf("invalid retention plan created_at: %w", err)
	}
	if err := validateUTCInstant(payload.SnapshotAt); err != nil {
		return xerrors.Errorf("invalid retention plan snapshot_at: %w", err)
	}
	if payload.Source.SQLiteUserVersion < 0 || !lowerHexDigest.MatchString(payload.Source.MigrationDigest) {
		return xerrors.Errorf("retention plan source and timestamps are required")
	}
	rootIDs := make(map[string]struct{}, len(payload.Source.Roots))
	for _, root := range payload.Source.Roots {
		if root.RootID == "" || !lowerHexDigest.MatchString(root.Fingerprint) {
			return xerrors.Errorf("retention plan root is invalid")
		}
		if _, exists := rootIDs[root.RootID]; exists {
			return xerrors.Errorf("duplicate retention plan root %s", root.RootID)
		}
		rootIDs[root.RootID] = struct{}{}
	}
	if len(payload.RecoveryRequirements) != 1 || !lowerHexDigest.MatchString(payload.RecoveryRequirements[0].Digest) || payload.RecoveryRequirements[0].State != "active" {
		return xerrors.Errorf("retention plan requires one active verified recovery point")
	}
	recovery := payload.RecoveryRequirements[0]
	if recovery.Generation == "" || recovery.RootID == "" || recovery.RelativePath == "" || !safeRelativePath(recovery.RelativePath) || !lowerHexDigest.MatchString(recovery.CoverageDigest) {
		return xerrors.Errorf("retention recovery point is invalid")
	}
	if _, exists := rootIDs[recovery.RootID]; !exists {
		return xerrors.Errorf("retention recovery point references an unknown root")
	}
	if len(payload.Policy.Ceilings) != 1 || payload.Policy.Ceilings[0].Class != "raw_body" || payload.Policy.Ceilings[0].Ceiling != "age" || !canonicalUnsignedInteger.MatchString(payload.Policy.Ceilings[0].Value) {
		return xerrors.Errorf("raw-body retention plan requires one age ceiling")
	}
	if len(payload.ClassResults) != 1 || payload.ClassResults[0].Class != "raw_body" || !validRetentionStatus(payload.ClassResults[0].Status) || len(payload.ClassResults[0].Ceilings) != 1 {
		return xerrors.Errorf("raw-body retention class result is invalid")
	}
	ceilingResult := payload.ClassResults[0].Ceilings[0]
	if ceilingResult.Ceiling != "age" || ceilingResult.Status != payload.ClassResults[0].Status || !validKnownExtent(ceilingResult.Current) || !validKnownExtent(ceilingResult.Projected) {
		return xerrors.Errorf("raw-body retention ceiling result is invalid")
	}
	seen := make(map[string]struct{}, len(payload.Candidates))
	for _, candidate := range payload.Candidates {
		if candidate.Class != "raw_body" || candidate.IdentityKind != "database" || candidate.DatabaseIdentity == "" || candidate.RootID != "" || candidate.RelativePath != "" {
			return xerrors.Errorf("retention plan contains a non-raw-body candidate")
		}
		if candidate.LogicalExtent.Availability != "known" || !canonicalUnsignedInteger.MatchString(candidate.LogicalExtent.Bytes) || candidate.AllocatedExtent.Availability != "unknown" {
			return xerrors.Errorf("retention candidate extent is invalid")
		}
		if candidate.AllocatedExtent.Bytes != "" || len(candidate.Reasons) != 1 || candidate.Reasons[0] != "age" {
			return xerrors.Errorf("retention candidate reasons or unknown extent are invalid")
		}
		if err := validateUTCInstant(candidate.Timestamp); err != nil {
			return xerrors.Errorf("invalid retention candidate timestamp: %w", err)
		}
		if _, _, err := parseRawBodyCandidateIdentity(candidate.CandidateIdentity); err != nil {
			return err
		}
		if _, exists := seen[candidate.CandidateIdentity]; exists {
			return xerrors.Errorf("duplicate retention candidate %s", candidate.CandidateIdentity)
		}
		seen[candidate.CandidateIdentity] = struct{}{}
	}
	if len(payload.Phases) != 1 || payload.Phases[0].Phase != "body_prune" || len(payload.Phases[0].Batches) != len(payload.Candidates) {
		return xerrors.Errorf("raw-body retention plan must contain one durable batch per candidate")
	}
	wantSteps := []string{"verify-plan", "confirm-plan", "verify-recovery", "verify-source", "prune-body", "record-ledger"}
	if len(payload.Phases[0].OrderedSteps) != len(wantSteps) {
		return xerrors.Errorf("retention body_prune steps are invalid")
	}
	for index, step := range wantSteps {
		if payload.Phases[0].OrderedSteps[index] != step {
			return xerrors.Errorf("retention body_prune steps are invalid")
		}
	}
	for index, candidate := range payload.Candidates {
		batch := payload.Phases[0].Batches[index]
		if batch.Ordinal != strconv.Itoa(index) || len(batch.CandidateIdentities) != 1 || batch.CandidateIdentities[0] != candidate.CandidateIdentity {
			return xerrors.Errorf("retention plan batch order does not match candidates")
		}
	}
	return nil
}

func validateUTCInstant(value string) error {
	if !strings.HasSuffix(value, "Z") {
		return xerrors.Errorf("timestamp must use UTC Z")
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return xerrors.Errorf("parse timestamp: %w", err)
	}
	if parsed.IsZero() {
		return xerrors.Errorf("timestamp must not be zero")
	}
	if value != parsed.UTC().Format(time.RFC3339Nano) {
		return xerrors.Errorf("timestamp must use canonical RFC3339Nano form")
	}
	return nil
}

func safeRelativePath(value string) bool {
	if strings.HasPrefix(value, "/") || strings.Contains(value, "\\") {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func validRetentionStatus(value string) bool {
	return value == "satisfied" || value == "unsatisfied" || value == "indeterminate"
}

func validKnownExtent(value apptypes.RetentionExtent) bool {
	return value.Availability == "known" && canonicalUnsignedInteger.MatchString(value.Bytes)
}

func normalizeRetentionPlan(plan *apptypes.RetentionPlan) {
	sort.Slice(plan.CanonicalPayload.Candidates, func(i, j int) bool {
		left, right := plan.CanonicalPayload.Candidates[i], plan.CanonicalPayload.Candidates[j]
		if left.Class != right.Class {
			return left.Class < right.Class
		}
		if left.DatabaseIdentity != right.DatabaseIdentity {
			return left.DatabaseIdentity < right.DatabaseIdentity
		}
		if left.RootID != right.RootID {
			return left.RootID < right.RootID
		}
		if left.RelativePath != right.RelativePath {
			return left.RelativePath < right.RelativePath
		}
		if left.Timestamp != right.Timestamp {
			return left.Timestamp < right.Timestamp
		}
		return left.CandidateIdentity < right.CandidateIdentity
	})
	for index := range plan.CanonicalPayload.Candidates {
		sort.Strings(plan.CanonicalPayload.Candidates[index].Reasons)
	}
	sort.Slice(plan.CanonicalPayload.Exclusions, func(i, j int) bool {
		left, right := plan.CanonicalPayload.Exclusions[i], plan.CanonicalPayload.Exclusions[j]
		if left.Reason != right.Reason {
			return left.Reason < right.Reason
		}
		return left.StableIdentity < right.StableIdentity
	})
	if len(plan.CanonicalPayload.Phases) == 1 {
		batches := make([]apptypes.RetentionPlanBatch, len(plan.CanonicalPayload.Candidates))
		for index, candidate := range plan.CanonicalPayload.Candidates {
			batches[index] = apptypes.RetentionPlanBatch{Ordinal: strconv.Itoa(index), CandidateIdentities: []string{candidate.CandidateIdentity}}
		}
		plan.CanonicalPayload.Phases[0].Batches = batches
	}
}

func retentionPlanOrderEqual(left, right apptypes.RetentionPlan) bool {
	leftJSON, _ := json.Marshal(left.CanonicalPayload)
	rightJSON, _ := json.Marshal(right.CanonicalPayload)
	return bytes.Equal(leftJSON, rightJSON)
}

func rawBodyCandidateIdentity(eventID, digest string) string {
	return "event:" + base64.RawURLEncoding.EncodeToString([]byte(eventID)) + ":sha256:" + digest
}

func parseRawBodyCandidateIdentity(value string) (string, string, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 4 || parts[0] != "event" || parts[2] != "sha256" || !lowerHexDigest.MatchString(parts[3]) {
		return "", "", xerrors.Errorf("invalid raw-body candidate identity")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(decoded) == 0 || !utf8.Valid(decoded) {
		return "", "", xerrors.Errorf("invalid event ID in raw-body candidate identity")
	}
	return string(decoded), parts[3], nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errorsIsEOF(err) {
		return nil
	}
	if err == nil {
		return xerrors.Errorf("JSON input contains multiple values")
	}
	return xerrors.Errorf("parse JSON trailer: %w", err)
}

func errorsIsEOF(err error) bool { return err == io.EOF }

func isASCII(value string) bool {
	for _, character := range value {
		if character > 0x7f {
			return false
		}
	}
	return true
}
