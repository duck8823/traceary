package main

import (
	"bytes"
	"errors"
	"testing"
)

func TestWriteCLIError(t *testing.T) {
	t.Parallel()

	t.Run("plain text で user-facing error を出力する", func(t *testing.T) {
		t.Parallel()

		buffer := &bytes.Buffer{}
		if err := writeCLIError(buffer, testError("boom")); err != nil {
			t.Fatalf("writeCLIError() error = %v", err)
		}
		if buffer.String() != "Error: boom\n" {
			t.Fatalf("buffer = %q, want %q", buffer.String(), "Error: boom\n")
		}
	})

	t.Run("nil error なら何も出力しない", func(t *testing.T) {
		t.Parallel()

		buffer := &bytes.Buffer{}
		if err := writeCLIError(buffer, nil); err != nil {
			t.Fatalf("writeCLIError() error = %v", err)
		}
		if buffer.Len() != 0 {
			t.Fatalf("buffer = %q, want empty", buffer.String())
		}
	})
}

func TestCLICommandError_Unwrap(t *testing.T) {
	t.Parallel()

	baseErr := testError("boom")
	err := cliCommandError{err: baseErr}

	if err.Error() != "boom" {
		t.Fatalf("err.Error() = %q, want %q", err.Error(), "boom")
	}
	if !errors.Is(err, baseErr) {
		t.Fatal("errors.Is(err, baseErr) = false, want true")
	}
}

type testError string

func (e testError) Error() string { return string(e) }
