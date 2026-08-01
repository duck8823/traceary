package types_test

import (
	"reflect"
	"testing"

	"github.com/duck8823/traceary/application/types"
)

func TestOrderedPayloadRehearsalFieldsOwnsStableWorkflowOrder(t *testing.T) {
	fields := types.OrderedPayloadRehearsalFields()
	want := []types.PayloadRehearsalField{types.PayloadRehearsalEventBody, types.PayloadRehearsalCommandText, types.PayloadRehearsalInputText, types.PayloadRehearsalOutputText}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("fields = %v, want %v", fields, want)
	}
	fields[0] = types.PayloadRehearsalOutputText
	if types.OrderedPayloadRehearsalFields()[0] != types.PayloadRehearsalEventBody {
		t.Fatal("workflow order leaked mutable shared storage")
	}
}
