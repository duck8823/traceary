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
		englishDescription:  "live event stream",
		japaneseDescription: "イベントのライブ表示",
	},
	{
		id:                  cockpitSectionTop,
		key:                 "2",
		englishLabel:        "Sessions",
		japaneseLabel:       "セッション",
		englishDescription:  "session dashboard for sessions, failures, commands, and health",
		japaneseDescription: "セッション・失敗・コマンド・状態の一覧",
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
		id:                  cockpitSectionSettings,
		key:                 "4",
		englishLabel:        "Settings",
		japaneseLabel:       "設定",
		englishDescription:  "language, read defaults, redaction diagnostics",
		japaneseDescription: "言語・表示既定・redaction 診断",
	},
}

// These zero-length arrays make the build fail if the navigation table and the
// cockpitSectionID enum drift in count. Keep cockpitSectionCount immediately
// below the last real section ID; the first assertion catches len > count and
// the second catches len < count. Tests additionally pin the id/key/order map.
var _ [int(cockpitSectionCount) - len(cockpitNavigationSections)]struct{}
var _ [len(cockpitNavigationSections) - int(cockpitSectionCount)]struct{}

// cockpitNavigationSectionsList returns a defensive slice copy so render
// helpers and tests cannot mutate the package-level navigation table; the
// package-level count assertions above keep enum drift caught at compile time.
func cockpitNavigationSectionsList() []cockpitNavigationSection {
	sections := make([]cockpitNavigationSection, len(cockpitNavigationSections))
	copy(sections, cockpitNavigationSections[:])
	return sections
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
