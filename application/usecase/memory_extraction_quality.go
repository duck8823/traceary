package usecase

import (
	"regexp"
	"strings"
)

// extractionNoise* names categorise low-quality memory candidates so the
// extractor can route them to the hidden inbox view (#857). Each value is a
// stable string suitable for surfacing through `memory extract --debug-signals`
// and the candidate hygiene scan (#864).
const (
	extractionNoiseDiffFragment      = "diff_fragment"
	extractionNoiseGeneratedCode     = "generated_code"
	extractionNoiseStandaloneCommand = "standalone_command"
	extractionNoiseReviewConclusion  = "review_conclusion"
	extractionNoiseWorkDeclaration   = "work_declaration"
	extractionNoiseTransientPRRound  = "transient_pr_round"
)

var (
	// diffContentPrefixPattern matches a unified-diff added/removed line.
	// The trailing alternation allows tab- and 2+ space-indented diff
	// fragments (e.g. "+\tfunc handler()" or "+  func main() {") while still
	// rejecting single-space markdown bullets like "+ list item".
	diffContentPrefixPattern = regexp.MustCompile(`^[+-](?:[^\s+\-]|\t| {2,})`)
	diffHeaderPrefixPattern  = regexp.MustCompile(`^(?:@@|diff --git\b|index [0-9a-fA-F]{4,}\.\.[0-9a-fA-F]{4,}|\+\+\+ |--- |Binary files )`)

	standaloneCommandPattern = regexp.MustCompile(`(?i)^` +
		`(?:git|gh|npm|npx|yarn|pnpm|make|go|flutter|dart|python3?|pip3?|cargo|brew|docker(?:-compose)?|kubectl|helm|aws|gcloud|terraform|psql|sqlite3?|mkdir|rmdir|rm|mv|cp|cd|ls|ssh|scp|curl|wget|cat|tail|head|sed|awk|grep|rg|bash|zsh|sh|tree|env|export|source|sudo|node|deno|bun|tsx|swift|xcrun|adb|fastlane|gem|bundle|rake|tox|pytest|jest|vitest|mvn|gradle|sbt|cmake|ninja|jq|tar|zip|unzip|gzip|gunzip|hg|svn|nslookup|dig|ping|traceroute|systemctl|service|launchctl|crontab|chmod|chown|kill|pkill|ps|lsof|netstat|whoami|id|uname|date|rtk|traceary|claude|codex|gemini)` +
		`\s+\S`)

	// reviewConclusionEnglishPattern is conservatively anchored: each
	// alternative must end after a small review-scope suffix. This prevents
	// durable facts that *start* with LGTM / "No issues" /
	// "MUST findings: none" from being hidden when the user continues with a
	// real follow-up.
	reviewConclusionEnglishPattern = regexp.MustCompile(`(?i)^` +
		`(?:` +
		// "(severity) findings: none|nothing|n/a|0" with optional
		// review-scope wording such as "for this round" plus terminal
		// punctuation. Do not allow arbitrary words after "none".
		`(?:must|should|nice-to-have|critical|high|medium|low|severe|blocker)?\s*findings?\s*[:\-]\s*(?:none|nothing|n/?a|0)\b` + reviewConclusionScopeTailPattern +
		// "(no|zero) (severity)? (findings|issues|problems|concerns|defects) (found|detected)?"
		// with the same constrained review-scope tail.
		`|(?:no|zero)\s+(?:must|critical|high|medium|low|blocker(?:s)?|severe)?\s*(?:findings?|issues?|problems?|concerns?|defects?)(?:\s+(?:found|detected))?\b` + reviewConclusionScopeTailPattern +
		// LGTM-style approvals must reach end-of-string with only non-word
		// trailing characters (whitespace, punctuation, emoji). Any letter
		// after the phrase signals durable continuation and excludes it.
		`|(?:lgtm|approved|approve|all\s+(?:good|clear|green|passing)|ship\s*it|looks\s+good\s+to\s+me|good\s+to\s+go)\b\W*$` +
		`)`)

	reviewConclusionJapanesePattern = regexp.MustCompile(
		`^(?:確認(?:済み|しました|完了)?[\s、,・]*)?(?:問題|不具合|エラー)?(?:なし|ありません|ありませんでした)(?:です)?[。\.]?\s*$` +
			`|^OK(?:です)?[。\.]?\s*$` +
			`|^承認(?:済み|済|しました)?[。\.]?\s*$` +
			`|^異常なし(?:です)?[。\.]?\s*$`,
	)

	workDeclarationEnglishPattern = regexp.MustCompile(`(?i)^` +
		`(?:` +
		`i'?ll\s+\w+` +
		`|i\s+(?:will|am\s+going\s+to|am\s+gonna|need\s+to|have\s+to|plan\s+to|intend\s+to|am\s+about\s+to)\s+\w+` +
		`|let\s+me\s+\w+` +
		`|let'?s\s+\w+` +
		`|(?:going\s+to|gonna|about\s+to|planning\s+to)\s+\w+` +
		`)`)

	workDeclarationJapanesePattern = regexp.MustCompile(
		`^(?:私(?:は|が)?\s*)?(?:これから|今から|まず(?:は)?|次に(?:は)?|それから|まずは|次は)`,
	)

	transientPRRoundPattern = regexp.MustCompile(`(?i)^(?:round\s+\d+\b|pr\s*#?\d+\s+(?:review|check|status|round|update|recap|notes?|pass|follow-?ups?))`)

	reviewConclusionScopeTailPattern = `(?:\s+(?:for|in|on)\s+(?:(?:this|the)\s+)?(?:round|review|pass|pr|pull\s+request|change|changes))?[\s.!?。．]*$`

	// standaloneCommandProseMarkers list connectives, modal verbs, articles,
	// and descriptive verb forms that strongly indicate the input is prose
	// describing a command rather than a command line itself. The check is
	// space-padded so a token only matches as a whole word.
	standaloneCommandProseMarkers = []string{
		// Articles and determiners
		" a ", " an ", " the ", " this ", " that ", " these ", " those ", " its ", " their ",
		// "be" forms
		" is ", " are ", " was ", " were ", " be ", " been ", " being ",
		// "have" / "do" forms
		" has ", " have ", " had ", " having ",
		" do ", " does ", " did ", " doing ",
		// Modals
		" can ", " could ", " should ", " would ", " might ", " may ", " must ", " will ", " shall ",
		// Negations / temporal adverbs
		" not ", " never ", " always ", " sometimes ", " often ", " rarely ", " usually ",
		// Conjunctions and subordinators
		" because ", " therefore ", " however ", " although ", " though ", " unless ", " whereas ", " while ", " when ", " since ", " until ", " before ", " after ",
		// Prepositions that mark prose context (infinitives, purpose, platform,
		// origin, accompaniment). These are very rare in real command lines
		// because shell tokens are unquoted identifiers, flags, paths, or
		// `KEY=VALUE` pairs — bare English prepositions almost never appear.
		" to ", " for ", " on ", " from ", " with ", " of ", " into ", " as ", " than ",
		// Descriptive verbs (3rd-person / past / gerund forms rarely appear in commands)
		" require ", " requires ", " required ", " requiring ",
		" need ", " needs ", " needed ", " needing ",
		" fail ", " fails ", " failed ", " failing ",
		" support ", " supports ", " supported ", " supporting ",
		" depend ", " depends ", " depended ", " depending ",
		" expect ", " expects ", " expected ", " expecting ",
		" produce ", " produces ", " produced ", " producing ",
		" return ", " returns ", " returned ", " returning ",
		" find ", " finds ", " found ", " finding ",
		" use ", " uses ", " used ", " using ",
		" follow ", " follows ", " followed ", " following ",
		" cover ", " covers ", " covered ", " covering ",
		" enforce ", " enforces ", " enforced ", " enforcing ",
		" work ", " works ", " worked ", " working ",
		" exist ", " exists ", " existed ", " existing ",
		" allow ", " allows ", " allowed ", " allowing ",
		" enable ", " enables ", " enabled ", " enabling ",
		" break ", " breaks ", " broke ", " broken ", " breaking ",
		" contain ", " contains ", " contained ", " containing ",
		" include ", " includes ", " included ", " including ",
		" trigger ", " triggers ", " triggered ", " triggering ",
		" cause ", " causes ", " caused ", " causing ",
		// Japanese prose tokens
		" です", " ます", " した", " ません", " ない", " である",
	}
)

