package management

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestListAuthFiles_IncludesProjectIDFromManager(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	authDir := t.TempDir()
	fileName := "gemini-user@example.com-project-a.json"
	filePath := filepath.Join(authDir, fileName)
	if errWrite := os.WriteFile(filePath, []byte(`{"type":"gemini","email":"user@example.com","project_id":"project-a"}`), 0o600); errWrite != nil {
		t.Fatalf("failed to write auth file: %v", errWrite)
	}

	manager := coreauth.NewManager(nil, nil, nil)
	record := &coreauth.Auth{
		ID:       fileName,
		FileName: fileName,
		Provider: "gemini-cli",
		Status:   coreauth.StatusActive,
		Attributes: map[string]string{
			"path": filePath,
		},
		Metadata: map[string]any{
			"type":       "gemini",
			"email":      "user@example.com",
			"project_id": "project-a",
		},
	}
	if _, errRegister := manager.Register(context.Background(), record); errRegister != nil {
		t.Fatalf("failed to register auth record: %v", errRegister)
	}

	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, manager)
	h.tokenStore = &memoryAuthStore{}

	entry := firstAuthFileEntry(t, h)
	if got := entry["project_id"]; got != "project-a" {
		t.Fatalf("expected project_id %q, got %#v", "project-a", got)
	}
}

func TestListAuthFilesFromDisk_IncludesProjectID(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	authDir := t.TempDir()
	filePath := filepath.Join(authDir, "gemini-user@example.com-project-a.json")
	if errWrite := os.WriteFile(filePath, []byte(`{"type":"gemini","email":"user@example.com","project_id":"project-a"}`), 0o600); errWrite != nil {
		t.Fatalf("failed to write auth file: %v", errWrite)
	}

	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, nil)

	entry := firstAuthFileEntry(t, h)
	if got := entry["project_id"]; got != "project-a" {
		t.Fatalf("expected project_id %q, got %#v", "project-a", got)
	}
}

func TestListAuthFiles_IncludesWebsocketsFromManager(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	authDir := t.TempDir()
	fileName := "codex-user@example.com-pro.json"
	filePath := filepath.Join(authDir, fileName)
	if errWrite := os.WriteFile(filePath, []byte(`{"type":"codex","email":"user@example.com"}`), 0o600); errWrite != nil {
		t.Fatalf("failed to write auth file: %v", errWrite)
	}

	manager := coreauth.NewManager(nil, nil, nil)
	record := &coreauth.Auth{
		ID:       fileName,
		FileName: fileName,
		Provider: "codex",
		Status:   coreauth.StatusActive,
		Attributes: map[string]string{
			"path":       filePath,
			"websockets": "true",
		},
		Metadata: map[string]any{
			"type": "codex",
		},
	}
	if _, errRegister := manager.Register(context.Background(), record); errRegister != nil {
		t.Fatalf("failed to register auth record: %v", errRegister)
	}

	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, manager)
	h.tokenStore = &memoryAuthStore{}

	entry := firstAuthFileEntry(t, h)
	if got := entry["websockets"]; got != true {
		t.Fatalf("expected websockets true, got %#v", got)
	}
}

func TestListAuthFilesFromDisk_IncludesWebsockets(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	authDir := t.TempDir()
	filePath := filepath.Join(authDir, "codex-user@example.com-pro.json")
	if errWrite := os.WriteFile(filePath, []byte(`{"type":"codex","email":"user@example.com","websockets":false}`), 0o600); errWrite != nil {
		t.Fatalf("failed to write auth file: %v", errWrite)
	}

	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, nil)

	entry := firstAuthFileEntry(t, h)
	if got := entry["websockets"]; got != false {
		t.Fatalf("expected websockets false, got %#v", got)
	}
}

func TestListAuthFiles_IncludesRefreshIntervalFieldsFromManager(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	authDir := t.TempDir()
	fileName := "codex-user@example.com-pro.json"
	filePath := filepath.Join(authDir, fileName)
	if errWrite := os.WriteFile(filePath, []byte(`{"type":"codex","email":"user@example.com"}`), 0o600); errWrite != nil {
		t.Fatalf("failed to write auth file: %v", errWrite)
	}

	manager := coreauth.NewManager(nil, nil, nil)
	record := &coreauth.Auth{
		ID:       fileName,
		FileName: fileName,
		Provider: "codex",
		Status:   coreauth.StatusActive,
		Attributes: map[string]string{
			"path":            filePath,
			"refreshInterval": "168h",
		},
		Metadata: map[string]any{
			"type":                     "codex",
			"refresh_interval_seconds": 604800,
			"refresh_interval":         "168h",
		},
	}
	if _, errRegister := manager.Register(context.Background(), record); errRegister != nil {
		t.Fatalf("failed to register auth record: %v", errRegister)
	}

	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, manager)
	h.tokenStore = &memoryAuthStore{}

	entry := firstAuthFileEntry(t, h)
	if got := entry["refresh_interval_seconds"]; got != float64(604800) {
		t.Fatalf("expected refresh_interval_seconds %v, got %#v", float64(604800), got)
	}
	if got := entry["refresh_interval"]; got != "168h" {
		t.Fatalf("expected refresh_interval %q, got %#v", "168h", got)
	}
	if got := entry["refreshInterval"]; got != "168h" {
		t.Fatalf("expected refreshInterval %q, got %#v", "168h", got)
	}
}

