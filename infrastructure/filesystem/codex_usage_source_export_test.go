package filesystem

import "github.com/duck8823/traceary/application"

func NewCodexUsageSourceForTest(
	userHomeDir func() (string, error),
	maxFileBytes int64,
	maxLineBytes int,
) application.CodexUsageSource {
	source := newCodexUsageSourceWithHomeDir(userHomeDir)
	source.maxFileBytes = maxFileBytes
	source.maxLineBytes = maxLineBytes
	return source
}
