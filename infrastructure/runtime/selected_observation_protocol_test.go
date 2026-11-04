package runtime

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
)

func selectedProtocolFixture(t *testing.T) app.SelectedObservation {
	t.Helper()
	metadata, err := domain.DerivedObservationOf(domain.DerivedObservationState{Host: "codex", HostVersion: "fixture 1", EventID: "spool:receipt", Kind: domain.ObservationPromptBoundary})
	if err != nil {
		t.Fatal(err)
	}
	return app.SelectedObservation{Selection: app.OutputSelection{Identity: domain.LaunchIdentity{Epoch: strings.Repeat("a", 32), Run: strings.Repeat("b", 32), Generation: strings.Repeat("c", 32)}, Worktree: domain.WorktreeID("w_" + strings.Repeat("d", 64)), Profile: "fixture"}, Metadata: metadata}
}

func TestSelectedObservationProtocolIsClosedAndRawByteBounded(t *testing.T) {
	observation := selectedProtocolFixture(t)
	selected, err := json.Marshal(recordSelectedObservation(observation))
	if err != nil {
		t.Fatal(err)
	}
	wrap := func(body string) []byte {
		return []byte(`{"protocol_version":1,"request_id":"selected","operation":"observe_selected_v1","selected_observation":` + body + `}`)
	}
	var decoded runtimeRequest
	if err = decodeRuntimeJSON(wrap(string(selected)), &decoded); err != nil {
		t.Fatal(err)
	}
	result, err := decoded.SelectedObservation.observation()
	if err != nil || result != observation {
		t.Fatal("roundtrip changed derived selection/metadata", err)
	}
	for _, tt := range []struct{ name, body string }{
		{"missing_selection", `{"metadata":{"host":"codex","host_version":"fixture 1","event_id":"event","kind":"prompt_boundary"}}`},
		{"missing_metadata", `{"selection":{}}`},
		{"unknown_body", strings.TrimSuffix(string(selected), "}") + `,"body":"must_not_forward"}`},
		{"redundant_nested_version", strings.TrimSuffix(string(selected), "}") + `,"version":1}`},
		{"null_optional", strings.Replace(string(selected), `"event_id":"spool:receipt"`, `"native_session_id":null,"event_id":"spool:receipt"`, 1)},
		{"unknown_effect", strings.Replace(string(selected), `"event_id":"spool:receipt"`, `"approval":true,"event_id":"spool:receipt"`, 1)},
		{"duplicate", strings.Replace(string(selected), `"event_id":"spool:receipt"`, `"event_id":"first","event_id":"second"`, 1)},
		{"bad_kind", strings.Replace(string(selected), "prompt_boundary", "approval_request", 1)},
		{"noncanonical_worktree", strings.Replace(string(selected), strings.Repeat("d", 64), strings.Repeat("D", 64), 1)},
		{"raw_whitespace_budget", "{" + strings.Repeat(" ", domain.MaxSelectedObservationBytes) + string(selected)[1:]},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var r runtimeRequest
			e := decodeRuntimeJSON(wrap(tt.body), &r)
			if e == nil {
				_, e = r.SelectedObservation.observation()
			}
			if !errors.Is(e, app.ErrRuntimeProtocol) {
				t.Fatalf("closed metadata error=%v", e)
			}
		})
	}
	state := observation.Metadata.Snapshot()
	state.EventID = strings.Repeat("a", 256)
	state.NativeSessionID = strings.Repeat("a", 256)
	state.NativeTurnID = strings.Repeat("a", 256)
	observation.Metadata, err = domain.DerivedObservationOf(state)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(recordSelectedObservation(observation))
	if err != nil {
		t.Fatal(err)
	}
	escaped := strings.ReplaceAll(string(encoded), "a", `\u0061`)
	if len(escaped) <= domain.MaxSelectedObservationBytes {
		t.Fatal("fixture did not exceed raw byte budget")
	}
	if e := decodeRuntimeJSON(wrap(escaped), &decoded); !errors.Is(e, app.ErrRuntimeProtocol) {
		t.Fatalf("escaped raw budget accepted: %v", e)
	}
	for _, arm := range []string{"history", "history_hint", "control", "receipt", "attach", "observation", "launch", "page"} {
		t.Run("mixed_"+arm, func(t *testing.T) {
			wire := strings.TrimSuffix(string(wrap(string(selected))), "}") + `,"` + arm + `":{}}`
			var r runtimeRequest
			if e := decodeRuntimeJSON([]byte(wire), &r); e != nil {
				if !errors.Is(e, app.ErrRuntimeProtocol) {
					t.Fatal(e)
				}
				return
			}
			if r.singleArm() {
				t.Fatal("mixed request shape accepted")
			}
		})
	}
	for _, wire := range []string{string(wrap(string(selected))) + ` {}`, strings.Replace(string(wrap(string(selected))), `"protocol_version":1`, `"protocol_version":1,"protocol_version":1`, 1)} {
		if e := decodeRuntimeJSON([]byte(wire), &decoded); !errors.Is(e, app.ErrRuntimeProtocol) {
			t.Fatalf("ambiguous outer frame accepted: %v", e)
		}
	}
	if e := decodeRuntimeJSON([]byte(`{"protocol_version":1,"request_id":"x","code":"queued","selected_observation":{}}`), &runtimeResponse{}); !errors.Is(e, app.ErrRuntimeProtocol) {
		t.Fatalf("unknown response arm accepted: %v", e)
	}
}