// classifyExtractionNoise returns the low-quality reason markers detected in
// the candidate fact. The set is conservative: each rule must match a
// well-known noise format so durable preferences, decisions, lessons, and
// constraints never trip the filter. Callers should treat a non-empty result
// as a hint to route the candidate to the hidden inbox source unless the user
// expressed explicit remember intent.
func classifyExtractionNoise(fact string) []string {
	trimmed := strings.TrimSpace(fact)
	if trimmed == "" {
		return nil
	}
	lower := strings.ToLower(trimmed)
	reasons := make([]string, 0, 2)
	if isDiffFragment(trimmed) {
		reasons = append(reasons, extractionNoiseDiffFragment)
	}
	if isGeneratedCodeMarker(lower) {
		reasons = append(reasons, extractionNoiseGeneratedCode)
	}
	if isStandaloneCommand(trimmed, lower) {
		reasons = append(reasons, extractionNoiseStandaloneCommand)
	}
	if isReviewConclusion(trimmed) {
		reasons = append(reasons, extractionNoiseReviewConclusion)
	}
	if isWorkDeclaration(trimmed) {
		reasons = append(reasons, extractionNoiseWorkDeclaration)
	}
	if isTransientPRRound(lower) {
		reasons = append(reasons, extractionNoiseTransientPRRound)
	}
	if len(reasons) == 0 {
		return nil
	}
	return reasons
}

