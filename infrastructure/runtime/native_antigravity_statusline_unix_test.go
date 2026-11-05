//go:build darwin || linux

package runtime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	domain "github.com/duck8823/traceary/domain/runtime"
	"reflect"
	"strings"
	"testing"
)

// Private representation tests do not qualify native startup or provider/auth.
const agyStatuslineSession = "00112233-4455-4677-8899-aabbccddeeff"

func agyStatuslineJSON(label string) string {
	return `{"session_id":"` + agyStatuslineSession + `","conversation_id":"` + agyStatuslineSession + `","cwd":"/fixture","workspace":{"current_dir":"/fixture","project_dir":"/project"},"model":{"id":"` + label + `","display_name":"` + label + `","effort":"medium"},"version":"1.5.0","product":"antigravity","email":"PRIVATE_ACCOUNT@example.invalid","agent_state":"working","tool_confirmation_pending":true,"private_body":{"credential":"PRIVATE_SECRET"}}`
}
func TestAntigravityStatuslineCodecReturnsOnlyReducedRepresentation(t *testing.T) {
	for label, slug := range map[string]string{"Gemini 4 Argon (Medium)": "gemini-4-argon-medium", "Gemini 4 Argon (High)": "gemini-4-argon-high", "Gemini 4 Argon (Low)": "gemini-4-argon-low"} {
		t.Run(slug, func(t *testing.T) {
			got, err := decodeAntigravityStatusline([]byte(agyStatuslineJSON(label)))
			want := antigravityStatuslineSnapshot{SessionID: agyStatuslineSession, CWD: "/fixture", ProjectDir: "/project", Model: slug, AccountPresent: true}
			if err != nil || got != want {
				t.Fatal(got, err)
			}
			if strings.Contains(fmt.Sprintf("%+v", got), "PRIVATE_") {
				t.Fatal("private values retained")
			}
		})
	}
	// State identity does not deduplicate or select a historical latest match.
	labels := []string{"Gemini 4 Argon (High)", "Gemini 4 Argon (Medium)", "Gemini 4 Argon (High)"}
	var models []string
	for _, label := range labels {
		v, err := decodeAntigravityStatusline([]byte(agyStatuslineJSON(label)))
		if err != nil {
			t.Fatal(err)
		}
		models = append(models, v.Model)
	}
	if !reflect.DeepEqual(models, []string{"gemini-4-argon-high", "gemini-4-argon-medium", "gemini-4-argon-high"}) {
		t.Fatal("A-B-A history collapsed", models)
	}
	typ := reflect.TypeFor[antigravityStatuslineSnapshot]()
	if typ.NumField() != 5 {
		t.Fatal("representation gained extra authority/body fields")
	}
	valid := agyStatuslineJSON("Gemini 4 Argon (Medium)")
	for _, replacement := range []string{`"email":""`, `"email":null`} {
		in := strings.Replace(valid, `"email":"PRIVATE_ACCOUNT@example.invalid"`, replacement, 1)
		got, err := decodeAntigravityStatusline([]byte(in))
		if err != nil || got.AccountPresent {
			t.Fatal("email absence became provider proof", got, err)
		}
	}
	missing := strings.Replace(valid, `"email":"PRIVATE_ACCOUNT@example.invalid",`, "", 1)
	got, err := decodeAntigravityStatusline([]byte(missing))
	if err != nil || got.AccountPresent {
		t.Fatal(got, err)
	}
}
func TestAntigravityStatuslineCodecRejectsPartialAliasesAndMalformedPayloads(t *testing.T) {
	valid := agyStatuslineJSON("Gemini 4 Argon (Medium)")
	cases := []struct {
		name, input string
		want        error
	}{
		{"initial_empty_ids", strings.ReplaceAll(valid, agyStatuslineSession, ""), domain.ErrNotFound},
		{"initial_one_empty", strings.Replace(valid, `"conversation_id":"`+agyStatuslineSession+`"`, `"conversation_id":""`, 1), domain.ErrNotFound},
		{"identity_conflict", strings.Replace(valid, `"conversation_id":"`+agyStatuslineSession+`"`, `"conversation_id":"11112233-4455-4677-8899-aabbccddeeff"`, 1), domain.ErrChanged},
		{"uuid_case", strings.ReplaceAll(valid, agyStatuslineSession, strings.ToUpper(agyStatuslineSession)), domain.ErrInvalid},
		{"uuid_non_v4", strings.ReplaceAll(valid, agyStatuslineSession, "00112233-4455-1677-8899-aabbccddeeff"), domain.ErrInvalid},
		{"root_alias", strings.Replace(valid, `"session_id"`, `"Session_ID"`, 1), domain.ErrInvalid},
		{"root_fold_alias", strings.Replace(valid, `"session_id"`, `"ſession_id"`, 1), domain.ErrInvalid},
		{"extra_alias", strings.Replace(valid, `"session_id":`, `"sessionid":"foreign","session_id":`, 1), domain.ErrInvalid},
		{"nested_alias", strings.Replace(valid, `"current_dir"`, `"currentDir"`, 1), domain.ErrInvalid},
		{"model_alias", strings.Replace(valid, `"display_name"`, `"displayName"`, 1), domain.ErrInvalid},
		{"project_alias", strings.Replace(valid, `"project_dir"`, `"Project_Dir"`, 1), domain.ErrInvalid},
		{"duplicate", strings.Replace(valid, `"version":"1.5.0"`, `"version":"1.5.0","version":"1.5.0"`, 1), domain.ErrInvalid},
		{"unknown_duplicate", strings.Replace(valid, `"private_body":{`, `"extra":0,"extra":0,"private_body":{`, 1), domain.ErrInvalid},
		{"version", strings.Replace(valid, `"version":"1.5.0"`, `"version":"1.5.1"`, 1), domain.ErrUnsupported},
		{"product", strings.Replace(valid, `"product":"antigravity"`, `"product":"codex"`, 1), domain.ErrUnsupported},
		{"unknown_model", strings.ReplaceAll(valid, "Gemini 4 Argon (Medium)", "PRIVATE_MODEL"), domain.ErrUnsupported},
		{"slug_not_measured_label", strings.ReplaceAll(valid, "Gemini 4 Argon (Medium)", "gemini-4-argon-medium"), domain.ErrUnsupported},
		{"model_conflict", strings.Replace(valid, `"display_name":"Gemini 4 Argon (Medium)"`, `"display_name":"Gemini 4 Argon (High)"`, 1), domain.ErrChanged},
		{"cwd_conflict", strings.Replace(valid, `"current_dir":"/fixture"`, `"current_dir":"/foreign"`, 1), domain.ErrChanged},
		{"relative_cwd", strings.Replace(valid, `"cwd":"/fixture"`, `"cwd":"relative"`, 1), domain.ErrInvalid},
		{"unclean_project", strings.Replace(valid, `"project_dir":"/project"`, `"project_dir":"/project/../foreign"`, 1), domain.ErrInvalid},
		{"email_type", strings.Replace(valid, `"email":"PRIVATE_ACCOUNT@example.invalid"`, `"email":true`, 1), domain.ErrInvalid},
		{"email_bound", strings.Replace(valid, "PRIVATE_ACCOUNT@example.invalid", strings.Repeat("x", 321), 1), domain.ErrInvalid},
		{"malformed", `{`, domain.ErrInvalid}, {"array", `[]`, domain.ErrInvalid}, {"trailing", valid + ` {}`, domain.ErrInvalid},
		{"utf8", strings.Replace(valid, "PRIVATE_SECRET", string([]byte{0xff}), 1), domain.ErrInvalid},
	}
	for _, key := range []string{"session_id", "conversation_id", "cwd", "model", "workspace", "version", "product"} {
		cases = append(cases, struct {
			name, input string
			want        error
		}{"null_" + key, strings.Replace(valid, `"`+key+`":`, `"`+key+`":null,"discarded_`+key+`":`, 1), domain.ErrInvalid})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := decodeAntigravityStatusline([]byte(tc.input))
			if !errors.Is(err, tc.want) || got != (antigravityStatuslineSnapshot{}) {
				t.Fatal(got, err, tc.want)
			}
			if err != nil && strings.Contains(err.Error(), "PRIVATE_") {
				t.Fatal("native value leaked")
			}
		})
	}
}
func TestAntigravityStatuslineCodecBounds(t *testing.T) {
	valid := agyStatuslineJSON("Gemini 4 Argon (Medium)")
	add := func(extra string) []byte { return []byte(valid[:len(valid)-1] + "," + extra + "}") }
	// Root counts as depth1; fifteen nested arrays produce sixteen containers.
	good := add(`"unknown":` + strings.Repeat("[", 15) + `0` + strings.Repeat("]", 15))
	bad := add(`"unknown":` + strings.Repeat("[", 16) + `0` + strings.Repeat("]", 16))
	if _, err := decodeAntigravityStatusline(good); err != nil {
		t.Fatal("depth16", err)
	}
	for name, payload := range map[string][]byte{"depth17": bad, "array129": add(`"unknown":[` + strings.Repeat(`0,`, 128) + `0]`), "oversize": bytes.Repeat([]byte{'x'}, 1048577)} {
		got, err := decodeAntigravityStatusline(payload)
		if !errors.Is(err, domain.ErrLimit) || got != (antigravityStatuslineSnapshot{}) {
			t.Fatal(name, got, err)
		}
	}
	if _, err := decodeAntigravityStatusline(add(`"unknown":[` + strings.Repeat(`0,`, 127) + `0]`)); err != nil {
		t.Fatal("array128", err)
	}
	// Put key-count boundaries in an unknown object, independent of native fields.
	for _, n := range []int{128, 129} {
		var b strings.Builder
		b.WriteString(`"unknown":{`)
		for i := 0; i < n; i++ {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, `"key%d":0`, i)
		}
		b.WriteByte('}')
		got, err := decodeAntigravityStatusline(add(b.String()))
		if n == 128 {
			if err != nil {
				t.Fatal("keys128", err)
			}
		} else if !errors.Is(err, domain.ErrLimit) || got != (antigravityStatuslineSnapshot{}) {
			t.Fatal("keys129", got, err)
		}
	}
	var rootFields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(valid), &rootFields); err != nil {
		t.Fatal(err)
	}
	for _, total := range []int{128, 129} {
		var b strings.Builder
		b.WriteString(valid[:len(valid)-1])
		for i := len(rootFields); i < total; i++ {
			fmt.Fprintf(&b, `,"root_extra%d":0`, i)
		}
		b.WriteByte('}')
		got, err := decodeAntigravityStatusline([]byte(b.String()))
		if total == 128 {
			if err != nil {
				t.Fatal("root128", err)
			}
		} else if !errors.Is(err, domain.ErrLimit) || got != (antigravityStatuslineSnapshot{}) {
			t.Fatal("root129", got, err)
		}
	}
	prefix := valid[:len(valid)-1] + `,"padding":"`
	suffix := `"}`
	exact := []byte(prefix + strings.Repeat("x", 1048576-len(prefix)-len(suffix)) + suffix)
	if _, err := decodeAntigravityStatusline(exact); err != nil {
		t.Fatal("exact byte bound", err)
	}
}
func FuzzAntigravityStatuslineCodec(f *testing.F) {
	f.Add([]byte(agyStatuslineJSON("Gemini 4 Argon (Medium)")))
	f.Add([]byte(`{"session_id":""}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		got, err := decodeAntigravityStatusline(data)
		if err != nil && got != (antigravityStatuslineSnapshot{}) {
			t.Fatal("failed snapshot retained")
		}
	})
}
