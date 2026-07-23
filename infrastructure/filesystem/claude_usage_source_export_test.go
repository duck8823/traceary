package filesystem

import "github.com/duck8823/traceary/application"

func NewClaudeUsageSourceForTest(
	userHomeDir func() (string, error),
	maxFileBytes int64,
	maxLineBytes int,
) application.ClaudeUsageSource {
	source := newClaudeUsageSourceWithHomeDir(userHomeDir)
	source.maxFileBytes = maxFileBytes
	source.maxLineBytes = maxLineBytes
	return source
}
