package cli

import (
	"github.com/spf13/cobra"
	"golang.org/x/xerrors"
)

func noArgsJP() cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) == 0 {
			return nil
		}

		return xerrors.Errorf(localizef(
			"this command does not accept positional arguments (received: %d)",
			"引数は不要です (受け取った引数数: %d)",
			len(args),
		))
	}
}

func exactArgsJP(expected int) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) == expected {
			return nil
		}

		return xerrors.Errorf(localizef(
			"expected exactly %d positional argument(s) (received: %d)",
			"引数はちょうど %d 個必要です (受け取った引数数: %d)",
			expected,
			len(args),
		))
	}
}

func maximumNArgsJP(maxArgs int) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) <= maxArgs {
			return nil
		}

		return xerrors.Errorf(localizef(
			"expected at most %d positional argument(s) (received: %d)",
			"引数は最大 %d 個まで指定できます (受け取った引数数: %d)",
			maxArgs,
			len(args),
		))
	}
}
