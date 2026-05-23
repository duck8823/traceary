package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/presentation"
)

const (
	cockpitSettingsConfigMissing    = "missing"
	cockpitSettingsConfigLoaded     = "loaded"
	cockpitSettingsConfigInvalid    = "invalid"
	cockpitSettingsConfigUnreadable = "unreadable"
	cockpitSettingsConfigPathError  = "path_error"
)

type cockpitSettingsState struct {
	loaded         bool
	snapshot       cockpitSettingsSnapshot
	draft          cockpitSettingsValues
	cursor         int
	confirmSave    bool
	editingPattern bool
	patternInput   string
	info           string
	err            string
}

type cockpitSettingsSnapshot struct {
	Path   string
	Status string
	Error  string
	Values cockpitSettingsValues
	Env    cockpitSettingsEnv
}

type cockpitSettingsEnv struct {
	TracearyLang         string
	TracearyLangSet      bool
	TracearyDBPath       string
	TracearyDBPathSet    bool
	TracearyWorkspace    string
	TracearyWorkspaceSet bool
	TracearyClient       string
	TracearyClientSet    bool
	TracearyAgent        string
	TracearyAgentSet     bool
	TracearySessionID    string
	TracearySessionIDSet bool
	HookStateDir         string
	HookStateDirSet      bool
	HookStateKey         string
	HookStateKeySet      bool
	HookDebug            string
	HookDebugSet         bool
}

type cockpitSettingsValues struct {
	UILanguage      string
	ReadColor       string
	ReadFields      []string
	ReadPresets     []cockpitSettingsPresetSummary
	ExtraPatterns   []string
	RedactRuleCount int
	RedactRuleNames []string
}

type cockpitSettingsPresetSummary struct {
	Name    string
	Fields  []string
	Filters []string
}

type cockpitSettingsConfigFile struct {
	UI     cockpitSettingsUISection     `json:"ui"`
	Read   cockpitSettingsReadSection   `json:"read"`
	Redact cockpitSettingsRedactSection `json:"redact"`
}

type cockpitSettingsUISection struct {
	Language string `json:"language"`
}

type cockpitSettingsReadSection struct {
	Fields  []string                                `json:"fields"`
	Color   string                                  `json:"color"`
	Presets map[string]cockpitSettingsReadPresetDoc `json:"presets"`
}

type cockpitSettingsReadPresetDoc struct {
	Fields  []string                        `json:"fields"`
	Filters cockpitSettingsReadPresetFilter `json:"filters"`
}

type cockpitSettingsReadPresetFilter struct {
	Kind      string `json:"kind"`
	Failures  *bool  `json:"failures"`
	Workspace string `json:"workspace"`
	SessionID string `json:"session_id"`
	Client    string `json:"client"`
	Agent     string `json:"agent"`
}

type cockpitSettingsRedactSection struct {
	ExtraPatterns []string                        `json:"extra_patterns"`
	Rules         []cockpitSettingsRedactRuleView `json:"rules"`
}

type cockpitSettingsRedactRuleView struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

func (m cockpitModel) openCockpitSettings() (tea.Model, tea.Cmd) {
	m.mode = cockpitModeSettings
	m.showHelp = false
	if !m.settings.loaded {
		m.settings = newCockpitSettingsState()
	}
	return m, nil
}

func newCockpitSettingsState() cockpitSettingsState {
	snapshot := loadCockpitSettingsSnapshot()
	return cockpitSettingsState{
		loaded:   true,
		snapshot: snapshot,
		draft:    snapshot.Values.clone(),
	}
}

func loadCockpitSettingsSnapshot() cockpitSettingsSnapshot {
	path, err := presentation.DefaultConfigPath()
	snapshot := cockpitSettingsSnapshot{Env: readCockpitSettingsEnv()}
	if err != nil {
		snapshot.Status = cockpitSettingsConfigPathError
		snapshot.Error = err.Error()
		return snapshot
	}
	snapshot.Path = path
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			snapshot.Status = cockpitSettingsConfigMissing
			return snapshot
		}
		snapshot.Status = cockpitSettingsConfigUnreadable
		snapshot.Error = err.Error()
		return snapshot
	}
	var file cockpitSettingsConfigFile
	if err := json.Unmarshal(data, &file); err != nil {
		snapshot.Status = cockpitSettingsConfigInvalid
		snapshot.Error = err.Error()
		return snapshot
	}
	snapshot.Status = cockpitSettingsConfigLoaded
	snapshot.Values = cockpitSettingsValues{
		UILanguage:      strings.TrimSpace(file.UI.Language),
		ReadColor:       strings.TrimSpace(file.Read.Color),
		ReadFields:      slices.Clone(file.Read.Fields),
		ReadPresets:     summarizeCockpitSettingsPresets(file.Read.Presets),
		ExtraPatterns:   slices.Clone(file.Redact.ExtraPatterns),
		RedactRuleCount: len(file.Redact.Rules),
		RedactRuleNames: summarizeCockpitSettingsRedactRules(file.Redact.Rules),
	}
	return snapshot
}

