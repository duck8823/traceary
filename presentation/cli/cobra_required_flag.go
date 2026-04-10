package cli

import (
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"
)

func configureRequiredFlag(cmd *cobra.Command, flagName string) error {
	if err := cmd.MarkFlagRequired(flagName); err != nil {
		return xerrors.Errorf(
			"%s: %w",
			localizef(
				"failed to configure required flag %q",
				"必須 flag %q の設定に失敗しました",
				"--"+strings.TrimSpace(flagName),
			),
			err,
		)
	}

	return nil
}
