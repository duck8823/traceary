// Package anthropicmemory exposes Traceary's local backend for Anthropic's
// native beta memory tool.
package anthropicmemory

import (
	"context"
	"encoding/json"
	"math"

	"github.com/anthropics/anthropic-sdk-go"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/sqlite"
)

// ToolVersion is the Anthropic memory-tool version Traceary implements.
//
// Do not auto-bump this value when Anthropic ships a newer beta version. Review
// the command schema, return strings, and storage semantics manually before
// adding another handler version. The value is read from the SDK's generated
// constant type default so compile-time drift is visible when the SDK changes.
var ToolVersion = string(anthropic.BetaMemoryTool20250818Param{}.Type.Default())

// ToolName is the Anthropic built-in memory tool name.
var ToolName = string(anthropic.BetaMemoryTool20250818Param{}.Name.Default())

// Handler executes Anthropic memory-tool commands against Traceary storage.
type Handler struct {
	memoryTool usecase.MemoryToolUsecase
}

// NewHandler wraps an existing MemoryToolUsecase.
func NewHandler(memoryTool usecase.MemoryToolUsecase) (*Handler, error) {
	if memoryTool == nil {
		return nil, xerrors.Errorf("memory tool usecase must not be nil")
	}
	return &Handler{memoryTool: memoryTool}, nil
}

// NewHandlerWithRepository creates a Handler from a memory-tool repository.
func NewHandlerWithRepository(repository model.MemoryToolFileRepository, clock types.Clock) (*Handler, error) {
	if repository == nil {
		return nil, xerrors.Errorf("memory tool repository must not be nil")
	}
	return NewHandler(usecase.NewMemoryToolUsecase(repository, clock))
}

// NewSQLiteHandler creates a Handler backed by Traceary's SQLite store.
//
// The caller owns Database construction, including the migration filesystem and
// DB path policy. This constructor initializes the store before wrapping the
// memory-tool datasource so fresh database files have the memory_tool_files
// table before the first command executes.
func NewSQLiteHandler(ctx context.Context, db *sqlite.Database) (*Handler, error) {
	if db == nil {
		return nil, xerrors.Errorf("sqlite database must not be nil")
	}
	store := sqlite.NewStoreManagementDatasource(db)
	if err := store.Initialize(ctx); err != nil {
		return nil, xerrors.Errorf("failed to initialize SQLite store for Anthropic memory handler: %w", err)
	}
	return NewHandlerWithRepository(sqlite.NewMemoryToolFileDatasource(db), types.SystemClock{})
}

// Tool returns the pinned Anthropic beta memory-tool definition.
func Tool() anthropic.BetaToolUnionParam {
	return anthropic.BetaToolUnionParam{
		OfMemoryTool20250818: &anthropic.BetaMemoryTool20250818Param{},
	}
}

// Handle executes a BetaMemoryTool20250818 command and returns tool_result
// content suitable for anthropic.NewBetaToolResultTextBlockParam or
// BetaToolResultBlockParam.Content.
func (h *Handler) Handle(
	ctx context.Context,
	input anthropic.BetaMemoryTool20250818CommandUnion,
) (anthropic.BetaToolResultBlockParamContentUnion, error) {
	text, err := h.HandleText(ctx, input)
	if err != nil {
		return anthropic.BetaToolResultBlockParamContentUnion{}, err
	}
	return anthropic.BetaToolResultBlockParamContentUnion{OfText: &anthropic.BetaTextBlockParam{Text: text}}, nil
}

// HandleText executes a BetaMemoryTool20250818 command and returns the raw text
// that should be sent back in a tool_result block.
func (h *Handler) HandleText(ctx context.Context, input anthropic.BetaMemoryTool20250818CommandUnion) (string, error) {
	if h == nil || h.memoryTool == nil {
		return "", xerrors.Errorf("memory tool handler is not initialized")
	}

	switch command := input.AsAny().(type) {
	case anthropic.BetaMemoryTool20250818ViewCommand:
		viewRange, err := intSlice(command.ViewRange, "view_range")
		if err != nil {
			return "", err
		}
		result, err := h.memoryTool.View(ctx, command.Path, viewRange)
		return wrapResult("view", result, err)
	case anthropic.BetaMemoryTool20250818CreateCommand:
		result, err := h.memoryTool.Create(ctx, command.Path, command.FileText)
		return wrapResult("create", result, err)
	case anthropic.BetaMemoryTool20250818StrReplaceCommand:
		result, err := h.memoryTool.StrReplace(ctx, command.Path, command.OldStr, command.NewStr)
		return wrapResult("str_replace", result, err)
	case anthropic.BetaMemoryTool20250818InsertCommand:
		insertLine, err := intValue(command.InsertLine, "insert_line")
		if err != nil {
			return "", err
		}
		result, err := h.memoryTool.Insert(ctx, command.Path, insertLine, command.InsertText)
		return wrapResult("insert", result, err)
	case anthropic.BetaMemoryTool20250818DeleteCommand:
		result, err := h.memoryTool.Delete(ctx, command.Path)
		return wrapResult("delete", result, err)
	case anthropic.BetaMemoryTool20250818RenameCommand:
		result, err := h.memoryTool.Rename(ctx, command.OldPath, command.NewPath)
		return wrapResult("rename", result, err)
	default:
		return "", xerrors.Errorf("unsupported %s command: %q", ToolVersion, input.Command)
	}
}

// HandleToolUse executes a command and wraps its output as a complete
// tool_result block for the supplied tool_use ID.
func (h *Handler) HandleToolUse(
	ctx context.Context,
	toolUseID string,
	input anthropic.BetaMemoryTool20250818CommandUnion,
) (anthropic.BetaToolResultBlockParam, error) {
	text, err := h.HandleText(ctx, input)
	if err != nil {
		return anthropic.BetaToolResultBlockParam{}, err
	}
	return anthropic.NewBetaToolResultTextBlockParam(toolUseID, text, false), nil
}

// DecodeInput decodes a raw tool_use input payload into the pinned SDK command
// union. It is useful because response tool_use blocks expose Input as raw JSON.
func DecodeInput(data json.RawMessage) (anthropic.BetaMemoryTool20250818CommandUnion, error) {
	var input anthropic.BetaMemoryTool20250818CommandUnion
	if len(data) == 0 {
		return input, xerrors.Errorf("memory tool input must not be empty")
	}
	if err := json.Unmarshal(data, &input); err != nil {
		return input, xerrors.Errorf("failed to decode %s input: %w", ToolVersion, err)
	}
	if input.AsAny() == nil {
		return input, xerrors.Errorf("unsupported %s command: %q", ToolVersion, input.Command)
	}
	return input, nil
}

func intSlice(values []int64, field string) ([]int, error) {
	converted := make([]int, 0, len(values))
	for _, value := range values {
		item, err := intValue(value, field)
		if err != nil {
			return nil, err
		}
		converted = append(converted, item)
	}
	return converted, nil
}

func intValue(value int64, field string) (int, error) {
	if value < int64(math.MinInt) || value > int64(math.MaxInt) {
		return 0, xerrors.Errorf("%s value %d overflows int", field, value)
	}
	return int(value), nil
}

func wrapResult(command string, result string, err error) (string, error) {
	if err != nil {
		return "", xerrors.Errorf("failed to execute %s memory tool command: %w", command, err)
	}
	return result, nil
}