func readCockpitSettingsEnv() cockpitSettingsEnv {
	return cockpitSettingsEnv{
		TracearyLang:         envValue(cliLanguageEnvKey),
		TracearyLangSet:      envSet(cliLanguageEnvKey),
		TracearyDBPath:       envValue("TRACEARY_DB_PATH"),
		TracearyDBPathSet:    envSet("TRACEARY_DB_PATH"),
		TracearyWorkspace:    envValue("TRACEARY_WORKSPACE"),
		TracearyWorkspaceSet: envSet("TRACEARY_WORKSPACE"),
		TracearyClient:       envValue("TRACEARY_CLIENT"),
		TracearyClientSet:    envSet("TRACEARY_CLIENT"),
		TracearyAgent:        envValue("TRACEARY_AGENT"),
		TracearyAgentSet:     envSet("TRACEARY_AGENT"),
		TracearySessionID:    envValue("TRACEARY_SESSION_ID"),
		TracearySessionIDSet: envSet("TRACEARY_SESSION_ID"),
		HookStateDir:         envValue("TRACEARY_HOOK_STATE_DIR"),
		HookStateDirSet:      envSet("TRACEARY_HOOK_STATE_DIR"),
		HookStateKey:         envValue("TRACEARY_HOOK_STATE_KEY"),
		HookStateKeySet:      envSet("TRACEARY_HOOK_STATE_KEY"),
		HookDebug:            envValue("TRACEARY_HOOK_DEBUG"),
		HookDebugSet:         envSet("TRACEARY_HOOK_DEBUG"),
	}
}

func envValue(name string) string {
	value, _ := os.LookupEnv(name)
	return value
}

func envSet(name string) bool {
	_, ok := os.LookupEnv(name)
	return ok
}

func summarizeCockpitSettingsPresets(raw map[string]cockpitSettingsReadPresetDoc) []cockpitSettingsPresetSummary {
	if len(raw) == 0 {
		return nil
	}
	names := make([]string, 0, len(raw))
	for name := range raw {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]cockpitSettingsPresetSummary, 0, len(names))
	for _, name := range names {
		doc := raw[name]
		out = append(out, cockpitSettingsPresetSummary{Name: name, Fields: slices.Clone(doc.Fields), Filters: summarizeCockpitSettingsPresetFilters(doc.Filters)})
	}
	return out
}

func summarizeCockpitSettingsPresetFilters(filters cockpitSettingsReadPresetFilter) []string {
	out := []string{}
	if strings.TrimSpace(filters.Kind) != "" {
		out = append(out, "kind="+strings.TrimSpace(filters.Kind))
	}
	if filters.Failures != nil {
		out = append(out, fmt.Sprintf("failures=%t", *filters.Failures))
	}
	if strings.TrimSpace(filters.Workspace) != "" {
		out = append(out, "workspace="+strings.TrimSpace(filters.Workspace))
	}
	if strings.TrimSpace(filters.SessionID) != "" {
		out = append(out, "session_id="+strings.TrimSpace(filters.SessionID))
	}
	if strings.TrimSpace(filters.Client) != "" {
		out = append(out, "client="+strings.TrimSpace(filters.Client))
	}
	if strings.TrimSpace(filters.Agent) != "" {
		out = append(out, "agent="+strings.TrimSpace(filters.Agent))
	}
	return out
}

func summarizeCockpitSettingsRedactRules(rules []cockpitSettingsRedactRuleView) []string {
	out := make([]string, 0, len(rules))
	for i, rule := range rules {
		name := strings.TrimSpace(rule.Name)
		if name == "" {
			name = fmt.Sprintf("rule-%d", i+1)
		}
		kind := strings.TrimSpace(rule.Type)
		if kind == "" {
			kind = "regex"
		}
		out = append(out, fmt.Sprintf("%s(%s)", name, kind))
	}
	return out
}

func (v cockpitSettingsValues) clone() cockpitSettingsValues {
	out := v
	out.ReadFields = slices.Clone(v.ReadFields)
	out.ExtraPatterns = slices.Clone(v.ExtraPatterns)
	out.RedactRuleNames = slices.Clone(v.RedactRuleNames)
	out.ReadPresets = slices.Clone(v.ReadPresets)
	for i := range out.ReadPresets {
		out.ReadPresets[i].Fields = slices.Clone(out.ReadPresets[i].Fields)
		out.ReadPresets[i].Filters = slices.Clone(out.ReadPresets[i].Filters)
	}
	return out
}