func isDiffFragment(value string) bool {
	return diffContentPrefixPattern.MatchString(value) || diffHeaderPrefixPattern.MatchString(value)
}

func isGeneratedCodeMarker(lower string) bool {
	return strings.Contains(lower, "code generated by") ||
		strings.Contains(lower, "auto-generated") ||
		strings.Contains(lower, "automatically generated") ||
		strings.Contains(lower, "auto generated") ||
		strings.Contains(lower, "自動生成")
}

func isStandaloneCommand(value, lower string) bool {
	if !standaloneCommandPattern.MatchString(value) {
		return false
	}
	// CJK prose strongly suggests a sentence describing or constraining a
	// command rather than an executable line — keep those out of the
	// standalone-command bucket so durable Japanese / multilingual facts such
	// as "go test 必須" or "git pull 常に" are not mis-hidden.
	if hasCJKScript(value) {
		return false
	}
	if !hasStandaloneCommandShape(value) {
		return false
	}
	paddedLower := " " + lower + " "
	for _, marker := range standaloneCommandProseMarkers {
		if strings.Contains(paddedLower, marker) {
			return false
		}
	}
	return true
}

func hasStandaloneCommandShape(value string) bool {
	fields := strings.Fields(value)
	if len(fields) < 2 {
		return false
	}
	command := normalizeStandaloneCommandToken(fields[0])
	second := normalizeStandaloneCommandToken(fields[1])
	if command == "" || second == "" {
		return false
	}
	knownSubcommand := isKnownStandaloneSubcommand(command, second)
	hasShellOperand := isLikelyShellOperand(fields[1])
	for _, field := range fields[2:] {
		if strings.HasPrefix(field, "-") || strings.Contains(field, "=") || isLikelyShellOperand(field) {
			hasShellOperand = true
			break
		}
	}
	if knownSubcommand {
		return len(fields) == 2 || hasShellOperand || !looksLikeKnownSubcommandProseContinuation(fields[2:])
	}
	if hasShellOperand {
		return true
	}
	return allowsLooseStandaloneOperand(command) && len(fields) == 2 && !looksLikeCommonProsePair(command, second)
}