func TestListAuthFiles_IncludesRefreshStateFieldsFromManager(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	authDir := t.TempDir()
	fileName := "codex-user@example.com-refresh.json"
	filePath := filepath.Join(authDir, fileName)
	if errWrite := os.WriteFile(filePath, []byte(`{"type":"codex","email":"user@example.com"}`), 0o600); errWrite != nil {
		t.Fatalf("failed to write auth file: %v", errWrite)
	}

	manager := coreauth.NewManager(nil, nil, nil)
	record := &coreauth.Auth{
		ID:       fileName,
		FileName: fileName,
		Provider: "codex",
		Status:   coreauth.StatusActive,
		Attributes: map[string]string{
			"path": filePath,
		},
		Metadata: map[string]any{
			"type":                    "codex",
			"fetched_refresh_time":    true,
			"exact_seven_day_refresh": false,
			"preheat_needed":          false,
			"weekly_reset_at":         "2026-06-08T12:00:00Z",
			"fetched_at":              "2026-06-01T12:00:00Z",
		},
	}
	if _, errRegister := manager.Register(context.Background(), record); errRegister != nil {
		t.Fatalf("failed to register auth record: %v", errRegister)
	}

	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, manager)
	h.tokenStore = &memoryAuthStore{}

	entry := firstAuthFileEntry(t, h)
	if got := entry["fetched_refresh_time"]; got != true {
		t.Fatalf("expected fetched_refresh_time true, got %#v", got)
	}
	if got := entry["exact_seven_day_refresh"]; got != false {
		t.Fatalf("expected exact_seven_day_refresh false, got %#v", got)
	}
	if got := entry["preheat_needed"]; got != false {
		t.Fatalf("expected preheat_needed false, got %#v", got)
	}
	if got := entry["weekly_reset_at"]; got != "2026-06-08T12:00:00Z" {
		t.Fatalf("expected weekly_reset_at %q, got %#v", "2026-06-08T12:00:00Z", got)
	}
	if got := entry["fetched_at"]; got != "2026-06-01T12:00:00Z" {
		t.Fatalf("expected fetched_at %q, got %#v", "2026-06-01T12:00:00Z", got)
	}
}

func TestListAuthFiles_IncludesPlanTypeFromManager(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	authDir := t.TempDir()
	fileName := "codex-user@example.com-free.json"
	filePath := filepath.Join(authDir, fileName)
	if errWrite := os.WriteFile(filePath, []byte(`{"type":"codex","email":"user@example.com"}`), 0o600); errWrite != nil {
		t.Fatalf("failed to write auth file: %v", errWrite)
	}

	manager := coreauth.NewManager(nil, nil, nil)
	record := &coreauth.Auth{
		ID:       fileName,
		FileName: fileName,
		Provider: "codex",
		Status:   coreauth.StatusActive,
		Attributes: map[string]string{
			"path":      filePath,
			"plan_type": "free",
		},
		Metadata: map[string]any{
			"type": "codex",
		},
	}
	if _, errRegister := manager.Register(context.Background(), record); errRegister != nil {
		t.Fatalf("failed to register auth record: %v", errRegister)
	}

	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, manager)
	h.tokenStore = &memoryAuthStore{}

	entry := firstAuthFileEntry(t, h)
	if got := entry["plan_type"]; got != "free" {
		t.Fatalf("expected plan_type %q, got %#v", "free", got)
	}
}