func (m cockpitModel) updateSettingsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.settings.editingPattern {
		return m.updateSettingsPatternInputKey(msg)
	}
	if m.settings.confirmSave {
		return m.updateSettingsConfirmKey(msg)
	}
	if isCockpitQuitKey(msg) {
		return m, tea.Quit
	}
	if key.Matches(msg, m.keys.Help) {
		m.showHelp = !m.showHelp
		return m, nil
	}
	if isCockpitBackKey(msg) {
		return m.backCockpitSection()
	}
	if section, ok := cockpitSectionFromKey(msg); ok {
		return m.openCockpitSection(section)
	}
	if section, ok := m.cockpitAdjacentSectionFromKey(msg); ok {
		return m.openCockpitSection(section)
	}
	switch {
	case key.Matches(msg, m.keys.Up):
		m.settings.cursor--
		m.clampSettingsCursor()
		return m, nil
	case key.Matches(msg, m.keys.Down):
		m.settings.cursor++
		m.clampSettingsCursor()
		return m, nil
	case key.Matches(msg, m.keys.Refresh):
		m.settings = newCockpitSettingsState()
		m.settings.info = Localize("Settings reloaded from disk.", "Settings を disk から再読み込みしました。")
		return m, nil
	}
	if msg.Type != tea.KeyRunes || len(msg.Runes) == 0 {
		return m, nil
	}
	switch strings.ToLower(string(msg.Runes)) {
	case "l":
		return m.stageSettingsLanguageToggle()
	case "c":
		return m.stageSettingsColorCycle()
	case "f":
		return m.stageSettingsFieldsCycle()
	case "+", "n":
		return m.startSettingsPatternInput()
	case "-", "x":
		return m.stageSettingsPatternRemove()
	case "w":
		return m.startSettingsSaveConfirmation()
	case "d":
		m.settings.draft = m.settings.snapshot.Values.clone()
		m.settings.info = Localize("Discarded pending settings changes.", "未保存の設定変更を破棄しました。")
		m.settings.err = ""
		return m, nil
	}
	return m, nil
}

func (m cockpitModel) updateSettingsPatternInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyCtrlC {
		return m, tea.Quit
	}
	switch msg.Type {
	case tea.KeyEsc:
		m.settings.editingPattern = false
		m.settings.patternInput = ""
		m.settings.info = Localize("Cancelled redaction pattern input.", "redaction pattern 入力をキャンセルしました。")
		m.settings.err = ""
		return m, nil
	case tea.KeyEnter:
		pattern := strings.TrimSpace(m.settings.patternInput)
		if pattern == "" {
			m.settings.err = Localize("Redaction pattern cannot be empty.", "redaction pattern は空にできません。")
			return m, nil
		}
		if _, err := regexp.Compile(pattern); err != nil {
			m.settings.err = Localizef("Invalid regex: %v", "regex が不正です: %v", err)
			return m, nil
		}
		m.settings.draft.ExtraPatterns = append(m.settings.draft.ExtraPatterns, pattern)
		m.settings.editingPattern = false
		m.settings.patternInput = ""
		m.settings.info = Localize("Staged redaction pattern. Press w to review and save.", "redaction pattern を staged にしました。w で確認して保存します。")
		m.settings.err = ""
		return m, nil
	case tea.KeyBackspace, tea.KeyDelete:
		runes := []rune(m.settings.patternInput)
		if len(runes) > 0 {
			m.settings.patternInput = string(runes[:len(runes)-1])
		}
		return m, nil
	case tea.KeySpace:
		m.settings.patternInput += " "
		return m, nil
	case tea.KeyRunes:
		m.settings.patternInput += string(msg.Runes)
		return m, nil
	}
	return m, nil
}

func (m cockpitModel) updateSettingsConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyEsc {
		m.settings.confirmSave = false
		m.settings.info = Localize("Save confirmation cancelled.", "保存確認をキャンセルしました。")
		return m, nil
	}
	if isCockpitQuitKey(msg) {
		return m, tea.Quit
	}
	if msg.Type != tea.KeyRunes || len(msg.Runes) == 0 {
		return m, nil
	}
	switch strings.ToLower(string(msg.Runes)) {
	case "y":
		return m.applySettingsSave()
	case "n":
		m.settings.confirmSave = false
		m.settings.info = Localize("Save confirmation cancelled.", "保存確認をキャンセルしました。")
		return m, nil
	}
	return m, nil
}

func (m cockpitModel) stageSettingsLanguageToggle() (tea.Model, tea.Cmd) {
	if !m.settings.canEditConfig() {
		m.settings.err = Localize("Fix config readability/JSON before editing settings.", "設定を編集する前に config の読み込み/JSON を修復してください。")
		return m, nil
	}
	current := normalizeSettingsLanguage(m.settings.draft.UILanguage)
	if current == "ja" {
		m.settings.draft.UILanguage = "en"
	} else {
		m.settings.draft.UILanguage = "ja"
	}
	m.settings.info = Localize("Staged UI language change. Press w to review and save.", "UI language 変更を staged にしました。w で確認して保存します。")
	m.settings.err = ""
	return m, nil
}

