package usecase

import (
	"fmt"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/redaction"
	"github.com/duck8823/traceary/domain/model"
	domtypes "github.com/duck8823/traceary/domain/types"
)

func candidateSegments(text string) []string {
	lines := strings.Split(text, "\n")
	segments := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := normalizeCandidateFact(line)
		if trimmed == "" {
			continue
		}
		segments = append(segments, trimmed)
	}
	return segments
}

func normalizeCandidateFact(value string) string {
	trimmed := strings.TrimSpace(value)
	trimmed = strings.TrimPrefix(trimmed, "- ")
	trimmed = strings.TrimPrefix(trimmed, "* ")
	trimmed = strings.TrimPrefix(trimmed, "> ")
	trimmed = strings.Trim(trimmed, "`")
	trimmed = strings.Trim(trimmed, "[]")
	return strings.Join(strings.Fields(trimmed), " ")
}

func sanitizeCandidateFact(value string, extraRedactors []redaction.Redactor) string {
	sanitized, _ := redaction.Apply(strings.TrimSpace(value), extraRedactors)
	return strings.Join(strings.Fields(sanitized), " ")
}

func memoryTypeFromLabel(label string) (domtypes.MemoryType, error) {
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "preference", "feedback", "correction":
		return domtypes.MemoryTypePreference, nil
	case "好み", "設定", "要望", "修正", "フィードバック":
		return domtypes.MemoryTypePreference, nil
	case "decision":
		return domtypes.MemoryTypeDecision, nil
	case "決定", "判断":
		return domtypes.MemoryTypeDecision, nil
	case "constraint":
		return domtypes.MemoryTypeConstraint, nil
	case "制約":
		return domtypes.MemoryTypeConstraint, nil
	case "lesson":
		return domtypes.MemoryTypeLesson, nil
	case "教訓", "学び":
		return domtypes.MemoryTypeLesson, nil
	case "artifact":
		return domtypes.MemoryTypeArtifact, nil
	case "成果物", "資料":
		return domtypes.MemoryTypeArtifact, nil
	default:
		return domtypes.MemoryType(""), xerrors.Errorf("unsupported memory label: %s", label)
	}
}

