package cli

import (
	"time"

	"github.com/duck8823/traceary/domain/types"
)

// auditPayloadInput holds the raw flag/positional values used by
// resolveAuditPayload to determine the final command/input/output strings.
type auditPayloadInput struct {
	positionalArgs []string
	command        string
	commandFlagSet bool
	input          string
	inputFlagSet   bool
	output         string
	outputFlagSet  bool
}

// auditCommandInput is the resolved input to the `traceary audit` command.
type auditCommandInput struct {
	dbPath         string
	command        string
	input          string
	output         string
	client         string
	agent          string
	sessionID      string
	repo           string
	exitCode       types.Optional[int]
	idOnly         bool
	asJSON         bool
	allowSecrets   bool
	maxInputBytes  int
	maxOutputBytes int
}

// backupCreateCommandInput is the resolved input to `traceary backup create`.
type backupCreateCommandInput struct {
	dbPath     string
	outputPath string
	force      bool
}

// backupRestoreCommandInput is the resolved input to `traceary backup restore`.
type backupRestoreCommandInput struct {
	dbPath    string
	inputPath string
	force     bool
	assumeYes bool
	prompter  *backupRestorePrompter
}

// contextCommandInput is the resolved input to the `traceary context` command.
type contextCommandInput struct {
	dbPath    string
	sessionID string
	client    string
	agent     string
	repo      string
	limit     int
	asJSON    bool
}

// doctorCommandInput is the resolved input to the `traceary doctor` command.
type doctorCommandInput struct {
	dbPath         string
	client         string
	projectDir     string
	currentVersion string
	asJSON         bool
}

// gcCommandInput is the resolved input to the `traceary gc` command.
type gcCommandInput struct {
	dbPath   string
	keepDays int
	target   string
	dryRun   bool
}

// hooksPrintCommandInput is the resolved input to `traceary hooks print`.
type hooksPrintCommandInput struct {
	client      string
	tracearyBin string
	matcher     string
}

// hooksInstallCommandInput is the resolved input to `traceary hooks install`.
type hooksInstallCommandInput struct {
	client      string
	projectDir  string
	tracearyBin string
	outputPath  string
	global      bool
	force       bool
	upgrade     bool
	matcher     string
}

// hooksGuideCommandInput is the resolved input to `traceary hooks guide`.
type hooksGuideCommandInput struct {
	client     string
	projectDir string
	outputPath string
}

// listCommandInput is the resolved input to the `traceary list` command.
type listCommandInput struct {
	dbPath       string
	limit        int
	offset       int
	kind         string
	client       string
	agent        string
	sessionID    string
	repo         string
	from         string
	since        string
	to           string
	until        string
	failuresOnly bool
	sourceHook   string
	asJSON       bool
	wide         bool
	utc          bool
	location     *time.Location
	fields       []string
	fieldsSet    bool
	preset          string
	presetSet       bool
	kindSet         bool
	clientSet       bool
	agentSet        bool
	sessionIDSet    bool
	repoSet         bool
	failuresOnlySet bool
	sourceHookSet   bool
	color           string
	colorSet        bool
}

// logCommandInput is the resolved input to the `traceary log` command.
type logCommandInput struct {
	dbPath    string
	message   string
	kind      string
	client    string
	agent     string
	sessionID string
	repo      string
	idOnly    bool
	asJSON    bool
}

// searchCommandInput is the resolved input to the `traceary search` command.
type searchCommandInput struct {
	dbPath       string
	repo         string
	sessionID    string
	client       string
	agent        string
	kind         string
	from         string
	since        string
	to           string
	until        string
	limit        int
	offset       int
	query        string
	failuresOnly bool
	asJSON       bool
	wide         bool
	utc          bool
	location     *time.Location
	fields       []string
	fieldsSet    bool
	preset          string
	presetSet       bool
	kindSet         bool
	clientSet       bool
	agentSet        bool
	sessionIDSet    bool
	repoSet         bool
	failuresOnlySet bool
	color           string
	colorSet        bool
}

// sessionBoundaryCommandInput is the resolved input to
// `traceary session start` and `traceary session end`.
type sessionBoundaryCommandInput struct {
	dbPath          string
	client          string
	agent           string
	sessionID       string
	repo            string
	summary         string
	parentSessionID string
	kind            types.EventKind
	idOnly          bool
	asJSON          bool
}

// sessionLatestCommandInput is the resolved input to
// `traceary session latest` and `traceary session active`.
type sessionLatestCommandInput struct {
	dbPath     string
	client     string
	agent      string
	repo       string
	activeOnly bool
	staleAfter time.Duration
	allowStale bool
	asJSON     bool
}

// timelineCommandInput is the resolved input to the `traceary timeline` command.
type timelineCommandInput struct {
	dbPath    string
	workspace string
	from      string
	to        string
	gap       int
	limit     int
	asJSON    bool
	utc       bool
	location  *time.Location
}

// tailCommandInput is the resolved input to the `traceary tail` command.
type tailCommandInput struct {
	dbPath        string
	limit         int
	kind          string
	client        string
	agent         string
	sessionID     string
	repo          string
	failuresOnly  bool
	asJSON        bool
	wide          bool
	utc           bool
	location      *time.Location
	fields        []string
	fieldsSet     bool
	preset          string
	presetSet       bool
	kindSet         bool
	clientSet       bool
	agentSet        bool
	sessionIDSet    bool
	repoSet         bool
	failuresOnlySet bool
	color            string
	colorSet         bool
	followSession    string
	followSessionSet bool
	nowFunc          func() time.Time
	tickerFactory    func(time.Duration) tailTicker
}

