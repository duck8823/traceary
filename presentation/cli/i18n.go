package cli

import (
	"fmt"
	"os"
	"strings"
)

const cliLanguageEnvKey = "TRACEARY_LANG"

// Localize は CLI の表示言語に応じて英語または日本語の文字列を返します。
func Localize(english string, japanese string) string {
	if isJapaneseCLI() {
		return japanese
	}

	return english
}

// Localizef は CLI の表示言語に応じて英語または日本語のフォーマット文字列を整形します。
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
