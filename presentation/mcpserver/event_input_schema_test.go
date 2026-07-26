package mcpserver

import (
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

func TestEventToolInputSchemasDocumentHardBudgetLimits(t *testing.T) {
	t.Parallel()

	listSchema, err := jsonschema.For[listEventsInput](nil)
	if err != nil {
		t.Fatalf("jsonschema.For[listEventsInput]() error = %v", err)
	}
	searchSchema, err := jsonschema.For[searchInput](nil)
	if err != nil {
		t.Fatalf("jsonschema.For[searchInput]() error = %v", err)
	}
	contextSchema, err := jsonschema.For[getContextInput](nil)
	if err != nil {
		t.Fatalf("jsonschema.For[getContextInput]() error = %v", err)
	}

	for name, schema := range map[string]*jsonschema.Schema{
		"list_events": listSchema,
		"search":      searchSchema,
		"get_context": contextSchema,
	} {
		limitDescription := schema.Properties["limit"].Description
		if !strings.Contains(limitDescription, "maximum: 100") {
			t.Errorf("%s limit description = %q, want maximum 100", name, limitDescription)
		}
		bodyLimitDescription := schema.Properties["body_limit"].Description
		if !strings.Contains(bodyLimitDescription, "maximum 16384") {
			t.Errorf("%s body_limit description = %q, want maximum 16384", name, bodyLimitDescription)
		}
	}
}
