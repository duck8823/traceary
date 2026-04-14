package usecase_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
)

func TestCodexIntegrationUsecase_Install(t *testing.T) {
	t.Parallel()

	t.Run("rejects empty required values", func(t *testing.T) {
		t.Parallel()

		testCases := []struct {
			name            string
			repoRoot        string
			codexHome       string
			marketplaceRoot string
			tracearyBin     string
			wantMessage     string
		}{
			{name: "empty repo root", repoRoot: " ", codexHome: "/tmp/.codex", marketplaceRoot: "/tmp/agents/plugins", tracearyBin: "traceary", wantMessage: "repository root must not be empty"},
			{name: "empty codex home", repoRoot: "/tmp/repo", codexHome: " ", marketplaceRoot: "/tmp/agents/plugins", tracearyBin: "traceary", wantMessage: "codex home must not be empty"},
			{name: "empty marketplace root", repoRoot: "/tmp/repo", codexHome: "/tmp/.codex", marketplaceRoot: " ", tracearyBin: "traceary", wantMessage: "marketplace root must not be empty"},
			{name: "empty traceary binary", repoRoot: "/tmp/repo", codexHome: "/tmp/.codex", marketplaceRoot: "/tmp/agents/plugins", tracearyBin: " ", wantMessage: "traceary binary must not be empty"},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				sut := usecase.NewCodexIntegrationUsecase(&codexIntegrationManagerStub{})
				_, err := sut.Install(context.Background(), tc.repoRoot, tc.codexHome, tc.marketplaceRoot, tc.tracearyBin)
				if err == nil {
					t.Fatalf("Install() error = nil, want %q", tc.wantMessage)
				}
				if diff := cmp.Diff(tc.wantMessage, err.Error()); diff != "" {
					t.Fatalf("Install() error mismatch (-want +got):\n%s", diff)
				}
			})
		}
	})

	t.Run("trims values before delegating", func(t *testing.T) {
		t.Parallel()

		manager := &codexIntegrationManagerStub{
			installResult: apptypes.CodexIntegrationInstallResultOf("/tmp/agents/plugins/plugins/traceary", "/tmp/.codex/plugins/cache/local-traceary-plugins/traceary/local", "/tmp/.codex/config.toml", "/tmp/.codex/hooks.json", "traceary@local-traceary-plugins"),
		}
		sut := usecase.NewCodexIntegrationUsecase(manager)

		_, err := sut.Install(context.Background(), " /tmp/repo ", " /tmp/.codex ", " /tmp/agents/plugins ", " traceary ")
		if err != nil {
			t.Fatalf("Install() error = %v", err)
		}

		if diff := cmp.Diff("/tmp/repo", manager.installCall.repoRoot); diff != "" {
			t.Fatalf("repoRoot mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("/tmp/.codex", manager.installCall.codexHome); diff != "" {
			t.Fatalf("codexHome mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("/tmp/agents/plugins", manager.installCall.marketplaceRoot); diff != "" {
			t.Fatalf("marketplaceRoot mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("traceary", manager.installCall.tracearyBin); diff != "" {
			t.Fatalf("tracearyBin mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("wraps manager errors", func(t *testing.T) {
		t.Parallel()

		sut := usecase.NewCodexIntegrationUsecase(&codexIntegrationManagerStub{installErr: errors.New("boom")})
		_, err := sut.Install(context.Background(), "/tmp/repo", "/tmp/.codex", "/tmp/agents/plugins", "traceary")
		if err == nil {
			t.Fatal("Install() error = nil")
		}
		if diff := cmp.Diff(true, strings.Contains(err.Error(), "failed to install Codex integration: boom")); diff != "" {
			t.Fatalf("Install() wrapped error mismatch (-want +got):\n%s", diff)
		}
	})
}

func TestCodexIntegrationUsecase_Uninstall(t *testing.T) {
	t.Parallel()

	t.Run("rejects empty required values", func(t *testing.T) {
		t.Parallel()

		testCases := []struct {
			name            string
			codexHome       string
			marketplaceRoot string
			wantMessage     string
		}{
			{name: "empty codex home", codexHome: " ", marketplaceRoot: "/tmp/agents/plugins", wantMessage: "codex home must not be empty"},
			{name: "empty marketplace root", codexHome: "/tmp/.codex", marketplaceRoot: " ", wantMessage: "marketplace root must not be empty"},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				sut := usecase.NewCodexIntegrationUsecase(&codexIntegrationManagerStub{})
				_, err := sut.Uninstall(context.Background(), tc.codexHome, tc.marketplaceRoot)
				if err == nil {
					t.Fatalf("Uninstall() error = nil, want %q", tc.wantMessage)
				}
				if diff := cmp.Diff(tc.wantMessage, err.Error()); diff != "" {
					t.Fatalf("Uninstall() error mismatch (-want +got):\n%s", diff)
				}
			})
		}
	})

	t.Run("trims values before delegating", func(t *testing.T) {
		t.Parallel()

		manager := &codexIntegrationManagerStub{
			uninstallResult: apptypes.CodexIntegrationUninstallResultOf("/tmp/agents/plugins/plugins/traceary", true, "/tmp/agents/plugins/marketplace.json", "/tmp/.codex/plugins/cache/local-traceary-plugins/traceary", true, "/tmp/.codex/config.toml", "/tmp/.codex/hooks.json", true),
		}
		sut := usecase.NewCodexIntegrationUsecase(manager)

		_, err := sut.Uninstall(context.Background(), " /tmp/.codex ", " /tmp/agents/plugins ")
		if err != nil {
			t.Fatalf("Uninstall() error = %v", err)
		}

		if diff := cmp.Diff("/tmp/.codex", manager.uninstallCall.codexHome); diff != "" {
			t.Fatalf("codexHome mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("/tmp/agents/plugins", manager.uninstallCall.marketplaceRoot); diff != "" {
			t.Fatalf("marketplaceRoot mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("wraps manager errors", func(t *testing.T) {
		t.Parallel()

		sut := usecase.NewCodexIntegrationUsecase(&codexIntegrationManagerStub{uninstallErr: errors.New("boom")})
		_, err := sut.Uninstall(context.Background(), "/tmp/.codex", "/tmp/agents/plugins")
		if err == nil {
			t.Fatal("Uninstall() error = nil")
		}
		if diff := cmp.Diff(true, strings.Contains(err.Error(), "failed to uninstall Codex integration: boom")); diff != "" {
			t.Fatalf("Uninstall() wrapped error mismatch (-want +got):\n%s", diff)
		}
	})
}

type codexIntegrationManagerStub struct {
	installCall struct {
		repoRoot        string
		codexHome       string
		marketplaceRoot string
		tracearyBin     string
	}
	installResult apptypes.CodexIntegrationInstallResult
	installErr    error

	uninstallCall struct {
		codexHome       string
		marketplaceRoot string
	}
	uninstallResult apptypes.CodexIntegrationUninstallResult
	uninstallErr    error
}

func (s *codexIntegrationManagerStub) Install(_ context.Context, repoRoot string, codexHome string, marketplaceRoot string, tracearyBin string) (apptypes.CodexIntegrationInstallResult, error) {
	s.installCall.repoRoot = repoRoot
	s.installCall.codexHome = codexHome
	s.installCall.marketplaceRoot = marketplaceRoot
	s.installCall.tracearyBin = tracearyBin
	return s.installResult, s.installErr
}

func (s *codexIntegrationManagerStub) Uninstall(_ context.Context, codexHome string, marketplaceRoot string) (apptypes.CodexIntegrationUninstallResult, error) {
	s.uninstallCall.codexHome = codexHome
	s.uninstallCall.marketplaceRoot = marketplaceRoot
	return s.uninstallResult, s.uninstallErr
}
