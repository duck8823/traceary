package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain"
)

func TestInspectUnavailableRetentionFailsWithCountSampleAndDigest(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	store := &offlineMigrationStoreStub{}
	store.unavailable = apptypes.UnavailableRetentionInspection{
		RowCount:      2,
		Digest:        "abc",
		Sample:        []string{"evt-1", "evt-2"},
		SchemaVersion: 82,
	}
	root := NewRootCLI(WithStoreManagement(store))
	check := root.inspectUnavailableRetention(context.Background())
	if check.Name != "unavailable-retention" || check.Status != doctorStatusFail {
		t.Fatalf("check = %#v", check)
	}
	for _, want := range []string{
		"unavailable_retention: 2",
		"cannot be recovered",
		"0.48.2",
		"sha256:abc",
		"ids: evt-1,evt-2",
		"2:abc",
	} {
		if !strings.Contains(check.Message, want) {
			t.Fatalf("message %q does not contain %q", check.Message, want)
		}
	}
}

func TestUnavailableRetentionApprovalForFixRejectsMissingAndMismatchedToken(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := &offlineMigrationStoreStub{
		unavailable: apptypes.UnavailableRetentionInspection{
			RowCount:      3,
			Digest:        "deadbeef",
			Sample:        []string{"a", "b", "c"},
			SchemaVersion: 82,
		},
	}
	_, err := unavailableRetentionApprovalForFix(ctx, store, "")
	var required *apptypes.UnavailableRetentionApprovalRequiredError
	if err == nil {
		t.Fatal("empty token succeeded")
	}
	if !errors.As(err, &required) {
		t.Fatalf("err = %v, want UnavailableRetentionApprovalRequiredError", err)
	}
	_, err = unavailableRetentionApprovalForFix(ctx, store, "1:deadbeef")
	if !errors.As(err, &required) {
		t.Fatalf("mismatched token err = %v, want required", err)
	}
	approval, err := unavailableRetentionApprovalForFix(ctx, store, "3:deadbeef")
	if err != nil {
		t.Fatal(err)
	}
	if approval == nil {
		t.Fatal("matching token returned nil approval")
	}
	want, err := domain.NewUnavailableRetentionApproval(3, "deadbeef", 82)
	if err != nil {
		t.Fatal(err)
	}
	if *approval != want {
		t.Fatalf("approval = %+v, want %+v", *approval, want)
	}
}

func TestUnavailableRetentionApprovalForFixSkipsZeroCount(t *testing.T) {
	t.Parallel()
	approval, err := unavailableRetentionApprovalForFix(context.Background(), &offlineMigrationStoreStub{}, "1:x")
	if err != nil {
		t.Fatal(err)
	}
	if approval != nil {
		t.Fatalf("zero-count approval = %+v, want nil", approval)
	}
}
