package cliproxy

import (
	"context"
	"net/http"
	"testing"

	internalregistry "github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
)

type codexAccountTypeModelsTestExecutor struct {
	selectedAuthID string
}

func (e *codexAccountTypeModelsTestExecutor) Identifier() string { return "codex" }

func (e *codexAccountTypeModelsTestExecutor) Execute(_ context.Context, auth *coreauth.Auth, _ cliproxyexecutor.Request, _ cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	if auth != nil {
		e.selectedAuthID = auth.ID
	}
	return cliproxyexecutor.Response{}, nil
}

func (e *codexAccountTypeModelsTestExecutor) ExecuteStream(context.Context, *coreauth.Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	return nil, nil
}

func (e *codexAccountTypeModelsTestExecutor) Refresh(_ context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
	return auth, nil
}

func (e *codexAccountTypeModelsTestExecutor) CountTokens(context.Context, *coreauth.Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, nil
}

func (e *codexAccountTypeModelsTestExecutor) HttpRequest(context.Context, *coreauth.Auth, *http.Request) (*http.Response, error) {
	return nil, nil
}

func TestCodexAccountTypeModelsRouteToMatchingAuth(t *testing.T) {
	ctx := context.Background()
	manager := coreauth.NewManager(nil, &coreauth.RoundRobinSelector{}, nil)
	executor := &codexAccountTypeModelsTestExecutor{}
	manager.RegisterExecutor(executor)

	service := &Service{
		cfg: &config.Config{
			Codex: config.CodexConfig{
				AccountTypeModels: map[string][]string{
					"plus": {"gpt-5.6-sol"},
					"free": {"gpt-5.6-terra"},
				},
			},
		},
		coreManager: manager,
	}
	plusAuth := &coreauth.Auth{
		ID:       "codex-account-type-plus",
		Provider: "codex",
		Attributes: map[string]string{
			"plan_type": "plus",
		},
	}
	freeAuth := &coreauth.Auth{
		ID:       "codex-account-type-free",
		Provider: "codex",
		Metadata: map[string]any{
			"access_token": "free-token",
			"planType":     "free",
		},
	}

	modelRegistry := internalregistry.GetGlobalRegistry()
	modelRegistry.UnregisterClient(plusAuth.ID)
	modelRegistry.UnregisterClient(freeAuth.ID)
	t.Cleanup(func() {
		modelRegistry.UnregisterClient(plusAuth.ID)
		modelRegistry.UnregisterClient(freeAuth.ID)
	})

	service.registerModelsForAuth(ctx, plusAuth)
	service.registerModelsForAuth(ctx, freeAuth)
	if !modelRegistry.ClientSupportsModel(plusAuth.ID, "gpt-5.6-sol") {
		t.Fatal("plus auth should support gpt-5.6-sol")
	}
	if modelRegistry.ClientSupportsModel(plusAuth.ID, "gpt-5.6-terra") {
		t.Fatal("plus auth should not support gpt-5.6-terra")
	}
	if !modelRegistry.ClientSupportsModel(freeAuth.ID, "gpt-5.6-terra") {
		t.Fatal("free auth should support gpt-5.6-terra")
	}
	if modelRegistry.ClientSupportsModel(freeAuth.ID, "gpt-5.6-sol") {
		t.Fatal("free auth should not support gpt-5.6-sol")
	}

	if _, err := manager.Register(ctx, plusAuth); err != nil {
		t.Fatalf("register plus auth: %v", err)
	}
	if _, err := manager.Register(ctx, freeAuth); err != nil {
		t.Fatalf("register free auth: %v", err)
	}

	if _, err := manager.Execute(ctx, []string{"codex"}, cliproxyexecutor.Request{Model: "gpt-5.6-terra"}, cliproxyexecutor.Options{}); err != nil {
		t.Fatalf("execute terra: %v", err)
	}
	if executor.selectedAuthID != freeAuth.ID {
		t.Fatalf("terra selected auth = %q, want %q", executor.selectedAuthID, freeAuth.ID)
	}

	if _, err := manager.Execute(ctx, []string{"codex"}, cliproxyexecutor.Request{Model: "gpt-5.6-sol"}, cliproxyexecutor.Options{}); err != nil {
		t.Fatalf("execute sol: %v", err)
	}
	if executor.selectedAuthID != plusAuth.ID {
		t.Fatalf("sol selected auth = %q, want %q", executor.selectedAuthID, plusAuth.ID)
	}
}

func TestCodexAccountTypeModelsReloadsOAuthRegistrations(t *testing.T) {
	ctx := context.Background()
	manager := coreauth.NewManager(nil, &coreauth.RoundRobinSelector{}, nil)
	service := &Service{cfg: &config.Config{}, coreManager: manager}
	auth := &coreauth.Auth{
		ID:       "codex-account-type-reload",
		Provider: "codex",
		Metadata: map[string]any{
			"access_token": "plus-token",
			"planType":     "plus",
		},
	}

	modelRegistry := internalregistry.GetGlobalRegistry()
	modelRegistry.UnregisterClient(auth.ID)
	t.Cleanup(func() {
		modelRegistry.UnregisterClient(auth.ID)
	})

	service.registerModelsForAuth(ctx, auth)
	if _, err := manager.Register(ctx, auth); err != nil {
		t.Fatalf("register auth: %v", err)
	}
	if !modelRegistry.ClientSupportsModel(auth.ID, "gpt-5.6-terra") {
		t.Fatal("plus auth should initially use the default model catalog")
	}

	service.applyWatcherConfigUpdate(&config.Config{
		Codex: config.CodexConfig{
			AccountTypeModels: map[string][]string{
				"plus": {"gpt-5.6-sol"},
			},
		},
	})
	if modelRegistry.ClientSupportsModel(auth.ID, "gpt-5.6-terra") {
		t.Fatal("reloaded plus auth should not support gpt-5.6-terra")
	}
	if !modelRegistry.ClientSupportsModel(auth.ID, "gpt-5.6-sol") {
		t.Fatal("reloaded plus auth should support gpt-5.6-sol")
	}
}
