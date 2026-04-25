// Example: Anthropic native memory tool backed by Traceary.
//
// Run from the repository root:
//
//	export ANTHROPIC_API_KEY=sk-ant-...
//	export TRACEARY_DB_PATH=/tmp/traceary-anthropic-memory.db # optional
//	go run ./examples/anthropic-memory
//
// The program makes a live Anthropic API call. CI should only build it; do not
// run it without an API key and an explicit smoke-test decision.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/infrastructure/sqlite"
	"github.com/duck8823/traceary/pkg/anthropicmemory"
)

const contextManagementBeta = "context-management-2025-06-27"

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	dbPath, err := resolveDBPath()
	if err != nil {
		return err
	}

	handler, err := newHandler(ctx, dbPath)
	if err != nil {
		return err
	}

	client := anthropic.NewClient(
		option.WithAPIKey(os.Getenv("ANTHROPIC_API_KEY")),
	)
	messages := []anthropic.BetaMessageParam{
		anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock("Remember that Traceary's Anthropic memory example stores notes locally, then briefly confirm what you did.")),
	}

	first, err := client.Beta.Messages.New(ctx, messageParams(messages))
	if err != nil {
		return xerrors.Errorf("failed to create first Anthropic message: %w", err)
	}
	printText(first)

	if first.StopReason != anthropic.BetaStopReasonToolUse {
		return nil
	}

	assistantContent, toolResults, err := runMemoryToolUses(ctx, handler, first.Content)
	if err != nil {
		return err
	}
	messages = append(messages, anthropic.BetaMessageParam{
		Role:    anthropic.BetaMessageParamRoleAssistant,
		Content: assistantContent,
	})
	messages = append(messages, anthropic.NewBetaUserMessage(toolResults...))

	second, err := client.Beta.Messages.New(ctx, messageParams(messages))
	if err != nil {
		return xerrors.Errorf("failed to create second Anthropic message: %w", err)
	}
	printText(second)
	return nil
}

func messageParams(messages []anthropic.BetaMessageParam) anthropic.BetaMessageNewParams {
	return anthropic.BetaMessageNewParams{
		Betas:     []anthropic.AnthropicBeta{contextManagementBeta},
		MaxTokens: 1024,
		Messages:  messages,
		Model:     anthropic.ModelClaudeSonnet4_5,
		Tools:     []anthropic.BetaToolUnionParam{anthropicmemory.Tool()},
	}
}

func newHandler(ctx context.Context, dbPath string) (*anthropicmemory.Handler, error) {
	migrations, err := fs.Sub(os.DirFS("."), "schema/sqlite/migrations")
	if err != nil {
		return nil, xerrors.Errorf("failed to open migration directory: %w", err)
	}
	db := sqlite.NewDatabase(dbPath, migrations)
	handler, err := anthropicmemory.NewSQLiteHandler(ctx, db)
	if err != nil {
		return nil, xerrors.Errorf("failed to create Anthropic memory handler: %w", err)
	}
	return handler, nil
}

func runMemoryToolUses(
	ctx context.Context,
	handler *anthropicmemory.Handler,
	blocks []anthropic.BetaContentBlockUnion,
) ([]anthropic.BetaContentBlockParamUnion, []anthropic.BetaContentBlockParamUnion, error) {
	assistantContent := make([]anthropic.BetaContentBlockParamUnion, 0, len(blocks))
	toolResults := make([]anthropic.BetaContentBlockParamUnion, 0)
	for _, block := range blocks {
		converted, err := contentBlockParam(block)
		if err != nil {
			return nil, nil, err
		}
		assistantContent = append(assistantContent, converted)

		if block.Type != "tool_use" || block.Name != anthropicmemory.ToolName {
			continue
		}
		input, err := anthropicmemory.DecodeInput(block.Input)
		if err != nil {
			return nil, nil, xerrors.Errorf("failed to decode memory tool input: %w", err)
		}
		result, err := handler.HandleToolUse(ctx, block.ID, input)
		if err != nil {
			return nil, nil, xerrors.Errorf("failed to handle memory tool use: %w", err)
		}
		toolResults = append(toolResults, anthropic.BetaContentBlockParamUnion{OfToolResult: &result})
	}
	return assistantContent, toolResults, nil
}

func contentBlockParam(block anthropic.BetaContentBlockUnion) (anthropic.BetaContentBlockParamUnion, error) {
	switch block.Type {
	case "text":
		return anthropic.NewBetaTextBlock(block.Text), nil
	case "tool_use":
		var input map[string]any
		if err := json.Unmarshal(block.Input, &input); err != nil {
			return anthropic.BetaContentBlockParamUnion{}, xerrors.Errorf("failed to decode assistant tool_use input: %w", err)
		}
		return anthropic.NewBetaToolUseBlock(block.ID, input, block.Name), nil
	default:
		return anthropic.BetaContentBlockParamUnion{}, xerrors.Errorf("example does not support assistant content block type %q", block.Type)
	}
}

func printText(message *anthropic.BetaMessage) {
	for _, block := range message.Content {
		if block.Type == "text" {
			fmt.Println(block.Text)
		}
	}
}

func resolveDBPath() (string, error) {
	if value := os.Getenv("TRACEARY_DB_PATH"); value != "" {
		absPath, err := filepath.Abs(value)
		if err != nil {
			return "", xerrors.Errorf("failed to resolve TRACEARY_DB_PATH: %w", err)
		}
		return absPath, nil
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", xerrors.Errorf("failed to get user home directory: %w", err)
	}
	return filepath.Join(homeDir, ".config", "traceary", "traceary.db"), nil
}
