package types

import "testing"

func TestNewEventResponseBudget(t *testing.T) {
	t.Parallel()
	budget, err := NewEventResponseBudget(DefaultEventResponseItemLimit, DefaultEventResponseBodyRuneLimit)
	if err != nil {
		t.Fatalf("NewEventResponseBudget() error = %v", err)
	}
	if budget.AggregateBodyBytes() != MaxEventResponseAggregateBodyBytes {
		t.Fatalf("aggregate = %d", budget.AggregateBodyBytes())
	}
	for _, values := range [][2]int{{0, 1}, {MaxEventResponseItemLimit + 1, 1}, {1, -1}, {1, MaxEventResponseBodyRuneLimit + 1}} {
		if _, err := NewEventResponseBudget(values[0], values[1]); err == nil {
			t.Fatalf("NewEventResponseBudget(%d, %d) error = nil", values[0], values[1])
		}
	}
}

func TestEventPageExtentCopiesReasons(t *testing.T) {
	t.Parallel()
	reasons := []string{"aggregate_body_budget"}
	extent, err := NewEventPageExtent(2, 1, true, true, reasons)
	if err != nil {
		t.Fatalf("NewEventPageExtent() error = %v", err)
	}
	reasons[0] = "mutated"
	if got := extent.Reasons(); len(got) != 1 || got[0] != "aggregate_body_budget" {
		t.Fatalf("Reasons() = %#v", got)
	}
	if _, err := NewEventPageExtent(1, 2, false, false, nil); err == nil {
		t.Fatal("invalid extent was accepted")
	}
}
