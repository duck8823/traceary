package sqlite

import (
	"regexp"
	"strings"
	"testing"
)

var metadataSelectListPattern = regexp.MustCompile(`(?is)\bselect\b(.*?)\bfrom\b`)

func TestMetadataQueries_DoNotSelectBodyColumns(t *testing.T) {
	t.Parallel()

	queries := map[string]string{
		"recent":             selectRecentEventMetadataQuery,
		"recent source hook": selectRecentEventMetadataBySourceHookQuery,
		"recent legacy hook": selectRecentEventMetadataBySourceHookWithLegacyQuery,
		"search":             searchEventMetadataQuery,
		"context":            getContextEventMetadataQuery,
	}
	for name, query := range queries {
		name, query := name, query
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			selectLists := metadataSelectListPattern.FindAllStringSubmatch(strings.ToLower(query), -1)
			if len(selectLists) == 0 {
				t.Fatalf("query has no FROM clause: %s", query)
			}
			for _, match := range selectLists {
				selectList := match[1]
				for _, forbidden := range []string{"e.body,", "e.body ", "body_blocks", "command_text", "input_text", "output_text"} {
					if strings.Contains(selectList, forbidden) {
						t.Fatalf("SELECT list contains body-bearing column %q: %s", forbidden, selectList)
					}
				}
			}
		})
	}
}
