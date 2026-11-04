//go:build darwin || linux

package runtime

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"unicode/utf8"

	domain "github.com/duck8823/traceary/domain/runtime"
	"github.com/google/uuid"
)

const maxNativeStartBytes = 1024 * 1024

// nativeStartPlan is infrastructure-owned intent, never a management RPC DTO.
// Syntax checks do not authenticate a native child or prove old-process exit.
type nativeStartPlan struct {
	Identity  domain.LaunchIdentity
	SessionID string
	CWD       string
	Store     string
	Namespace string
	Source    string
}

// nativeStartEvent retains only planned metadata. Vendor stdin, prompt/tool
// content, credentials and arbitrary environment fields are never retained.
type nativeStartEvent struct {
	Plan           nativeStartPlan
	TranscriptPath string
}

func canonicalNativePath(value string) bool {
	return value != "" && len(value) <= 4096 && filepath.IsAbs(value) && filepath.Clean(value) == value && !strings.ContainsAny(value, "\x00\r\n")
}
func (p nativeStartPlan) valid() bool {
	id, err := uuid.Parse(p.SessionID)
	return err == nil && id.String() == p.SessionID && id.Version() == 4 && id.Variant() == uuid.RFC4122 && p.Identity.Valid() && canonicalNativePath(p.CWD) && canonicalNativePath(p.Store) && canonicalNativePath(p.Namespace) && (p.Source == "startup" || p.Source == "resume")
}

// decodeNativeStart is a representation boundary only. The actual owned child
// adapter must additionally validate private marker provenance, pinned source,
// history/model/provider and original target; public observations cannot call
// this codec to produce RecoveryBinding or verified/control authority.
func decodeNativeStart(data []byte, plan nativeStartPlan, environment map[string]string) (nativeStartEvent, error) {
	return decodeNativeStartEvent(data, plan, environment, false)
}

func decodeNativeStartEvent(data []byte, plan nativeStartPlan, environment map[string]string, allowCompact bool) (nativeStartEvent, error) {
	if !plan.valid() {
		return nativeStartEvent{}, domain.ErrInvalid
	}
	if len(data) > maxNativeStartBytes {
		return nativeStartEvent{}, domain.ErrLimit
	}
	if !utf8.Valid(data) {
		return nativeStartEvent{}, domain.ErrInvalid
	}
	if err := uniqueJSONValue(json.NewDecoder(bytes.NewReader(data)), 0); err != nil {
		return nativeStartEvent{}, err
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return nativeStartEvent{}, domain.ErrInvalid
	}
	names := []string{"hook_event_name", "session_id", "cwd", "source", "transcript_path"}
	for key := range fields {
		for _, name := range names {
			if key != name && strings.EqualFold(strings.ReplaceAll(key, "_", ""), strings.ReplaceAll(name, "_", "")) {
				return nativeStartEvent{}, domain.ErrInvalid
			}
		}
	}
	values := make(map[string]string, len(names))
	for _, name := range names {
		raw, ok := fields[name]
		if !ok {
			return nativeStartEvent{}, domain.ErrInvalid
		}
		var value string
		if json.Unmarshal(raw, &value) != nil || value == "" {
			return nativeStartEvent{}, domain.ErrInvalid
		}
		values[name] = value
	}
	if values["hook_event_name"] != "SessionStart" {
		return nativeStartEvent{}, domain.ErrInvalid
	}
	if values["session_id"] != plan.SessionID || values["cwd"] != plan.CWD || values["source"] != plan.Source && (!allowCompact || values["source"] != "compact") {
		return nativeStartEvent{}, domain.ErrChanged
	}
	expected := map[string]string{"TRACEARY_RUNTIME_RUN_ID": plan.Identity.Run, "TRACEARY_RUNTIME_GENERATION": plan.Identity.Generation, "TRACEARY_RUNTIME_EPOCH": plan.Identity.Epoch, "TRACEARY_DB_PATH": plan.Store}
	for key, value := range expected {
		if environment[key] != value {
			return nativeStartEvent{}, domain.ErrChanged
		}
	}
	transcript := values["transcript_path"]
	if !canonicalNativePath(transcript) {
		return nativeStartEvent{}, domain.ErrInvalid
	}
	relative, err := filepath.Rel(plan.Namespace, transcript)
	if err != nil {
		return nativeStartEvent{}, domain.ErrInvalid
	}
	parts := strings.Split(relative, string(filepath.Separator))
	// The qualified main conversation is one project directory below projects;
	// subagent/other namespaces and a similarly-prefixed sibling never qualify.
	if len(parts) != 2 || parts[0] == "" || parts[0] == "." || parts[0] == ".." || parts[1] != plan.SessionID+".jsonl" {
		return nativeStartEvent{}, domain.ErrChanged
	}
	return nativeStartEvent{Plan: plan, TranscriptPath: transcript}, nil
}
