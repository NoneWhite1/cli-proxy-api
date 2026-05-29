package management

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

func TestCodexForceWebsocketsManagementToggle(t *testing.T) {
	gin.SetMode(gin.TestMode)

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("codex-force-websockets: false\nws-auth: true\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	h := NewHandler(&config.Config{WebsocketAuth: true}, configPath, nil)
	router := gin.New()
	router.GET("/codex-force-websockets", h.GetCodexForceWebsockets)
	router.PATCH("/codex-force-websockets", h.PutCodexForceWebsockets)
	router.GET("/ws-auth", h.GetWebsocketAuth)

	initial := httptest.NewRecorder()
	router.ServeHTTP(initial, httptest.NewRequest(http.MethodGet, "/codex-force-websockets", nil))
	if initial.Code != http.StatusOK {
		t.Fatalf("initial GET status = %d, want %d", initial.Code, http.StatusOK)
	}
	var initialBody map[string]bool
	if err := json.Unmarshal(initial.Body.Bytes(), &initialBody); err != nil {
		t.Fatalf("decode initial body: %v", err)
	}
	if initialBody["codex-force-websockets"] {
		t.Fatal("codex-force-websockets initial value = true, want false")
	}

	patch := httptest.NewRecorder()
	router.ServeHTTP(patch, httptest.NewRequest(http.MethodPatch, "/codex-force-websockets", strings.NewReader(`{"value":true}`)))
	if patch.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, want %d; body=%s", patch.Code, http.StatusOK, patch.Body.String())
	}
	if !h.cfg.CodexForceWebsockets {
		t.Fatal("handler config codex-force-websockets = false, want true")
	}
	if !h.cfg.WebsocketAuth {
		t.Fatal("ws-auth changed while toggling codex-force-websockets")
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), "codex-force-websockets: true") {
		t.Fatalf("persisted config missing codex-force-websockets: true; got:\n%s", data)
	}

	wsAuth := httptest.NewRecorder()
	router.ServeHTTP(wsAuth, httptest.NewRequest(http.MethodGet, "/ws-auth", nil))
	if !strings.Contains(wsAuth.Body.String(), `"ws-auth":true`) {
		t.Fatalf("ws-auth response changed after toggle: %s", wsAuth.Body.String())
	}
}