func looksLikeKnownSubcommandProseContinuation(fields []string) bool {
	if len(fields) == 0 {
		return false
	}
	continuation := " " + strings.ToLower(strings.Join(fields, " ")) + " "
	for _, marker := range standaloneCommandProseMarkers {
		if strings.Contains(continuation, marker) {
			return true
		}
	}
	return false
}

func normalizeStandaloneCommandToken(value string) string {
	trimmed := strings.Trim(value, "`'\"()[]{}.,:;!?")
	return strings.ToLower(trimmed)
}

func isKnownStandaloneSubcommand(command, subcommand string) bool {
	subcommands, ok := standaloneCommandSubcommands[command]
	if !ok {
		return false
	}
	_, ok = subcommands[subcommand]
	return ok
}

var standaloneCommandSubcommands = map[string]map[string]struct{}{
	"git": {
		"add": {}, "bisect": {}, "branch": {}, "checkout": {}, "clone": {}, "commit": {}, "diff": {}, "fetch": {}, "grep": {}, "init": {}, "log": {}, "merge": {}, "mv": {}, "pull": {}, "push": {}, "rebase": {}, "reset": {}, "restore": {}, "rm": {}, "show": {}, "stash": {}, "status": {}, "switch": {}, "tag": {}, "worktree": {},
	},
	"gh": {
		"api": {}, "auth": {}, "codespace": {}, "config": {}, "extension": {}, "gist": {}, "issue": {}, "label": {}, "pr": {}, "release": {}, "repo": {}, "run": {}, "search": {}, "workflow": {},
	},
	"go": {
		"build": {}, "clean": {}, "env": {}, "fmt": {}, "generate": {}, "get": {}, "install": {}, "list": {}, "mod": {}, "run": {}, "test": {}, "tool": {}, "version": {}, "vet": {}, "work": {},
	},
	"docker": {
		"build": {}, "compose": {}, "down": {}, "exec": {}, "images": {}, "logs": {}, "network": {}, "ps": {}, "pull": {}, "push": {}, "restart": {}, "run": {}, "start": {}, "stop": {}, "up": {}, "volume": {},
	},
	"docker-compose": {
		"build": {}, "down": {}, "exec": {}, "images": {}, "logs": {}, "ps": {}, "pull": {}, "push": {}, "restart": {}, "run": {}, "start": {}, "stop": {}, "up": {},
	},
	"kubectl": {
		"apply": {}, "config": {}, "delete": {}, "describe": {}, "exec": {}, "get": {}, "logs": {}, "patch": {}, "rollout": {}, "scale": {},
	},
	"npm":      stringSet("audit", "build", "ci", "create", "exec", "install", "publish", "run", "start", "test"),
	"npx":      stringSet("create", "jest", "tsx", "vite"),
	"pnpm":     stringSet("add", "build", "create", "dlx", "exec", "install", "remove", "run", "start", "test"),
	"yarn":     stringSet("add", "build", "create", "dlx", "exec", "install", "remove", "run", "start", "test"),
	"cargo":    stringSet("build", "check", "clippy", "fmt", "install", "run", "test"),
	"brew":     stringSet("bundle", "cleanup", "install", "list", "tap", "uninstall", "update", "upgrade"),
	"rtk":      stringSet("git", "gh", "go", "gofmt", "golangci-lint"),
	"traceary": stringSet("context", "hook", "memory", "session", "shell", "version"),
	"codex":    stringSet("review"),
}

