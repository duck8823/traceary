package types_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/domain/types"
)

func TestOptional_Some(t *testing.T) {
	t.Parallel()

	opt := types.Some("hello")

	if _, ok := opt.Value(); !ok {
		t.Errorf("Value() ok = false, want true")
	}
	value, ok := opt.Value()
	if !ok {
		t.Errorf("Value() ok = false, want true")
	}
	if diff := cmp.Diff("hello", value); diff != "" {
		t.Errorf("Value() mismatch (-want +got):\n%s", diff)
	}
}

func TestOptional_None(t *testing.T) {
	t.Parallel()

	opt := types.None[string]()

	if _, ok := opt.Value(); ok {
		t.Errorf("Value() ok = true, want false")
	}
	value, ok := opt.Value()
	if ok {
		t.Errorf("Value() ok = true, want false")
	}
	if diff := cmp.Diff("", value); diff != "" {
		t.Errorf("Value() mismatch (-want +got):\n%s", diff)
	}
}

func TestOptional_OrElse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		opt          types.Optional[int]
		defaultValue int
		want         int
	}{
		{
			name:         "present returns value",
			opt:          types.Some(42),
			defaultValue: 0,
			want:         42,
		},
		{
			name:         "empty returns default",
			opt:          types.None[int](),
			defaultValue: 99,
			want:         99,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.opt.OrElse(tt.defaultValue)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("OrElse() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestOptional_PointerValue(t *testing.T) {
	t.Parallel()

	// Ensure Optional works with pointer value types (common use case).
	type payload struct{ value string }

	present := types.Some(&payload{value: "set"})
	if _, ok := present.Value(); !ok {
		t.Errorf("Value() ok = false, want true")
	}
	p, ok := present.Value()
	if !ok {
		t.Errorf("Value() ok = false, want true")
	}
	if p == nil {
		t.Fatalf("Value() = nil, want non-nil pointer")
	}
	if diff := cmp.Diff("set", p.value); diff != "" {
		t.Errorf("value mismatch (-want +got):\n%s", diff)
	}

	empty := types.None[*payload]()
	if _, ok := empty.Value(); ok {
		t.Errorf("Value() ok = true, want false")
	}
	np, ok := empty.Value()
	if ok {
		t.Errorf("Value() ok = true, want false")
	}
	if np != nil {
		t.Errorf("Value() = %v, want nil", np)
	}
}

func TestOptional_LegacyAliasesRemainCompatible(t *testing.T) {
	t.Parallel()

	present := types.Of("legacy")
	gotPresent, ok := present.Get()
	if diff := cmp.Diff(true, ok); diff != "" {
		t.Fatalf("Get() ok mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("legacy", gotPresent); diff != "" {
		t.Fatalf("Get() value mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(true, present.IsPresent()); diff != "" {
		t.Fatalf("IsPresent() mismatch (-want +got):\n%s", diff)
	}

	empty := types.Empty[string]()
	gotEmpty, ok := empty.Get()
	if diff := cmp.Diff(false, ok); diff != "" {
		t.Fatalf("Get() ok mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("", gotEmpty); diff != "" {
		t.Fatalf("Get() value mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(false, empty.IsPresent()); diff != "" {
		t.Fatalf("IsPresent() mismatch (-want +got):\n%s", diff)
	}
}
