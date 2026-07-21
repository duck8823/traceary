package types_test

import (
	"testing"

	"github.com/duck8823/traceary/application/types"
)

func TestEventProjectionFrom(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name    string
		value   string
		want    types.EventProjection
		wantErr bool
	}{
		{name: "legacy", value: "", want: types.EventProjectionLegacy},
		{name: "metadata", value: " metadata ", want: types.EventProjectionMetadata},
		{name: "bounded", value: "bounded", want: types.EventProjectionBounded},
		{name: "full", value: "full", want: types.EventProjectionFull},
		{name: "unknown", value: "summary", wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := types.EventProjectionFrom(tt.value)
			if (err != nil) != tt.wantErr {
				t.Fatalf("EventProjectionFrom() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("EventProjectionFrom() = %q, want %q", got, tt.want)
			}
		})
	}
}
