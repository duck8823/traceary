package cli

// Memory review localization glossary:
//   - The user-facing English queue label is "memory review queue".
//   - The workflow label is "メモリ確認" in Japanese.
//   - Individual candidates are "メモリ候補".
//   - The queue is "メモリ候補の確認キュー"; do not surface "inbox" as a
//     Japanese UI noun except inside literal command paths such as
//     `traceary memory inbox`. English keeps "inbox" only for literal command
//     paths and machine-searchable metric IDs such as candidate(inbox).
//   - Copy/paste-sensitive developer terms and action/command names stay
//     English: evidence, artifact refs, operator, scope, redaction, staged,
//     accept/reject/skip, edit/distill.
//   - Machine-searchable metric identifiers in Top summary rows stay English
//     (for example accepted(reviewed), candidate(inbox), remember-intent). The
//     surrounding explanation may be localized.
func memoryReviewWorkflowLabel() string {
	return Localize("memory review", "メモリ確認")
}

// memoryReviewWorkflowTitle appends a suffix that the caller has already
// localized for the active CLI locale.
func memoryReviewWorkflowTitle(localizedSuffix string) string {
	if localizedSuffix == "" {
		return memoryReviewWorkflowLabel()
	}
	return memoryReviewWorkflowLabel() + " · " + localizedSuffix
}

func memoryCandidateCountLabel(index int, total int) string {
	return Localizef("candidate %d / %d", "メモリ候補 %d / %d", index, total)
}

func memoryReviewEmptyQueueMessage() string {
	return Localize("The memory review queue is empty.", "メモリ候補の確認キューは空です")
}
