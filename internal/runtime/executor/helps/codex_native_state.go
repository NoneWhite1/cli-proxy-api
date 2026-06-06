package helps

import (
	"net/http"
	"strings"
	"sync"

	"github.com/google/uuid"
)

const (
	CodexHeaderSessionID       = "session-id"
	CodexHeaderThreadID        = "thread-id"
	CodexHeaderClientRequestID = "x-client-request-id"
	CodexHeaderTurnState       = "x-codex-turn-state"
	CodexHeaderTurnMetadata    = "x-codex-turn-metadata"
	CodexHeaderWindowID        = "x-codex-window-id"
	CodexMetadataInstallation  = "x-codex-installation-id"
	CodexMetadataWindowID      = "x-codex-window-id"
)

type CodexNativeStateInput struct {
	ExecutionSessionID string
	AuthID             string
	APIKey             string
	PromptCacheKey     string
	InstallationID     string
	SessionID          string
	ThreadID           string
	WindowGeneration   uint64
}

type CodexNativeState struct {
	SessionID        string
	ThreadID         string
	PromptCacheKey   string
	InstallationID   string
	WindowID         string
	WindowGeneration uint64

	mu        sync.RWMutex
	turnState string
}

func NewCodexNativeState(input CodexNativeStateInput) *CodexNativeState {
	threadID := firstNonEmpty(input.ThreadID, input.PromptCacheKey, input.ExecutionSessionID, uuid.NewString())
	sessionID := firstNonEmpty(input.SessionID, input.ExecutionSessionID, threadID)
	promptCacheKey := firstNonEmpty(input.PromptCacheKey, threadID)
	installationSeed := firstNonEmpty(input.AuthID, input.APIKey, input.ExecutionSessionID, threadID)
	installationID := firstNonEmpty(input.InstallationID, codexNativeUUID("installation", installationSeed))
	windowID := threadID + ":" + uintToString(input.WindowGeneration)

	return &CodexNativeState{
		SessionID:        sessionID,
		ThreadID:         threadID,
		PromptCacheKey:   promptCacheKey,
		InstallationID:   installationID,
		WindowID:         windowID,
		WindowGeneration: input.WindowGeneration,
	}
}

func (s *CodexNativeState) ApplySessionHeaders(headers http.Header) {
	if s == nil || headers == nil {
		return
	}
	setHeaderLowercase(headers, CodexHeaderSessionID, s.SessionID)
	setHeaderLowercase(headers, CodexHeaderThreadID, s.ThreadID)
	setHeaderLowercase(headers, CodexHeaderClientRequestID, s.ThreadID)
	setHeaderLowercase(headers, CodexHeaderWindowID, s.WindowID)
	if turnState := s.TurnState(); turnState != "" {
		setHeaderLowercase(headers, CodexHeaderTurnState, turnState)
	}
}

func (s *CodexNativeState) ApplyCompactHeaders(headers http.Header) {
	if s == nil || headers == nil {
		return
	}
	setHeaderLowercase(headers, CodexHeaderSessionID, s.SessionID)
	setHeaderLowercase(headers, CodexHeaderThreadID, s.ThreadID)
	setHeaderLowercase(headers, CodexHeaderWindowID, s.WindowID)
	setHeaderLowercase(headers, CodexMetadataInstallation, s.InstallationID)
}

func (s *CodexNativeState) ClientMetadata() map[string]string {
	if s == nil {
		return nil
	}
	metadata := map[string]string{
		CodexMetadataInstallation: s.InstallationID,
	}
	return metadata
}

func (s *CodexNativeState) WebsocketClientMetadata() map[string]string {
	metadata := s.ClientMetadata()
	if s == nil {
		return metadata
	}
	metadata[CodexMetadataWindowID] = s.WindowID
	return metadata
}

func (s *CodexNativeState) CaptureTurnState(headers http.Header) string {
	if s == nil || headers == nil {
		return ""
	}
	turnState := strings.TrimSpace(headerValueCaseInsensitive(headers, CodexHeaderTurnState))
	if turnState == "" {
		return ""
	}
	s.mu.Lock()
	if s.turnState == "" {
		s.turnState = turnState
	}
	current := s.turnState
	s.mu.Unlock()
	return current
}

func (s *CodexNativeState) SetTurnState(turnState string) {
	if s == nil {
		return
	}
	turnState = strings.TrimSpace(turnState)
	if turnState == "" {
		return
	}
	s.mu.Lock()
	if s.turnState == "" {
		s.turnState = turnState
	}
	s.mu.Unlock()
}

func (s *CodexNativeState) TurnState() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.turnState
}

func codexNativeUUID(kind string, seed string) string {
	seed = strings.TrimSpace(seed)
	if seed == "" {
		return uuid.NewString()
	}
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("cli-proxy-api:codex:native:"+kind+":"+seed)).String()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func setHeaderLowercase(headers http.Header, key string, value string) {
	key = strings.ToLower(strings.TrimSpace(key))
	value = strings.TrimSpace(value)
	if key == "" || value == "" {
		return
	}
	deleteHeaderCaseInsensitive(headers, key)
	headers[key] = []string{value}
}

func deleteHeaderCaseInsensitive(headers http.Header, key string) {
	if headers == nil {
		return
	}
	key = normalizeHeaderNameForComparison(key)
	for existing := range headers {
		if normalizeHeaderNameForComparison(existing) == key {
			delete(headers, existing)
		}
	}
}

func normalizeHeaderNameForComparison(key string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(key)), "_", "-")
}

func headerValueCaseInsensitive(headers http.Header, key string) string {
	if headers == nil {
		return ""
	}
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return ""
	}
	for existing, values := range headers {
		if strings.ToLower(strings.TrimSpace(existing)) != key {
			continue
		}
		for _, value := range values {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func uintToString(value uint64) string {
	if value == 0 {
		return "0"
	}
	buf := [20]byte{}
	i := len(buf)
	for value > 0 {
		i--
		buf[i] = byte('0' + value%10)
		value /= 10
	}
	return string(buf[i:])
}
