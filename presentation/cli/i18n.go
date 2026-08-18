package cli

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/duck8823/traceary/presentation"
	"golang.org/x/xerrors"
)

const cliLanguageEnvKey = "TRACEARY_LANG"

var cliLanguageCache = struct {
	sync.RWMutex
	loaded bool
	value  string
}{}

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

var cobraUnknownCommandPattern = regexp.MustCompile(`(?s)^unknown command "([^"]+)" for "([^"]+)"(.*)$`)

// LocalizeCobraExecuteError rewrites Cobra's root-level unknown-command
// message through the same catalog as applyStrictGroups. Suggestion lines
// after the first paragraph are kept verbatim.
func LocalizeCobraExecuteError(err error) error {
	if err == nil {
		return nil
	}
	match := cobraUnknownCommandPattern.FindStringSubmatch(err.Error())
	if match == nil {
		return err
	}
	name := match[1]
	path := match[2]
	suffix := match[3]
	localized := Localizef(
		`unknown command %q for %q; run %q for available commands`,
		`%q は %q の不明なコマンドです。利用可能なコマンドは %q を参照してください`,
		name, path, path+" --help",
	)
	if suffix != "" {
		localized += suffix
	}
	return xerrors.Errorf("%s", localized)
}

func isJapaneseCLI() bool {
	value, ok := explicitCLILanguageOverride()
	if !ok {
		value = configuredCLILanguage()
	}

	return strings.HasPrefix(value, "ja")
}

func explicitCLILanguageOverride() (string, bool) {
	value, ok := os.LookupEnv(cliLanguageEnvKey)
	if !ok {
		return "", false
	}
	return strings.ToLower(strings.TrimSpace(value)), true
}

func configuredCLILanguage() string {
	cliLanguageCache.RLock()
	if cliLanguageCache.loaded {
		value := cliLanguageCache.value
		cliLanguageCache.RUnlock()
		return value
	}
	cliLanguageCache.RUnlock()

	cfg := presentation.LoadConfig()
	value := normalizeCLILanguage(cfg.UILanguage)

	cliLanguageCache.Lock()
	cliLanguageCache.loaded = true
	cliLanguageCache.value = value
	cliLanguageCache.Unlock()
	return value
}

func resetConfiguredCLILanguageCacheForTest() {
	cliLanguageCache.Lock()
	cliLanguageCache.loaded = false
	cliLanguageCache.value = ""
	cliLanguageCache.Unlock()
}

func normalizeCLILanguage(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