// memoryListCommandInput is the resolved input to `traceary memory list`.
type memoryListCommandInput struct {
	dbPath          string
	workspace       string
	agent           string
	sessionFamily   string
	statuses        []string
	memoryTypes     []string
	limit           int
	offset          int
	asOf            string
	includeExpired  bool
	preset          string
	asJSON          bool
}

// memorySearchCommandInput is the resolved input to `traceary memory search`.
type memorySearchCommandInput struct {
	dbPath          string
	workspace       string
	agent           string
	sessionFamily   string
	statuses        []string
	memoryTypes     []string
	limit           int
	offset          int
	query           string
	asOf            string
	includeExpired  bool
	preset          string
	asJSON          bool
}

// memoryWriteCommandInput is the resolved input to memory write commands.
type memoryWriteCommandInput struct {
	dbPath        string
	workspace     string
	agent         string
	sessionFamily string
	memoryType    string
	fact          string
	confidence    string
	source        string
	evidenceRefs  []string
	artifactRefs  []string
	idOnly        bool
	asJSON        bool
}

// memoryMutationCommandInput is the resolved input to memory status commands.
type memoryMutationCommandInput struct {
	dbPath     string
	memoryID   string
	confidence string
	expiresAt  string
	idOnly     bool
	asJSON     bool
}

// memoryValidityCommandInput is the resolved input to
// `traceary memory set-validity`. The zero string for a bound means
// "leave unchanged" (validFrom) or "clear back to open-ended"
// (validTo only when --clear-to is set — otherwise zero means leave
// unchanged).
type memoryValidityCommandInput struct {
	dbPath    string
	memoryID  string
	validFrom string
	validTo   string
	clearTo   bool
	idOnly    bool
	asJSON    bool
}

// memorySupersedeCommandInput is the resolved input to `traceary memory supersede`.
type memorySupersedeCommandInput struct {
	dbPath        string
	memoryID      string
	workspace     string
	agent         string
	sessionFamily string
	memoryType    string
	fact          string
	confidence    string
	source        string
	evidenceRefs  []string
	artifactRefs  []string
	validFrom     string
	validTo       string
	idOnly        bool
	asJSON        bool
}

// memoryExtractCommandInput is the resolved input to `traceary memory extract`.
type memoryExtractCommandInput struct {
	dbPath         string
	sessionID      string
	workspace      string
	eventLimit     int
	candidateLimit int
	asJSON         bool
}

// memoryImportCodexCommandInput is the resolved input to `traceary memory
// import codex`. The flags mirror other Traceary read/write commands where
// useful and also expose two import-only controls: --root selects the Codex
// memory layout and --watch keeps the process polling for additional runs
// without re-invoking the CLI.
type memoryImportCodexCommandInput struct {
	dbPath    string
	root      string
	workspace string
	watch     bool
	interval  time.Duration
	asJSON    bool
}

// memoryInboxListCommandInput is the resolved input to `traceary memory
// inbox`. The command is a provenance-first view over candidate durable
// memories, so the filter set is identical to `memory list` but the
// default status is pinned to candidate.
type memoryInboxListCommandInput struct {
	dbPath        string
	workspace     string
	agent         string
	sessionFamily string
	memoryTypes   []string
	sources       []string
	limit         int
	offset        int
	asJSON        bool
}

// memoryInboxBatchCommandInput is the resolved input to `traceary memory
// inbox accept` and `traceary memory inbox reject`. Batch operations walk
// the id list in order and report per-id success/failure so the caller can
// retry the failing tail without undoing the successes.
type memoryInboxBatchCommandInput struct {
	dbPath     string
	ids        []string
	confidence string
	asJSON     bool
}

// memoryExportCommandInput is the resolved input to `traceary memory
// export`. The command always writes the markdown block to --out; the
// usecase itself stays filesystem-free so tests can exercise the
// rendering without touching the disk.
type memoryExportCommandInput struct {
	dbPath    string
	target    string
	workspace string
	outPath   string
	asJSON    bool
}

// memoryImportInstructionsCommandInput is the resolved input to
// `traceary memory import instructions`. Path / Source are independent
// of the Codex-memory import subcommand so operators can mix both in the
// same workflow without flag collisions.
type memoryImportInstructionsCommandInput struct {
	dbPath    string
	source    string
	inPath    string
	workspace string
	asJSON    bool
}

// memoryHygieneScanCommandInput is the resolved input to the
// `traceary memory hygiene scan` command.
type memoryHygieneScanCommandInput struct {
	dbPath     string
	workspace  string
	expiryDays int
	similarity float64
	asJSON     bool
}

// memoryHygieneApplyCommandInput is the resolved input to
// `traceary memory hygiene apply`. Ids reference memories seen in a
// prior scan; the usecase re-scans internally to confirm the transition
// is still appropriate before mutating state.
type memoryHygieneApplyCommandInput struct {
	dbPath     string
	ids        []string
	expiryDays int
	asJSON     bool
}
