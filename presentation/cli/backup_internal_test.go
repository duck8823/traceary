package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestConfirmBackupRestore(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       string
		expectError bool
	}{
		{
			name:        "yes で続行する",
			input:       "yes\n",
			expectError: false,
		},
		{
			name:        "y で続行する",
			input:       "y\n",
			expectError: false,
		},
		{
			name:        "空入力で中止する",
			input:       "\n",
			expectError: true,
		},
		{
			name:        "no で中止する",
			input:       "no\n",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			previousReader := backupRestorePromptReader
			defer func() {
				backupRestorePromptReader = previousReader
			}()

			backupRestorePromptReader = strings.NewReader(tt.input)
			output := &bytes.Buffer{}

			err := confirmBackupRestore(output, "/tmp/traceary.db")
			if tt.expectError && err == nil {
				t.Fatal("confirmBackupRestore() error = nil, want error")
			}
			if !tt.expectError && err != nil {
				t.Fatalf("confirmBackupRestore() error = %v", err)
			}
			if !strings.Contains(output.String(), "/tmp/traceary.db") {
				t.Fatalf("prompt output = %q, want destination path", output.String())
			}
		})
	}
}

