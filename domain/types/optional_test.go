package types_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/domain/types"
)

func TestOptional_Of(t *testing.T) {
	t.Parallel()

	opt := types.Of("hello")

	if !opt.IsPresent() {
		t.Errorf("IsPresent() = false, want true")
	}
	value, ok := opt.Get()
	if !ok {
		t.Errorf("Get() ok = false, want true")
	}
	if diff := cmp.Diff("hello", value); diff != "" {
		t.Errorf("Get() value mismatch (-want +got):\n%s", diff)
	}
}

func TestOptional_Empty(t *testing.T) {
	t.Parallel()

	opt := types.Empty[string]()

	if opt.IsPresent() {
		t.Errorf("IsPresent() = true, want false")
	}
	value, ok := opt.Get()
	if ok {
		t.Errorf("Get() ok = true, want false")
	}
	if diff := cmp.Diff("", value); diff != "" {
		t.Errorf("Get() value mismatch (-want +got):\n%s", diff)
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
			opt:          types.Of(42),
			defaultValue: 0,
			want:         42,
		},
		{
			name:         "empty returns default",
			opt:          types.Empty[int](),
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

	present := types.Of(&payload{value: "set"})
	if !present.IsPresent() {
		t.Errorf("IsPresent() = false, want true")
	}
	p, ok := present.Get()
	if !ok {
		t.Errorf("Get() ok = false, want true")
	}
	if p == nil {
		t.Fatalf("Get() value = nil, want non-nil pointer")
	}
	if diff := cmp.Diff("set", p.value); diff != "" {
		t.Errorf("value mismatch (-want +got):\n%s", diff)
	}

	empty := types.Empty[*payload]()
	if empty.IsPresent() {
		t.Errorf("IsPresent() = true, want false")
	}
	np, ok := empty.Get()
	if ok {
		t.Errorf("Get() ok = true, want false")
	}
	if np != nil {
		t.Errorf("Get() value = %v, want nil", np)
	}
}
