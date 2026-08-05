package sqlite

import (
	"errors"
	"strings"

	sqlitelib "modernc.org/sqlite"

	apptypes "github.com/duck8823/traceary/application/types"
)

const (
	sqliteCodeTriggerConstraint       = 1811
	archiveSequenceExhaustedTriggerID = "archive sequence exhausted"
)

// mapArchiveSequenceWriteError translates the fixed migration-44 trigger
// failure into the stable application error used by every event write path.
// Both the driver code and the trigger class must match; unrelated trigger
// constraints retain their original error.
func mapArchiveSequenceWriteError(err error) error {
	if err == nil {
		return nil
	}
	var sqliteErr *sqlitelib.Error
	if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqliteCodeTriggerConstraint &&
		strings.Contains(sqliteErr.Error(), archiveSequenceExhaustedTriggerID) {
		return apptypes.ErrArchiveSequenceOverflow
	}
	return err
}