func inferMemoryTypeFromText(text string) (domtypes.MemoryType, bool) {
	lower := strings.ToLower(text)

	switch {
	case containsAny(lower,
		"prefer ",
		"please ",
		"always ",
		"never ",
		"do not ",
		"don't ",
		"respond in",
		"call it ",
		"use english",
		"use japanese",
		"correction:",
		"feedback:",
		"してください",
		"してほしい",
		"常に",
		"必ず",
	):
		return domtypes.MemoryTypePreference, true
	case containsAny(lower,
		"decision:",
		"decided ",
		"we chose",
		"chosen ",
		"agreed ",
		"not tech-debt",
		"not technical debt",
		"not be treated as tech-debt",
		"決定",
		"判断",
		"採用",
		"ではない",
		"扱わない",
		"扱う",
		"済み",
	):
		return domtypes.MemoryTypeDecision, true
	case containsAny(lower,
		"must ",
		"must not",
		"required",
		"cannot ",
		"can't ",
		"only ",
		"forbidden",
		"out of scope",
		"必須",
		"必要",
		"不可",
		"できない",
		"してはいけない",
		"禁止",
	):
		return domtypes.MemoryTypeConstraint, true
	case containsAny(lower,
		"lesson:",
		"learned ",
		"next time",
		"remember to",
		"remember this",
		"remember that",
		"durable memory",
		"keep this in memory",
		"keep this in mind",
		"mistake",
		"教訓",
		"学び",
		"次回",
		"再起動",
		"restart",
		"解消",
		"確認済み",
		"覚えておいて",
		"おぼえておいて",
		"覚えてください",
		"覚えておく",
		"記憶しておいて",
		"記憶",
		"今後",
		"以後",
		"誤り",
	):
		return domtypes.MemoryTypeLesson, true
	default:
		if inferredArtifactRefs, err := inferArtifactRefs(text); err == nil && len(inferredArtifactRefs) > 0 && containsAny(lower, "see ", "reference ", "refer to", "artifact:") {
			return domtypes.MemoryTypeArtifact, true
		}
		return domtypes.MemoryType(""), false
	}
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func buildSignalEvidenceRefs(sessionID domtypes.SessionID, event *model.Event) ([]domtypes.EvidenceRef, error) {
	evidenceRefs := make([]domtypes.EvidenceRef, 0, 2)
	if sessionID.String() != "" {
		ref, err := domtypes.EvidenceRefFrom(domtypes.EvidenceRefKindSession, sessionID.String())
		if err != nil {
			return nil, xerrors.Errorf("failed to build session evidence ref: %w", err)
		}
		evidenceRefs = append(evidenceRefs, ref)
	}
	if event != nil {
		ref, err := domtypes.EvidenceRefFrom(domtypes.EvidenceRefKindEvent, event.EventID().String())
		if err != nil {
			return nil, xerrors.Errorf("failed to build event evidence ref: %w", err)
		}
		evidenceRefs = append(evidenceRefs, ref)
	}
	return evidenceRefs, nil
}

func mergeEvidenceRefs(groups ...[]domtypes.EvidenceRef) []domtypes.EvidenceRef {
	refs := make([]domtypes.EvidenceRef, 0)
	seen := make(map[string]struct{})
	for _, group := range groups {
		for _, ref := range group {
			key := ref.Kind().String() + ":" + ref.Value()
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			refs = append(refs, ref)
		}
	}
	return refs
}

func inferArtifactRefs(text string) ([]domtypes.ArtifactRef, error) {
	refs := make([]domtypes.ArtifactRef, 0)
	seen := make(map[string]struct{})
	appendRef := func(kind domtypes.ArtifactRefKind, value string) error {
		key := fmt.Sprintf("%s:%s", kind, value)
		if _, exists := seen[key]; exists {
			return nil
		}
		ref, err := domtypes.ArtifactRefFrom(kind, value)
		if err != nil {
			return xerrors.Errorf("invalid artifact ref %s:%s: %w", kind, value, err)
		}
		refs = append(refs, ref)
		seen[key] = struct{}{}
		return nil
	}

	textWithoutURLs := text
	for _, match := range urlRefPattern.FindAllString(text, -1) {
		if err := appendRef(domtypes.ArtifactRefKindURL, match); err != nil {
			return nil, xerrors.Errorf("failed to build URL artifact ref: %w", err)
		}
		textWithoutURLs = strings.ReplaceAll(textWithoutURLs, match, " ")
	}
	for _, matches := range issueRefPattern.FindAllStringSubmatch(text, -1) {
		if len(matches) != 2 {
			continue
		}
		if err := appendRef(domtypes.ArtifactRefKindIssue, "#"+matches[1]); err != nil {
			return nil, xerrors.Errorf("failed to build issue artifact ref: %w", err)
		}
	}
	for _, matches := range prRefPattern.FindAllStringSubmatch(text, -1) {
		if len(matches) != 2 {
			continue
		}
		if err := appendRef(domtypes.ArtifactRefKindPR, "#"+matches[1]); err != nil {
			return nil, xerrors.Errorf("failed to build PR artifact ref: %w", err)
		}
	}
	textWithoutPaths := textWithoutURLs
	for _, match := range pathLikeRefPattern.FindAllString(textWithoutURLs, -1) {
		normalized := normalizeArtifactPathMatch(match)
		if !looksPathLikeArtifact(normalized) {
			continue
		}
		if err := appendRef(domtypes.ArtifactRefKindFile, normalized); err != nil {
			return nil, xerrors.Errorf("failed to build file artifact ref: %w", err)
		}
		textWithoutPaths = strings.ReplaceAll(textWithoutPaths, match, " ")
	}
	for _, match := range bareFileRefPattern.FindAllString(textWithoutPaths, -1) {
		if err := appendRef(domtypes.ArtifactRefKindFile, match); err != nil {
			return nil, xerrors.Errorf("failed to build file artifact ref: %w", err)
		}
	}

	return refs, nil
}

func normalizeArtifactPathMatch(value string) string {
	trimmed := strings.TrimSpace(value)
	trimmed = strings.TrimLeft(trimmed, "`'\"()[]{}<>,:;")
	trimmed = strings.TrimRight(trimmed, "`'\"()[]{}<>,:;.!?")
	return trimmed
}

func looksPathLikeArtifact(value string) bool {
	if value == "" {
		return false
	}
	if !strings.Contains(value, "/") {
		return false
	}
	if strings.Contains(value, "://") {
		return false
	}
	hasExplicitPrefix := strings.HasPrefix(value, "./") || strings.HasPrefix(value, "../") || strings.HasPrefix(value, "/")

	hasLetter := false
	for _, r := range value {
		if ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') {
			hasLetter = true
			break
		}
	}
	if !hasLetter {
		return false
	}
	segments := strings.Split(value, "/")
	if len(segments) < 2 {
		return false
	}
	firstMeaningful := ""
	lastMeaningful := ""
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			continue
		}
		if firstMeaningful == "" {
			firstMeaningful = segment
		}
		lastMeaningful = segment
	}
	if firstMeaningful == "" || lastMeaningful == "" {
		return false
	}
	if strings.Contains(lastMeaningful, ".") {
		return hasAllowedArtifactExtension(lastMeaningful)
	}
	if hasExplicitPrefix {
		return true
	}
	_, ok := extensionlessArtifactRootSegments[strings.ToLower(firstMeaningful)]
	return ok
}

func hasAllowedArtifactExtension(value string) bool {
	lastDot := strings.LastIndex(value, ".")
	if lastDot <= 0 || lastDot == len(value)-1 {
		return false
	}
	_, ok := artifactFileExtensions[strings.ToLower(value[lastDot+1:])]
	return ok
}

func memoryCandidateKey(scope domtypes.MemoryScope, memoryType domtypes.MemoryType, fact string) string {
	scopeKey := ""
	if scope != nil {
		scopeKey = fmt.Sprintf("%s:%s", scope.Kind(), scope.Key())
	}
	return fmt.Sprintf("%s|%s|%s", scopeKey, memoryType, strings.ToLower(strings.TrimSpace(fact)))
}
