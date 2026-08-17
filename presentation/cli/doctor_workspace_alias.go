package cli

import (
	"context"
	"strings"

	"golang.org/x/xerrors"
)

const doctorWorkspaceAliasConflictSampleLimit = 3

func validateDoctorAliasFlags(input doctorCommandInput) error {
	absorb := input.aliasAdd || input.aliasRemove || input.aliasList
	if absorb && (input.fixSet || input.dryRunSet) {
		return xerrors.New(Localize(
			"--fix/--dry-run cannot be combined with --alias-add/--alias-remove/--alias-list",
			"--fix/--dry-run は --alias-add / --alias-remove / --alias-list と同時に使えません",
		))
	}
	if !absorb && (input.sessionSet || input.workspaceSet || input.reviewedBySet || input.noteSet) {
		return xerrors.New(Localize(
			"--session/--workspace/--reviewed-by/--note require --alias-add or --alias-remove",
			"--session / --workspace / --reviewed-by / --note には --alias-add または --alias-remove が必要です",
		))
	}
	if input.aliasList && (input.sessionSet || input.workspaceSet || input.reviewedBySet || input.noteSet) {
		return xerrors.New(Localize(
			"--session/--workspace/--reviewed-by/--note cannot be combined with --alias-list",
			"--session / --workspace / --reviewed-by / --note は --alias-list と同時に使えません",
		))
	}
	if input.aliasAdd {
		if strings.TrimSpace(input.session) == "" || strings.TrimSpace(input.workspace) == "" || strings.TrimSpace(input.reviewedBy) == "" {
			return xerrors.New(Localize(
				"--alias-add requires --session, --workspace, and --reviewed-by",
				"--alias-add には --session、--workspace、--reviewed-by が必要です",
			))
		}
	}
	if input.aliasRemove {
		if input.reviewedBySet || input.noteSet {
			return xerrors.New(Localize(
				"--reviewed-by/--note require --alias-add",
				"--reviewed-by / --note には --alias-add が必要です",
			))
		}
		if strings.TrimSpace(input.session) == "" || strings.TrimSpace(input.workspace) == "" {
			return xerrors.New(Localize(
				"--alias-remove requires --session and --workspace",
				"--alias-remove には --session と --workspace が必要です",
			))
		}
	}
	return nil
}

func skippedWorkspaceAliasesCheck() doctorCheck {
	return doctorCheck{
		Name:   "workspace-aliases",
		Status: doctorStatusSkip,
		Message: Localize(
			"default doctor is filesystem-metadata-only for stores at or above 2 GiB; use report workspace-identity or doctor --alias-list on a reviewed copy",
			"2 GiB 以上の store では default doctor は filesystem metadata のみです。report workspace-identity か、review 済み copy で doctor --alias-list を使ってください",
		),
	}
}

func (c *RootCLI) inspectWorkspaceAliases(ctx context.Context) doctorCheck {
	const name = "workspace-aliases"
	if c.workspaceIdentity == nil {
		return skippedWorkspaceAliasesCheck()
	}
	identity, err := c.workspaceIdentity.Report(ctx, doctorWorkspaceAliasConflictSampleLimit)
	if err != nil {
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusWarn,
			Message: localizef("workspace alias review failed: %v", "workspace alias の確認に失敗しました: %v", err),
		}
	}
	check := doctorCheck{
		Name:   name,
		Status: doctorStatusPass,
		Message: localizef(
			"reviewed aliases=%d; conflict pairs=%d (conflicts are not a store defect)",
			"review 済み alias=%d; conflict pair=%d（conflict は store の欠陥ではない）",
			len(identity.Aliases),
			identity.ConflictPairCount,
		),
	}
	if identity.ConflictPairCount > 0 {
		check.Hint = Localize(
			"review a pair with report workspace-identity, then doctor --alias-add --session <id> --workspace <path> --reviewed-by <operator>",
			"report workspace-identity で pair を確認し、doctor --alias-add --session <id> --workspace <path> --reviewed-by <operator> で登録してください",
		)
		if len(identity.ConflictSamples) > 0 {
			first := identity.ConflictSamples[0]
			check.Message += "; sample=" + first.SessionID + " " + first.Workspace
		}
	}
	return check
}
