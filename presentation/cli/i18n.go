package cli

import (
	"fmt"
	"os"
	"strings"
)

const cliLanguageEnvKey = "TRACEARY_LANG"

// Localize returns the English or Japanese string for the active CLI locale.
func Localize(english string, japanese string) string {
	if isJapaneseCLI() {
		return japanese
	}

	return english
}

// Localizef formats the English or Japanese string for the active CLI locale.
func Localizef(english string, japanese string, args ...any) string {
	return fmt.Sprintf(Localize(english, japanese), args...)
}

func localizef(english string, japanese string, args ...any) string {
	return Localizef(english, japanese, args...)
}

func isJapaneseCLI() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(cliLanguageEnvKey)))

	return strings.HasPrefix(value, "ja")
}
