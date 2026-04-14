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
	dryRun   bool
}

// hooksPrintCommandInput is the resolved input to `traceary hooks print`.
type hooksPrintCommandInput struct {
	client      string
	tracearyBin string
}

// hooksInstallCommandInput is the resolved input to `traceary hooks install`.
type hooksInstallCommandInput struct {
	client      string
	projectDir  string
	tracearyBin string
	outputPath  string
	force       bool
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
	asJSON       bool
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
	nowFunc       func() time.Time
	tickerFactory func(time.Duration) tailTicker
}

// memoryListCommandInput is the resolved input to `traceary memory list`.
type memoryListCommandInput struct {
	dbPath        string
	workspace     string
	agent         string
	sessionFamily string
	statuses      []string
	memoryTypes   []string
	limit         int
	offset        int
	asJSON        bool
}

// memorySearchCommandInput is the resolved input to `traceary memory search`.
type memorySearchCommandInput struct {
	dbPath        string
	workspace     string
	agent         string
	sessionFamily string
	statuses      []string
	memoryTypes   []string
	limit         int
	offset        int
	query         string
	asJSON        bool
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