func FuzzSelectedObservationProtocol(f *testing.F) {
	f.Add([]byte(`{"protocol_version":1,"request_id":"x","operation":"observe_selected_v1","selected_observation":{}}`))
	f.Add([]byte(`{"protocol_version":2,"selected_observation":null}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > MaxRuntimeFrameBytes {
			return
		}
		var request runtimeRequest
		if decodeRuntimeJSON(data, &request) != nil || request.SelectedObservation == nil {
			return
		}
		observation, err := request.SelectedObservation.observation()
		if err != nil {
			return
		}
		serialized, e := json.Marshal(recordSelectedObservation(observation))
		if e != nil || len(serialized) > domain.MaxSelectedObservationBytes {
			t.Fatal("accepted value exceeds output budget", e)
		}
	})
}

func TestSelectedAcknowledgmentGuardCoversEveryResponseDataField(t *testing.T) {
	base := runtimeResponse{Version: 1, ID: "correlated", Code: "queued"}
	if !base.acknowledgmentOnly() {
		t.Fatal("bare correlated ACK rejected")
	}
	kind := reflect.TypeOf(base)
	for i := 0; i < kind.NumField(); i++ {
		field := kind.Field(i)
		if field.Name == "Version" || field.Name == "ID" || field.Name == "Code" {
			continue
		}
		t.Run(field.Name, func(t *testing.T) {
			candidate := base
			value := reflect.ValueOf(&candidate).Elem().Field(i)
			switch value.Kind() {
			case reflect.Pointer:
				value.Set(reflect.New(value.Type().Elem()))
			case reflect.String:
				value.SetString("unexpected")
			default:
				t.Fatal("new response data kind needs an explicit nonzero fixture", value.Kind())
			}
			if candidate.acknowledgmentOnly() {
				t.Fatal("response data arm passed closed ACK guard")
			}
		})
	}
}

func TestSelectedObservationProtocolRejectsCaseFoldedSchemaAliases(t *testing.T) {
	selected, err := json.Marshal(recordSelectedObservation(selectedProtocolFixture(t)))
	if err != nil {
		t.Fatal(err)
	}
	valid := `{"protocol_version":1,"request_id":"selected","operation":"observe_selected_v1","selected_observation":` + string(selected) + `}`
	for _, tt := range []struct{ name, old, replacement string }{
		{"outer_version", `"protocol_version":1`, `"Protocol_version":1`},
		{"outer_id", `"request_id":"selected"`, `"REQUEST_ID":"selected"`},
		{"outer_arm", `"selected_observation":`, `"Selected_Observation":`},
		{"selection", `"selection":`, `"Selection":`},
		{"epoch", `"epoch":`, `"Epoch":`},
		{"metadata", `"metadata":`, `"Metadata":`},
		{"host", `"host":"codex"`, `"Host":"codex"`},
		{"alias_duplicate", `"host":"codex"`, `"host":"codex","Host":"codex"`},
		{"unicode_outer_version", `"protocol_version":1`, `"protocol_verſion":1`},
		{"unicode_outer_arm", `"selected_observation":`, `"ſelected_obſervation":`},
		{"unicode_selection", `"selection":`, `"ſelection":`},
		{"unicode_host", `"host":"codex"`, `"hoſt":"codex"`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var request runtimeRequest
			data := strings.Replace(valid, tt.old, tt.replacement, 1)
			if data == valid {
				t.Fatal("alias fixture did not change")
			}
			if e := decodeRuntimeJSON([]byte(data), &request); !errors.Is(e, app.ErrRuntimeProtocol) {
				t.Fatalf("schema alias accepted: %v", e)
			}
		})
	}
}

func TestSelectedRequestUnionIncludesEveryTypedRequestArm(t *testing.T) {
	base := runtimeRequest{Operation: selectedObservationOperation, SelectedObservation: recordSelectedObservation(selectedProtocolFixture(t))}
	if !base.singleArm() {
		t.Fatal("one selected arm rejected")
	}
	kind := reflect.TypeOf(base)
	for i := 0; i < kind.NumField(); i++ {
		field := kind.Field(i)
		if field.Type.Kind() != reflect.Pointer || field.Name == "SelectedObservation" {
			continue
		}
		t.Run(field.Name, func(t *testing.T) {
			candidate := base
			value := reflect.ValueOf(&candidate).Elem().Field(i)
			value.Set(reflect.New(value.Type().Elem()))
			if candidate.singleArm() {
				t.Fatal("mixed typed request arm omitted from union")
			}
		})
	}
}

func TestSelectedBareAcknowledgmentRejectsUnicodeHeaderAliases(t *testing.T) {
	for _, data := range []string{
		`{"protocol_verſion":1,"request_id":"x","code":"queued"}`,
		`{"protocol_version":1,"requeſt_id":"x","code":"queued"}`,
		`{"protocol_version":2,"protocol_verſion":1,"request_id":"x","code":"queued"}`,
	} {
		var reply runtimeResponse
		if err := decodeRuntimeJSON([]byte(data), &reply); !errors.Is(err, app.ErrRuntimeProtocol) {
			t.Fatalf("ambiguous ACK accepted: %v", err)
		}
	}
}