func TestListAuthFiles_IncludesAutoQuotaDisabledMarkerFromManager(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	authDir := t.TempDir()
	fileName := "codex-disabled.json"
	filePath := filepath.Join(authDir, fileName)
	if errWrite := os.WriteFile(filePath, []byte(`{"type":"codex","email":"disabled@example.com"}`), 0o600); errWrite != nil {
		t.Fatalf("failed to write auth file: %v", errWrite)
	}

	manager := coreauth.NewManager(nil, nil, nil)
	record := &coreauth.Auth{
		ID:       fileName,
		FileName: fileName,
		Provider: "codex",
		Disabled: true,
		Status:   coreauth.StatusDisabled,
		Attributes: map[string]string{
			"path": filePath,
		},
		Metadata: map[string]any{
			"type": "codex",
		},
		Quota: coreauth.QuotaState{
			Exceeded: true,
			Reason:   "codex_quota_auto_disabled",
		},
	}
	if _, errRegister := manager.Register(context.Background(), record); errRegister != nil {
		t.Fatalf("failed to register auth record: %v", errRegister)
	}

	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, manager)
	h.tokenStore = &memoryAuthStore{}

	entry := firstAuthFileEntry(t, h)
	if got := entry["quota_auto_disabled"]; got != true {
		t.Fatalf("expected quota_auto_disabled true, got %#v", got)
	}
}

func TestListAuthFilesFromDisk_IncludesRefreshIntervalFields(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	authDir := t.TempDir()
	filePath := filepath.Join(authDir, "codex-user@example.com-pro.json")
	if errWrite := os.WriteFile(filePath, []byte(`{"type":"codex","email":"user@example.com","refresh_interval_seconds":604800,"refreshIntervalSeconds":604800,"refresh_interval":"168h","refreshInterval":"168h"}`), 0o600); errWrite != nil {
		t.Fatalf("failed to write auth file: %v", errWrite)
	}

	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, nil)

	entry := firstAuthFileEntry(t, h)
	for _, key := range []string{"refresh_interval_seconds", "refreshIntervalSeconds"} {
		if got := entry[key]; got != float64(604800) {
			t.Fatalf("expected %s %v, got %#v", key, float64(604800), got)
		}
	}
	for _, key := range []string{"refresh_interval", "refreshInterval"} {
		if got := entry[key]; got != "168h" {
			t.Fatalf("expected %s %q, got %#v", key, "168h", got)
		}
	}
}

func TestListAuthFilesFromDisk_IncludesRefreshStateFields(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	authDir := t.TempDir()
	filePath := filepath.Join(authDir, "codex-user@example.com-refresh.json")
	if errWrite := os.WriteFile(filePath, []byte(`{"type":"codex","email":"user@example.com","fetched_refresh_time":true,"exact_seven_day_refresh":false,"preheat_needed":false,"weekly_reset_at":"2026-06-08T12:00:00Z","fetched_at":"2026-06-01T12:00:00Z"}`), 0o600); errWrite != nil {
		t.Fatalf("failed to write auth file: %v", errWrite)
	}

	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, nil)

	entry := firstAuthFileEntry(t, h)
	if got := entry["fetched_refresh_time"]; got != true {
		t.Fatalf("expected fetched_refresh_time true, got %#v", got)
	}
	if got := entry["exact_seven_day_refresh"]; got != false {
		t.Fatalf("expected exact_seven_day_refresh false, got %#v", got)
	}
	if got := entry["preheat_needed"]; got != false {
		t.Fatalf("expected preheat_needed false, got %#v", got)
	}
	if got := entry["weekly_reset_at"]; got != "2026-06-08T12:00:00Z" {
		t.Fatalf("expected weekly_reset_at %q, got %#v", "2026-06-08T12:00:00Z", got)
	}
	if got := entry["fetched_at"]; got != "2026-06-01T12:00:00Z" {
		t.Fatalf("expected fetched_at %q, got %#v", "2026-06-01T12:00:00Z", got)
	}
}

func firstAuthFileEntry(t *testing.T, h *Handler) map[string]any {
	t.Helper()

	rec := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(rec)
	ginCtx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/auth-files", nil)

	h.ListAuthFiles(ginCtx)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected list status %d, got %d with body %s", http.StatusOK, rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if errUnmarshal := json.Unmarshal(rec.Body.Bytes(), &payload); errUnmarshal != nil {
		t.Fatalf("failed to decode list payload: %v", errUnmarshal)
	}
	filesRaw, ok := payload["files"].([]any)
	if !ok {
		t.Fatalf("expected files array, payload: %#v", payload)
	}
	if len(filesRaw) != 1 {
		t.Fatalf("expected 1 auth entry, got %d", len(filesRaw))
	}
	fileEntry, ok := filesRaw[0].(map[string]any)
	if !ok {
		t.Fatalf("expected file entry object, got %#v", filesRaw[0])
	}
	return fileEntry
}