func (m cockpitModel) stageSettingsColorCycle() (tea.Model, tea.Cmd) {
	if !m.settings.canEditConfig() {
		m.settings.err = Localize("Fix config readability/JSON before editing settings.", "設定を編集する前に config の読み込み/JSON を修復してください。")
		return m, nil
	}
	m.settings.draft.ReadColor = nextSettingsColor(m.settings.draft.ReadColor)
	m.settings.info = Localize("Staged read.color change. Press w to review and save.", "read.color 変更を staged にしました。w で確認して保存します。")
	m.settings.err = ""
	return m, nil
}

func (m cockpitModel) stageSettingsFieldsCycle() (tea.Model, tea.Cmd) {
	if !m.settings.canEditConfig() {
		m.settings.err = Localize("Fix config readability/JSON before editing settings.", "設定を編集する前に config の読み込み/JSON を修復してください。")
		return m, nil
	}
	m.settings.draft.ReadFields = nextSettingsFieldSet(m.settings.draft.ReadFields)
	m.settings.info = Localize("Staged read.fields change. Press w to review and save.", "read.fields 変更を staged にしました。w で確認して保存します。")
	m.settings.err = ""
	return m, nil
}

func (m cockpitModel) startSettingsPatternInput() (tea.Model, tea.Cmd) {
	if !m.settings.canEditConfig() {
		m.settings.err = Localize("Fix config readability/JSON before editing settings.", "設定を編集する前に config の読み込み/JSON を修復してください。")
		return m, nil
	}
	m.settings.editingPattern = true
	m.settings.patternInput = ""
	m.settings.info = Localize("Enter a Go regexp for redact.extra_patterns; enter stages, esc cancels.", "redact.extra_patterns に追加する Go regexp を入力。enter で staged、esc でキャンセル。")
	m.settings.err = ""
	return m, nil
}

func (m cockpitModel) stageSettingsPatternRemove() (tea.Model, tea.Cmd) {
	if !m.settings.canEditConfig() {
		m.settings.err = Localize("Fix config readability/JSON before editing settings.", "設定を編集する前に config の読み込み/JSON を修復してください。")
		return m, nil
	}
	if len(m.settings.draft.ExtraPatterns) == 0 {
		m.settings.err = Localize("No redact.extra_patterns entry to remove.", "削除できる redact.extra_patterns はありません。")
		return m, nil
	}
	removed := m.settings.draft.ExtraPatterns[len(m.settings.draft.ExtraPatterns)-1]
	m.settings.draft.ExtraPatterns = m.settings.draft.ExtraPatterns[:len(m.settings.draft.ExtraPatterns)-1]
	m.settings.info = Localizef("Staged removal of redact.extra_patterns entry %q. Press w to review and save.", "redact.extra_patterns %q の削除を staged にしました。w で確認して保存します。", removed)
	m.settings.err = ""
	return m, nil
}

func (m cockpitModel) startSettingsSaveConfirmation() (tea.Model, tea.Cmd) {
	if !m.settings.canEditConfig() {
		m.settings.err = Localize("Fix config readability/JSON before saving settings.", "設定を保存する前に config の読み込み/JSON を修復してください。")
		return m, nil
	}
	if !m.settings.dirty() {
		m.settings.err = Localize("No pending settings changes to save.", "保存する未反映の設定変更はありません。")
		return m, nil
	}
	if err := validateCockpitSettingsDraft(m.settings.draft); err != nil {
		m.settings.err = err.Error()
		return m, nil
	}
	m.settings.confirmSave = true
	m.settings.info = Localize("Review the pending config diff, then press y to save or n/esc to cancel.", "未保存 config diff を確認し、y で保存 / n・esc でキャンセルします。")
	m.settings.err = ""
	return m, nil
}

func (m cockpitModel) applySettingsSave() (tea.Model, tea.Cmd) {
	if err := saveCockpitSettingsDraft(m.settings.snapshot, m.settings.draft); err != nil {
		m.settings.err = err.Error()
		m.settings.confirmSave = false
		return m, nil
	}
	if !m.settings.snapshot.Env.TracearyLangSet {
		setConfiguredCLILanguageForProcess(m.settings.draft.UILanguage)
	}
	m.settings = newCockpitSettingsState()
	m.settings.info = Localize("Settings saved. Current cockpit language refreshed where no TRACEARY_LANG override is set.", "Settings を保存しました。TRACEARY_LANG override がない場合は現在の cockpit language も更新しました。")
	return m, nil
}

