package helps

import (
	"net/http"
	"testing"
)

func TestCodexNativeStateAppliesOfficialSessionHeaders(t *testing.T) {
	state := NewCodexNativeState(CodexNativeStateInput{
		SessionID:        "session-1",
		ThreadID:         "thread-1",
		PromptCacheKey:   "cache-1",
		InstallationID:   "install-1",
		WindowGeneration: 2,
	})
	state.SetTurnState("turn-state-1")
	headers := http.Header{"Session_id": []string{"legacy-session"}}

	state.ApplySessionHeaders(headers)

	if got := headers[CodexHeaderSessionID]; len(got) != 1 || got[0] != "session-1" {
		t.Fatalf("session-id = %#v, want [session-1]", got)
	}
	if got := headers[CodexHeaderThreadID]; len(got) != 1 || got[0] != "thread-1" {
		t.Fatalf("thread-id = %#v, want [thread-1]", got)
	}
	if got := headers[CodexHeaderClientRequestID]; len(got) != 1 || got[0] != "thread-1" {
		t.Fatalf("x-client-request-id = %#v, want [thread-1]", got)
	}
	if got := headers[CodexHeaderWindowID]; len(got) != 1 || got[0] != "thread-1:2" {
		t.Fatalf("x-codex-window-id = %#v, want [thread-1:2]", got)
	}
	if got := headers[CodexHeaderTurnState]; len(got) != 1 || got[0] != "turn-state-1" {
		t.Fatalf("x-codex-turn-state = %#v, want [turn-state-1]", got)
	}
	if _, ok := headers["Session_id"]; ok {
		t.Fatalf("legacy Session_id header still exists: %#v", headers["Session_id"])
	}
}

func TestCodexNativeStateCaptureTurnStateReadsLowercaseHeader(t *testing.T) {
	state := NewCodexNativeState(CodexNativeStateInput{ThreadID: "thread-1"})
	headers := http.Header{CodexHeaderTurnState: []string{"turn-state-1"}}

	if got := state.CaptureTurnState(headers); got != "turn-state-1" {
		t.Fatalf("captured turn state = %q, want turn-state-1", got)
	}
}

func TestCodexNativeStateClientMetadata(t *testing.T) {
	state := NewCodexNativeState(CodexNativeStateInput{
		ThreadID:       "thread-1",
		InstallationID: "install-1",
	})

	metadata := state.ClientMetadata()

	if got := metadata[CodexMetadataInstallation]; got != "install-1" {
		t.Fatalf("installation metadata = %q, want install-1", got)
	}
	if _, ok := metadata[CodexMetadataWindowID]; ok {
		t.Fatalf("responses metadata includes websocket-only window id: %#v", metadata)
	}
}

func TestCodexNativeStateWebsocketClientMetadata(t *testing.T) {
	state := NewCodexNativeState(CodexNativeStateInput{
		ThreadID:       "thread-1",
		InstallationID: "install-1",
	})

	metadata := state.WebsocketClientMetadata()

	if got := metadata[CodexMetadataInstallation]; got != "install-1" {
		t.Fatalf("installation metadata = %q, want install-1", got)
	}
	if got := metadata[CodexMetadataWindowID]; got != "thread-1:0" {
		t.Fatalf("window metadata = %q, want thread-1:0", got)
	}
}

func TestCodexNativeStateAppliesCompactHeaders(t *testing.T) {
	state := NewCodexNativeState(CodexNativeStateInput{
		SessionID:      "session-1",
		ThreadID:       "thread-1",
		InstallationID: "install-1",
	})
	headers := http.Header{"Session_id": []string{"legacy-session"}}

	state.ApplyCompactHeaders(headers)

	if got := headers[CodexHeaderSessionID]; len(got) != 1 || got[0] != "session-1" {
		t.Fatalf("session-id = %#v, want [session-1]", got)
	}
	if got := headers[CodexHeaderThreadID]; len(got) != 1 || got[0] != "thread-1" {
		t.Fatalf("thread-id = %#v, want [thread-1]", got)
	}
	if got := headers[CodexHeaderWindowID]; len(got) != 1 || got[0] != "thread-1:0" {
		t.Fatalf("x-codex-window-id = %#v, want [thread-1:0]", got)
	}
	if got := headers[CodexMetadataInstallation]; len(got) != 1 || got[0] != "install-1" {
		t.Fatalf("x-codex-installation-id = %#v, want [install-1]", got)
	}
	if got := headers[CodexHeaderClientRequestID]; len(got) != 0 {
		t.Fatalf("x-client-request-id = %#v, want empty", got)
	}
	if _, ok := headers["Session_id"]; ok {
		t.Fatalf("legacy Session_id header still exists: %#v", headers["Session_id"])
	}
}

func TestCodexNativeStateCaptureTurnStateKeepsFirstValue(t *testing.T) {
	state := NewCodexNativeState(CodexNativeStateInput{ThreadID: "thread-1"})

	first := http.Header{}
	first.Set(CodexHeaderTurnState, "turn-state-1")
	second := http.Header{}
	second.Set(CodexHeaderTurnState, "turn-state-2")

	if got := state.CaptureTurnState(first); got != "turn-state-1" {
		t.Fatalf("first captured state = %q, want turn-state-1", got)
	}
	if got := state.CaptureTurnState(second); got != "turn-state-1" {
		t.Fatalf("second captured state = %q, want turn-state-1", got)
	}
	if got := state.TurnState(); got != "turn-state-1" {
		t.Fatalf("stored turn state = %q, want turn-state-1", got)
	}
}

func TestCodexNativeStateDefaultsPromptCacheKeyToThreadID(t *testing.T) {
	state := NewCodexNativeState(CodexNativeStateInput{ThreadID: "thread-1"})

	if state.PromptCacheKey != "thread-1" {
		t.Fatalf("PromptCacheKey = %q, want thread-1", state.PromptCacheKey)
	}
	if state.WindowID != "thread-1:0" {
		t.Fatalf("WindowID = %q, want thread-1:0", state.WindowID)
	}
}
