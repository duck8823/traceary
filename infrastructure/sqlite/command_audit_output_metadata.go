package sqlite

import (
	"strings"

	"github.com/duck8823/traceary/domain/types"
	"golang.org/x/xerrors"
)

func auditQueryWithOptionalOutputMetadata(query string, hasColumn bool) string {
	if hasColumn {
		return query
	}
	query = strings.ReplaceAll(query, ", ca.output_metadata", ", NULL")
	return strings.ReplaceAll(query, ", output_metadata", ", NULL")
}

func encodeCommandAuditOutputMetadata(metadata types.ReadOnlyOutputMetadata) (string, error) {
	encoded, err := types.EncodeReadOnlyOutputMetadata(metadata)
	if err != nil {
		return "", xerrors.Errorf("encode command audit output metadata: %w", err)
	}
	return encoded, nil
}

func decodeCommandAuditOutputMetadata(raw string) (types.Optional[types.ReadOnlyOutputMetadata], error) {
	decoded, err := types.DecodeReadOnlyOutputMetadata(raw)
	if err != nil {
		return types.None[types.ReadOnlyOutputMetadata](), xerrors.Errorf("decode command audit output metadata: %w", err)
	}
	return decoded, nil
}