func (m cockpitModel) settingsView() string {
	if !m.settings.loaded {
		m.settings = newCockpitSettingsState()
	}
	lines := []string{
		m.styles.Subtle.Render(Localize("Config-backed settings", "config backed settings")),
		Localize("config path: ", "config path: ") + formatOptionalColumn(m.settings.snapshot.Path),
		Localize("config status: ", "config status: ") + m.settings.configStatusLabel(),
	}
	if m.settings.snapshot.Error != "" {
		lines = append(lines, m.styles.Error.Render(Localize("config error: ", "config error: ")+m.settings.snapshot.Error))
	}
	if m.settings.info != "" {
		lines = append(lines, m.styles.Success.Render("• "+m.settings.info))
	}
	if m.settings.err != "" {
		lines = append(lines, m.styles.Error.Render("• "+m.settings.err))
	}
	if m.settings.snapshot.Env.TracearyLangSet {
		lines = append(lines, m.styles.Warning.Render(Localizef("TRACEARY_LANG=%s overrides ui.language until the env var is unset.", "TRACEARY_LANG=%s が設定されているため、解除するまで ui.language より優先されます。", m.settings.snapshot.Env.TracearyLang)))
	}
	if !m.settings.canEditConfig() {
		lines = append(lines, m.styles.Warning.Render(Localize("Config edits are disabled to avoid silently overwriting invalid or unreadable JSON.", "invalid / unreadable JSON を黙って上書きしないため、config 編集は無効です。")))
	}
	lines = append(lines, "", m.styles.Subtle.Render(Localize("Editable settings", "編集可能な settings")))
	for i, row := range m.settings.settingsRows() {
		prefix := "  "
		if i == m.settings.cursor {
			prefix = "> "
		}
		lines = append(lines, prefix+row)
	}
	lines = append(lines, "", m.styles.Subtle.Render(Localize("Read-only diagnostics", "読み取り専用 diagnostics")))
	lines = append(lines, m.settings.settingsEnvLines()...)
	if len(m.settings.draft.ReadPresets) > 0 {
		lines = append(lines, "", m.styles.Subtle.Render(Localize("read.presets (view only in v0.18)", "read.presets (v0.18 では表示のみ)")))
		for _, preset := range m.settings.draft.ReadPresets {
			fields := strings.Join(preset.Fields, ",")
			if fields == "" {
				fields = Localize("default fields", "default fields")
			}
			filters := strings.Join(preset.Filters, ",")
			if filters == "" {
				filters = Localize("no filters", "filter なし")
			}
			lines = append(lines, fmt.Sprintf("• %s fields=%s filters=%s", preset.Name, fields, filters))
		}
	}
	if m.settings.draft.RedactRuleCount > 0 {
		lines = append(lines, "", m.styles.Subtle.Render(Localize("redact.rules (view only in v0.18)", "redact.rules (v0.18 では表示のみ)")))
		for _, name := range m.settings.draft.RedactRuleNames {
			lines = append(lines, "• "+name)
		}
	}
	if m.settings.editingPattern {
		lines = append(lines, "", m.styles.Active.Render(Localize("Add redact.extra_patterns regex:", "redact.extra_patterns regex を追加:")), m.settings.patternInput)
	}
	if m.settings.confirmSave {
		lines = append(lines, "", m.styles.Warning.Render(Localize("Confirm config write", "config 書き込み確認")))
		lines = append(lines, m.settingsDiffLines()...)
		lines = append(lines, m.styles.Help.Render(Localize("y save atomically · n/esc cancel", "y atomic save · n/esc キャンセル")))
	} else if m.settings.dirty() {
		lines = append(lines, "", m.styles.Warning.Render(Localize("Pending changes are not written yet. Press w to review and save, d to discard.", "未保存の変更があります。w で確認して保存、d で破棄します。")))
		lines = append(lines, m.settingsDiffLines()...)
	}
	return m.renderCockpitShell(Localize("settings", "設定"), lines, m.settingsLocalHelp())
}

func (s cockpitSettingsState) settingsRows() []string {
	return []string{
		Localize("ui.language: ", "ui.language: ") + formatSettingValue(effectiveSettingsLanguage(s.draft.UILanguage)) + s.languageDirtyMarker(),
		Localize("read.color: ", "read.color: ") + formatSettingValue(effectiveSettingsColor(s.draft.ReadColor)) + s.settingDirtyMarker(s.snapshot.Values.ReadColor, s.draft.ReadColor),
		Localize("read.fields: ", "read.fields: ") + formatSettingValue(formatSettingsFields(s.draft.ReadFields)) + s.settingDirtyMarker(strings.Join(s.snapshot.Values.ReadFields, ","), strings.Join(s.draft.ReadFields, ",")),
		Localize("redact.extra_patterns: ", "redact.extra_patterns: ") + formatSettingValue(formatSettingsPatterns(s.draft.ExtraPatterns)) + s.settingDirtyMarker(strings.Join(s.snapshot.Values.ExtraPatterns, "\x00"), strings.Join(s.draft.ExtraPatterns, "\x00")),
	}
}

