package cli

type cockpitNavigationSection struct {
	id                  cockpitSectionID
	key                 string
	englishLabel        string
	japaneseLabel       string
	englishDescription  string
	japaneseDescription string
}

// cockpitNavigationSections is static locale-neutral metadata. Keys and ids
// stay ASCII and language-neutral so global shortcuts and persisted state do
// not drift with the operator's locale; labels/descriptions resolve locale at
// render time.
var cockpitNavigationSections = [...]cockpitNavigationSection{
	{
		id:                  cockpitSectionLive,
		key:                 "1",
		englishLabel:        "Tail",
		japaneseLabel:       "Tail",
		englishDescription:  "live event stream and event details",
		japaneseDescription: "イベントのライブ表示と詳細確認",
	},
	{
		id:                  cockpitSectionTop,
		key:                 "2",
		englishLabel:        "Top",
		japaneseLabel:       "Top",
		englishDescription:  "dashboard for sessions, failures, commands, memory, and health",
		japaneseDescription: "セッション・失敗・コマンド・メモリ・状態の一覧",
	},
	{
		id:                  cockpitSectionMemory,
		key:                 "3",
		englishLabel:        "Memory",
		japaneseLabel:       "メモリ",
		englishDescription:  "memory review queue",
		japaneseDescription: "メモリ候補の確認キュー",
	},
	{
		id:                  cockpitSectionSessions,
		key:                 "4",
		englishLabel:        "Sessions",
		japaneseLabel:       "セッション",
		englishDescription:  "session and handoff entry points",
		japaneseDescription: "セッション一覧と引き継ぎ導線",
	},
	{
		id:                  cockpitSectionSettings,
		key:                 "5",
		englishLabel:        "Settings",
		japaneseLabel:       "設定",
		englishDescription:  "language, read defaults, redaction diagnostics",
		japaneseDescription: "言語・表示既定・redaction 診断",
	},
}

// These zero-length arrays make the build fail if the navigation table and the
// cockpitSectionID enum drift in count. Keep cockpitSectionCount immediately
// below the last real section ID; tests additionally pin the id/key/order map.
var _ [int(cockpitSectionCount) - len(cockpitNavigationSections)]struct{}
var _ [len(cockpitNavigationSections) - int(cockpitSectionCount)]struct{}

// cockpitNavigationSectionsList exposes the static metadata as a read-only
// convention slice for render helpers and tests. Callers must not mutate the
// returned slice; the package-level count assertions above keep enum drift
// caught at compile time.
func cockpitNavigationSectionsList() []cockpitNavigationSection {
	return cockpitNavigationSections[:]
}

func (s cockpitNavigationSection) label() string {
	return Localize(s.englishLabel, s.japaneseLabel)
}

func (s cockpitNavigationSection) description() string {
	return Localize(s.englishDescription, s.japaneseDescription)
}

func (s cockpitNavigationSection) prefix() string {
	return s.key + " " + s.label()
}
