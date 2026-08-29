package usecase

import (
	"context"
	"strconv"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func hasStableHookDelivery(ctx context.Context) bool {
	input, ok := apptypes.HookDeliveryFromContext(ctx)
	return ok && input.NativeID() != ""
}

func attachHookDelivery(ctx context.Context, event *model.Event, semanticFields ...string) error {
	input, ok := apptypes.HookDeliveryFromContext(ctx)
	if !ok || event == nil {
		return nil
	}
	event.SetRawWorkspace(input.RawWorkspace())
	if input.NativeID() == "" {
		return nil
	}
	evidence, err := model.NewHookDeliveryEvidence(event, input.NativeID(), input.RawWorkspace(), semanticFields...)
	if err != nil {
		return xerrors.Errorf("failed to build hook delivery evidence: %w", err)
	}
	event.SetDeliveryEvidence(evidence)
	return nil
}

func commandAuditDeliveryFields(audit *model.CommandAudit) []string {
	if audit == nil {
		return nil
	}
	exitCode := "none"
	if value, ok := audit.ExitCode().Value(); ok {
		exitCode = strconv.Itoa(value)
	}
	metadataJSON := ""
	if metadata, ok := audit.OutputMetadata().Value(); ok {
		encoded, err := types.EncodeReadOnlyOutputMetadata(metadata)
		if err == nil {
			metadataJSON = encoded
		}
	}
	return []string{
		"audit",
		audit.Command(),
		audit.Input(),
		audit.Output(),
		strconv.FormatBool(audit.InputTruncated()),
		strconv.FormatBool(audit.OutputTruncated()),
		strconv.Itoa(audit.InputOriginalBytes()),
		strconv.Itoa(audit.OutputOriginalBytes()),
		exitCode,
		strconv.FormatBool(audit.Failed()),
		metadataJSON,
	}
}

// CommandAuditDeliveryFieldsForTest exposes fingerprint fields for tests.
func CommandAuditDeliveryFieldsForTest(audit *model.CommandAudit) []string {
	return commandAuditDeliveryFields(audit)
}