func (s cockpitSettingsState) settingsEnvLines() []string {
	env := s.snapshot.Env
	return []string{
		formatSettingsEnvLine(cliLanguageEnvKey, env.TracearyLang, env.TracearyLangSet),
		formatSettingsEnvLine("TRACEARY_DB_PATH", env.TracearyDBPath, env.TracearyDBPathSet),
		formatSettingsEnvLine("TRACEARY_WORKSPACE", env.TracearyWorkspace, env.TracearyWorkspaceSet),
		formatSettingsEnvLine("TRACEARY_CLIENT", env.TracearyClient, env.TracearyClientSet),
		formatSettingsEnvLine("TRACEARY_AGENT", env.TracearyAgent, env.TracearyAgentSet),
		formatSettingsEnvLine("TRACEARY_SESSION_ID", env.TracearySessionID, env.TracearySessionIDSet),
		formatSettingsEnvLine("TRACEARY_HOOK_STATE_DIR", env.HookStateDir, env.HookStateDirSet),
		formatSettingsEnvLine("TRACEARY_HOOK_STATE_KEY", env.HookStateKey, env.HookStateKeySet),
		formatSettingsEnvLine("TRACEARY_HOOK_DEBUG", env.HookDebug, env.HookDebugSet),
	}
}

func (s cockpitSettingsState) configStatusLabel() string {
	switch s.snapshot.Status {
	case cockpitSettingsConfigMissing:
		return Localize("missing (will be created on save)", "missing (保存時に作成)")
	case cockpitSettingsConfigLoaded:
		return Localize("loaded", "loaded")
	case cockpitSettingsConfigInvalid:
		return Localize("invalid JSON", "invalid JSON")
	case cockpitSettingsConfigUnreadable:
		return Localize("unreadable", "unreadable")
	case cockpitSettingsConfigPathError:
		return Localize("path error", "path error")
	default:
		return s.snapshot.Status
	}
}

func (s cockpitSettingsState) canEditConfig() bool {
	return s.snapshot.Status == cockpitSettingsConfigMissing || s.snapshot.Status == cockpitSettingsConfigLoaded
}

func (s cockpitSettingsState) dirty() bool {
	return normalizeSettingsLanguage(s.snapshot.Values.UILanguage) != normalizeSettingsLanguage(s.draft.UILanguage) ||
		strings.TrimSpace(s.snapshot.Values.ReadColor) != strings.TrimSpace(s.draft.ReadColor) ||
		!slices.Equal(s.snapshot.Values.ReadFields, s.draft.ReadFields) ||
		!slices.Equal(s.snapshot.Values.ExtraPatterns, s.draft.ExtraPatterns)
}

func (s cockpitSettingsState) settingDirtyMarker(oldValue string, newValue string) string {
	if strings.TrimSpace(oldValue) == strings.TrimSpace(newValue) {
		return ""
	}
	return Localize("  [staged]", "  [staged]")
}

func (s cockpitSettingsState) languageDirtyMarker() string {
	if normalizeSettingsLanguage(s.snapshot.Values.UILanguage) == normalizeSettingsLanguage(s.draft.UILanguage) {
		return ""
	}
	return Localize("  [staged]", "  [staged]")
}

func (m cockpitModel) settingsDiffLines() []string {
	old := m.settings.snapshot.Values
	draft := m.settings.draft
	lines := []string{}
	appendDiff := func(label string, before string, after string) {
		if strings.TrimSpace(before) == strings.TrimSpace(after) {
			return
		}
		lines = append(lines, fmt.Sprintf("• %s: %s -> %s", label, formatSettingValue(before), formatSettingValue(after)))
	}
	appendDiff("ui.language", effectiveSettingsLanguage(old.UILanguage), effectiveSettingsLanguage(draft.UILanguage))
	appendDiff("read.color", effectiveSettingsColor(old.ReadColor), effectiveSettingsColor(draft.ReadColor))
	appendDiff("read.fields", formatSettingsFields(old.ReadFields), formatSettingsFields(draft.ReadFields))
	appendDiff("redact.extra_patterns", formatSettingsPatterns(old.ExtraPatterns), formatSettingsPatterns(draft.ExtraPatterns))
	if len(lines) == 0 {
		return []string{Localize("• no effective diff", "• 有効な差分はありません")}
	}
	return lines
}

func (m *cockpitModel) clampSettingsCursor() {
	maxIndex := len(m.settings.settingsRows()) - 1
	if maxIndex < 0 {
		maxIndex = 0
	}
	if m.settings.cursor < 0 {
		m.settings.cursor = 0
	}
	if m.settings.cursor > maxIndex {
		m.settings.cursor = maxIndex
	}
}