func stringSet(values ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func isLikelyShellOperand(value string) bool {
	trimmed := strings.Trim(value, "`'\"")
	return strings.HasPrefix(trimmed, "-") ||
		strings.HasPrefix(trimmed, "/") ||
		strings.HasPrefix(trimmed, "./") ||
		strings.HasPrefix(trimmed, "../") ||
		strings.Contains(trimmed, "/") ||
		strings.Contains(trimmed, "=") ||
		strings.Contains(trimmed, "*") ||
		strings.Contains(trimmed, "$") ||
		strings.Contains(trimmed, "{") ||
		strings.Contains(trimmed, "}") ||
		strings.Contains(trimmed, "|") ||
		strings.Contains(trimmed, ">") ||
		strings.Contains(trimmed, "<")
}

func allowsLooseStandaloneOperand(command string) bool {
	switch command {
	case "make", "cmake", "ninja", "pytest", "jest", "vitest", "tox", "mvn", "gradle", "sbt", "bash", "zsh", "sh", "node", "deno", "bun", "tsx", "python", "python3", "pip", "pip3", "ruby", "gem", "bundle", "rake", "dart", "flutter", "swift", "xcrun", "adb", "fastlane", "helm", "aws", "gcloud", "terraform", "psql", "sqlite", "sqlite3", "mkdir", "rmdir", "rm", "mv", "cp", "cd", "ls", "ssh", "scp", "curl", "wget", "cat", "tail", "head", "sed", "awk", "grep", "rg", "tree", "env", "export", "source", "sudo", "jq", "tar", "zip", "unzip", "gzip", "gunzip", "hg", "svn", "nslookup", "dig", "ping", "traceroute", "systemctl", "service", "launchctl", "crontab", "chmod", "chown", "kill", "pkill", "ps", "lsof", "netstat", "whoami", "id", "uname", "date", "claude", "gemini":
		return true
	default:
		return false
	}
}

func looksLikeCommonProsePair(command, second string) bool {
	switch command + " " + second {
	case "make sure", "codex review", "gemini review", "claude code":
		return true
	default:
		return false
	}
}

func hasCJKScript(value string) bool {
	for _, r := range value {
		switch {
		case r >= 0x3040 && r <= 0x309F: // Hiragana
			return true
		case r >= 0x30A0 && r <= 0x30FF: // Katakana
			return true
		case r >= 0x3400 && r <= 0x4DBF: // CJK Unified Ideographs Extension A
			return true
		case r >= 0x4E00 && r <= 0x9FFF: // CJK Unified Ideographs
			return true
		case r >= 0xAC00 && r <= 0xD7AF: // Hangul Syllables
			return true
		case r >= 0xF900 && r <= 0xFAFF: // CJK Compatibility Ideographs
			return true
		}
	}
	return false
}

func isReviewConclusion(value string) bool {
	return reviewConclusionEnglishPattern.MatchString(value) || reviewConclusionJapanesePattern.MatchString(value)
}

func isWorkDeclaration(value string) bool {
	if !workDeclarationEnglishPattern.MatchString(value) && !workDeclarationJapanesePattern.MatchString(value) {
		return false
	}
	// "I need to always run gofmt before committing" or "覚えておいて: ..."
	// pair the work-declaration prefix with a durable-signal marker
	// (always/never/must/...). Treat those as durable, not ephemeral
	// chatter, so the noise filter does not hide them.
	if hasDurableSignalMarker(value) {
		return false
	}
	// "Let's use ContextUsecase for handoff output" or "I'll prefer Go
	// over Python" describe long-lived choices, not throwaway actions.
	// Detect choice-establishing verbs and exclude them.
	if hasDurableChoicePhrasing(value) {
		return false
	}
	return true
}

func hasDurableChoicePhrasing(value string) bool {
	lower := " " + strings.ToLower(value) + " "
	for _, marker := range durableChoicePhrasingMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

var durableChoicePhrasingMarkers = []string{
	" use ", " uses ", " using ",
	" prefer ", " prefers ", " preferred ", " preferring ",
	" adopt ", " adopts ", " adopted ", " adopting ",
	" choose ", " chooses ", " chose ", " chosen ", " choosing ",
	" switch to ", " switches to ", " switched to ", " switching to ",
	" standardize ", " standardizes ", " standardized ", " standardizing ",
	" rely on ", " relies on ", " relied on ", " relying on ",
	" treat as ", " treats as ", " treated as ", " treating as ",
}

func isTransientPRRound(lower string) bool {
	return transientPRRoundPattern.MatchString(lower)
}
