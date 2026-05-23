package cli

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/duck8823/traceary/presentation"
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

func setConfiguredCLILanguageForProcess(value string) {
	cliLanguageCache.Lock()
	cliLanguageCache.loaded = true
	cliLanguageCache.value = normalizeCLILanguage(value)
	cliLanguageCache.Unlock()
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