func (m cockpitModel) settingsLocalHelp() string {
	if m.settings.editingPattern {
		return Localize("enter stage regex · esc cancel · backspace edit", "enter regex を staged · esc キャンセル · backspace 編集")
	}
	if m.settings.confirmSave {
		return Localize("y save · n/esc cancel", "y 保存 · n/esc キャンセル")
	}
	return Localize("↑/↓ select · l language · c color · f fields · n/+ add regex · x/- remove regex · w save · d discard · r reload", "↑/↓ 選択 · l language · c color · f fields · n/+ regex 追加 · x/- regex 削除 · w 保存 · d 破棄 · r 再読込")
}

func (m cockpitModel) settingsContextualActions() []cockpitAction {
	if m.settings.editingPattern {
		return []cockpitAction{
			{key: "enter", description: Localize("Validate regex and stage redact.extra_patterns addition", "regex を検証して redact.extra_patterns 追加を staged")},
			{key: "esc", description: Localize("Cancel regex input", "regex 入力をキャンセル")},
		}
	}
	if m.settings.confirmSave {
		return []cockpitAction{
			{key: "y", description: Localize("Write config atomically", "config を atomic に書き込み")},
			{key: "n / esc", description: Localize("Cancel config write", "config 書き込みをキャンセル")},
		}
	}
	return []cockpitAction{
		{key: "l", description: Localize("Toggle ui.language between en and ja", "ui.language を en / ja で切替")},
		{key: "c", description: Localize("Cycle read.color auto / always / never", "read.color を auto / always / never で切替")},
		{key: "f", description: Localize("Cycle safe read.fields presets", "安全な read.fields preset を切替")},
		{key: "n / +", description: Localize("Add a regex to redact.extra_patterns after validation", "検証後に regex を redact.extra_patterns へ追加")},
		{key: "x / -", description: Localize("Remove the last redact.extra_patterns entry", "最後の redact.extra_patterns を削除")},
		{key: "w", description: Localize("Review diff and confirm before writing config", "diff を確認してから config 書き込み")},
		{key: "d", description: Localize("Discard pending settings changes", "未保存の settings 変更を破棄")},
		{key: "r", description: Localize("Reload settings from config file", "config file から settings を再読込")},
	}
}

func validateCockpitSettingsDraft(values cockpitSettingsValues) error {
	lang := normalizeSettingsLanguage(values.UILanguage)
	if lang != "" && lang != "en" && lang != "ja" {
		return xerrors.Errorf(Localize("ui.language must be en or ja", "ui.language は en または ja にしてください"))
	}
	if _, err := validateColorValue(values.ReadColor); err != nil {
		return err
	}
	if len(values.ReadFields) > 0 {
		if _, err := parseReadFields(values.ReadFields); err != nil {
			return err
		}
	}
	for _, pattern := range values.ExtraPatterns {
		trimmed := strings.TrimSpace(pattern)
		if trimmed == "" {
			return xerrors.Errorf(Localize("redact.extra_patterns cannot contain an empty regex", "redact.extra_patterns に空の regex は保存できません"))
		}
		if _, err := regexp.Compile(trimmed); err != nil {
			return xerrors.Errorf(Localizef("invalid redact.extra_patterns regex %q: %v", "redact.extra_patterns regex %q が不正です: %v", pattern, err))
		}
	}
	return nil
}

func saveCockpitSettingsDraft(snapshot cockpitSettingsSnapshot, values cockpitSettingsValues) error {
	if snapshot.Path == "" {
		return xerrors.Errorf(Localize("config path is unavailable", "config path を利用できません"))
	}
	if err := validateCockpitSettingsDraft(values); err != nil {
		return err
	}
	raw := map[string]json.RawMessage{}
	data, err := os.ReadFile(snapshot.Path)
	if err != nil {
		if !os.IsNotExist(err) {
			return xerrors.Errorf(Localizef("failed to read config before saving: %v", "保存前の config 読み込みに失敗しました: %v", err))
		}
	} else if len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, &raw); err != nil {
			return xerrors.Errorf(Localizef("config JSON is invalid; refusing to overwrite: %v", "config JSON が不正なため上書きを拒否します: %v", err))
		}
	}
	if raw == nil {
		raw = map[string]json.RawMessage{}
	}
	if normalizeSettingsLanguage(snapshot.Values.UILanguage) != normalizeSettingsLanguage(values.UILanguage) {
		if err := updateCockpitSettingsSection(raw, "ui", map[string]any{"language": normalizeSettingsLanguage(values.UILanguage)}); err != nil {
			return err
		}
	}
	readUpdates := map[string]any{}
	if strings.TrimSpace(snapshot.Values.ReadColor) != strings.TrimSpace(values.ReadColor) {
		readUpdates["color"] = strings.TrimSpace(values.ReadColor)
	}
	if !slices.Equal(snapshot.Values.ReadFields, values.ReadFields) {
		readUpdates["fields"] = values.ReadFields
	}
	if len(readUpdates) > 0 {
		if err := updateCockpitSettingsSection(raw, "read", readUpdates); err != nil {
			return err
		}
	}
	if !slices.Equal(snapshot.Values.ExtraPatterns, values.ExtraPatterns) {
		if err := updateCockpitSettingsSection(raw, "redact", map[string]any{"extra_patterns": values.ExtraPatterns}); err != nil {
			return err
		}
	}
	encoded, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return xerrors.Errorf(Localizef("failed to encode config JSON: %v", "config JSON の encode に失敗しました: %v", err))
	}
	encoded = append(encoded, '\n')
	return writeCockpitSettingsConfigAtomically(snapshot.Path, encoded)
}

