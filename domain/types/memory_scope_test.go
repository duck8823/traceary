package types_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/domain/types"
)

func TestMemoryScopeKindFrom(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    types.MemoryScopeKind
		wantErr bool
	}{
		{name: "global", input: "global", want: types.MemoryScopeKindGlobal},
		{name: "workspace", input: "workspace", want: types.MemoryScopeKindWorkspace},
		{name: "agent", input: "agent", want: types.MemoryScopeKindAgent},
		{name: "session family", input: "session_family", want: types.MemoryScopeKindSessionFamily},
		{name: "rejects empty", input: "", wantErr: true},
		{name: "rejects unknown", input: "user", wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := types.MemoryScopeKindFrom(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("MemoryScopeKindFrom() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Fatalf("MemoryScopeKindFrom() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMemoryScopeFrom(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		kind     string
		value    string
		wantKind types.MemoryScopeKind
		wantKey  string
		wantErr  bool
	}{
		{name: "global scope", kind: "global", value: "global", wantKind: types.MemoryScopeKindGlobal, wantKey: "global"},
		{name: "workspace scope", kind: "workspace", value: "github.com/duck8823/traceary", wantKind: types.MemoryScopeKindWorkspace, wantKey: "github.com/duck8823/traceary"},
		{name: "agent scope", kind: "agent", value: "codex", wantKind: types.MemoryScopeKindAgent, wantKey: "codex"},
		{name: "session family scope", kind: "session_family", value: "session-123", wantKind: types.MemoryScopeKindSessionFamily, wantKey: "session-123"},
		{name: "rejects unknown kind", kind: "user", value: "abc", wantErr: true},
		{name: "rejects invalid value", kind: "workspace", value: " ", wantErr: true},
		{name: "rejects invalid global value", kind: "global", value: "workspace", wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := types.MemoryScopeFrom(tt.kind, tt.value)
			if (err != nil) != tt.wantErr {
				t.Fatalf("MemoryScopeFrom() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if diff := cmp.Diff(tt.wantKind, got.Kind()); diff != "" {
				t.Fatalf("Kind() mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantKey, got.Key()); diff != "" {
				t.Fatalf("Key() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
