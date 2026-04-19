package cli

import (
	"context"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
)

// readFieldID identifies a column in compact text output for tail / list /
// search. The set of supported fields is closed on purpose so arbitrary user
// strings cannot pull internal event state into the presentation layer.
type readFieldID string

const (
	readFieldTS        readFieldID = "ts"
	readFieldKind      readFieldID = "kind"
	readFieldSession   readFieldID = "session"
	readFieldWorkspace readFieldID = "ws"
	readFieldClient    readFieldID = "client"
	readFieldAgent     readFieldID = "agent"
	readFieldMessage   readFieldID = "message"
	readFieldExitCode  readFieldID = "exit_code"
	readFieldEventID   readFieldID = "id"
)

// supportedReadFields is the canonical list of field IDs accepted by the
// --fields flag and by the read.fields config entry. The order is also the
// order shown in error messages.
var supportedReadFields = []readFieldID{
	readFieldTS,
	readFieldKind,
	readFieldSession,
	readFieldWorkspace,
	readFieldClient,
	readFieldAgent,
	readFieldMessage,
	readFieldExitCode,
	readFieldEventID,
}

// defaultReadFields is the built-in default column order, preserved from
// v0.6.1 compact output so that the default rendering stays byte-for-byte
// compatible when --fields is omitted.
var defaultReadFields = []readFieldID{
	readFieldTS,
	readFieldKind,
	readFieldSession,
	readFieldWorkspace,
	readFieldMessage,
}

func supportedReadFieldsLabel() string {
	values := make([]string, 0, len(supportedReadFields))
	for _, id := range supportedReadFields {
		values = append(values, string(id))
	}
	return strings.Join(values, ", ")
}

// resolveReadFields applies the precedence explicit flag > config columns >
// built-in default. When explicitSet is false the CLI caller has not passed
// --fields, so configFields is consulted; an empty configFields falls back
// to defaultReadFields.
func resolveReadFields(explicit []string, explicitSet bool, configFields []string) ([]readFieldID, error) {
	switch {
	case explicitSet:
		return parseReadFields(explicit)
	case len(configFields) > 0:
		return parseReadFields(configFields)
	default:
		return append([]readFieldID(nil), defaultReadFields...), nil
	}
}

// parseReadFields validates the incoming slice of raw field names and returns
// an ordered slice of typed field IDs. Empty values, unknown values, and
// duplicates are rejected with an error that includes the supported list.
func parseReadFields(values []string) ([]readFieldID, error) {
	result := make([]readFieldID, 0, len(values))
	seen := make(map[readFieldID]struct{}, len(values))
	for _, raw := range values {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			return nil, xerrors.Errorf(
				Localize(
					"read field cannot be empty (supported fields: %s)",
					"read フィールドは空にできません (利用可能なフィールド: %s)",
				),
				supportedReadFieldsLabel(),
			)
		}
		id := readFieldID(trimmed)
		if !isSupportedReadField(id) {
			return nil, xerrors.Errorf(
				Localize(
					"unsupported read field %q (supported fields: %s)",
					"read フィールド %q は対応していません (利用可能なフィールド: %s)",
				),
				trimmed,
				supportedReadFieldsLabel(),
			)
		}
		if _, dup := seen[id]; dup {
			return nil, xerrors.Errorf(
				Localize(
					"read field %q specified more than once (supported fields: %s)",
					"read フィールド %q が重複しています (利用可能なフィールド: %s)",
				),
				trimmed,
				supportedReadFieldsLabel(),
			)
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result, nil
}

func isSupportedReadField(id readFieldID) bool {
	for _, supported := range supportedReadFields {
		if supported == id {
			return true
		}
	}
	return false
}

// readFieldsContain reports whether the resolved field list contains the
// target. Callers use this to decide whether to lazily hydrate data that is
// only needed for a specific field (for example exit_code).
func readFieldsContain(fields []readFieldID, target readFieldID) bool {
	for _, f := range fields {
		if f == target {
			return true
		}
	}
	return false
}

// readFieldsFlagUsage returns the shared --fields flag usage string for tail
// / list / search commands. Keeping it in one place ensures the three
// commands advertise the same precedence semantics.
func readFieldsFlagUsage() string {
	return Localize(
		"comma-separated list of columns to show (supported: "+supportedReadFieldsLabel()+"; overrides read.fields in config.json; incompatible with --wide)",
		"表示するカラムをカンマ区切りで指定 (利用可能: "+supportedReadFieldsLabel()+"; config.json の read.fields を上書き; --wide との併用は不可)",
	)
}

// resolveReadFieldsForCommand applies the precedence rules and enforces the
// --wide vs --fields mutual exclusion for read commands. When the command is
// rendering --wide or --json output, the config-backed default is skipped so
// a broken read.fields entry does not block paths that would not consume it
// anyway.
func (c *RootCLI) resolveReadFieldsForCommand(explicit []string, explicitSet bool, wide bool, asJSON bool) ([]readFieldID, error) {
	if wide && explicitSet {
		return nil, xerrors.Errorf(Localize(
			"--fields cannot be combined with --wide (wide mode keeps the legacy seven-column format)",
			"--fields は --wide と併用できません (wide モードは従来の 7 カラム形式を維持します)",
		))
	}
	if wide || asJSON {
		return append([]readFieldID(nil), defaultReadFields...), nil
	}
	return resolveReadFields(explicit, explicitSet, c.defaultReadFields)
}

// makeCompactExtrasResolver returns a per-event extras resolver for compact
// rendering. When the resolved field list does not include fields that need
// lazy hydration (currently only exit_code), the returned resolver is nil so
// the caller can skip the hydrate loop entirely.
func (c *RootCLI) makeCompactExtrasResolver(ctx context.Context, fields []readFieldID) compactExtrasResolver {
	if !readFieldsContain(fields, readFieldExitCode) {
		return nil
	}
	if c.event == nil {
		return nil
	}
	event := c.event
	return func(ev *model.Event) compactRowExtras {
		if ev == nil {
			return compactRowExtras{}
		}
		if string(ev.Kind()) != "command_executed" {
			return compactRowExtras{}
		}
		details, err := event.Show(ctx, ev.EventID())
		if err != nil {
			return compactRowExtras{}
		}
		auditPtr, ok := details.CommandAudit().Value()
		if !ok || auditPtr == nil {
			return compactRowExtras{}
		}
		return compactRowExtras{exitCode: auditPtr.ExitCode()}
	}
}