func updateCockpitSettingsSection(raw map[string]json.RawMessage, section string, updates map[string]any) error {
	sectionMap := map[string]any{}
	if existing, ok := raw[section]; ok && len(bytes.TrimSpace(existing)) > 0 {
		if err := json.Unmarshal(existing, &sectionMap); err != nil {
			return xerrors.Errorf(Localizef("config section %s is invalid; refusing to overwrite: %v", "config section %s が不正なため上書きを拒否します: %v", section, err))
		}
		if sectionMap == nil {
			sectionMap = map[string]any{}
		}
	}
	for key, value := range updates {
		sectionMap[key] = value
	}
	encoded, err := json.Marshal(sectionMap)
	if err != nil {
		return xerrors.Errorf(Localizef("failed to encode config section %s: %v", "config section %s の encode に失敗しました: %v", section, err))
	}
	raw[section] = encoded
	return nil
}

func writeCockpitSettingsConfigAtomically(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return xerrors.Errorf(Localizef("failed to create config directory: %v", "config directory の作成に失敗しました: %v", err))
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config.json.tmp-*")
	if err != nil {
		return xerrors.Errorf(Localizef("failed to create temp config file: %v", "一時 config file の作成に失敗しました: %v", err))
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return xerrors.Errorf(Localizef("failed to write temp config file: %v", "一時 config file の書き込みに失敗しました: %v", err))
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return xerrors.Errorf(Localizef("failed to chmod temp config file: %v", "一時 config file の chmod に失敗しました: %v", err))
	}
	if err := tmp.Close(); err != nil {
		return xerrors.Errorf(Localizef("failed to close temp config file: %v", "一時 config file の close に失敗しました: %v", err))
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return xerrors.Errorf(Localizef("failed to replace config atomically: %v", "config の atomic replace に失敗しました: %v", err))
	}
	return nil
}

func normalizeSettingsLanguage(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if strings.HasPrefix(value, "ja") {
		return "ja"
	}
	if strings.HasPrefix(value, "en") {
		return "en"
	}
	return value
}

func effectiveSettingsLanguage(value string) string {
	lang := normalizeSettingsLanguage(value)
	if lang == "" {
		return "en (default)"
	}
	return lang
}

func effectiveSettingsColor(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "auto (default)"
	}
	return trimmed
}

func formatSettingsFields(fields []string) string {
	if len(fields) == 0 {
		return strings.Join(readFieldIDsToStrings(defaultReadFields), ",") + " (default)"
	}
	return strings.Join(fields, ",")
}

func formatSettingsPatterns(patterns []string) string {
	if len(patterns) == 0 {
		return Localize("none", "なし")
	}
	return strings.Join(patterns, ", ")
}

func formatSettingValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return Localize("unset", "未設定")
	}
	return value
}

func formatSettingsEnvLine(name string, value string, set bool) string {
	if !set {
		return "• " + name + "=" + Localize("unset", "未設定")
	}
	if value == "" {
		return "• " + name + "=" + Localize("set empty", "空で設定")
	}
	return "• " + name + "=" + value
}

func nextSettingsColor(current string) string {
	switch strings.TrimSpace(current) {
	case "auto", "":
		return "always"
	case "always":
		return "never"
	default:
		return "auto"
	}
}

func nextSettingsFieldSet(current []string) []string {
	sets := [][]string{
		readFieldIDsToStrings(defaultReadFields),
		{"ts", "kind", "exit_code", "session", "ws", "message"},
		{"ts", "kind", "client", "agent", "id", "message"},
		{"ts", "kind", "message"},
	}
	currentLabel := strings.Join(current, ",")
	for i, set := range sets {
		if currentLabel == strings.Join(set, ",") {
			return slices.Clone(sets[(i+1)%len(sets)])
		}
	}
	return slices.Clone(sets[0])
}

func readFieldIDsToStrings(fields []readFieldID) []string {
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		out = append(out, string(field))
	}
	return out
}
