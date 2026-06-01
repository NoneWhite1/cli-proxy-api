package auth

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type quotaDisableStore struct {
	mu    sync.Mutex
	saved *Auth
}

func (s *quotaDisableStore) List(context.Context) ([]*Auth, error) { return nil, nil }

func (s *quotaDisableStore) Save(_ context.Context, auth *Auth) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saved = auth.Clone()
	return auth.ID, nil
}

func (s *quotaDisableStore) Delete(context.Context, string) error { return nil }

func (s *quotaDisableStore) latest() *Auth {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.saved == nil {
		return nil
	}
	return s.saved.Clone()
}

func waitForQuotaRecoveryCondition(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if fn() {
		return
	}
	t.Fatalf("condition not met within %s", timeout)
}

func TestManager_Update_PreservesModelStates(t *testing.T) {
	m := NewManager(nil, nil, nil)

	model := "test-model"
	backoffLevel := 7

	if _, errRegister := m.Register(context.Background(), &Auth{
		ID:       "auth-1",
		Provider: "claude",
		Metadata: map[string]any{"k": "v"},
		ModelStates: map[string]*ModelState{
			model: {
				Quota: QuotaState{BackoffLevel: backoffLevel},
			},
		},
	}); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}

	if _, errUpdate := m.Update(context.Background(), &Auth{
		ID:       "auth-1",
		Provider: "claude",
		Metadata: map[string]any{"k": "v2"},
	}); errUpdate != nil {
		t.Fatalf("update auth: %v", errUpdate)
	}

	updated, ok := m.GetByID("auth-1")
	if !ok || updated == nil {
		t.Fatalf("expected auth to be present")
	}
	if len(updated.ModelStates) == 0 {
		t.Fatalf("expected ModelStates to be preserved")
	}
	state := updated.ModelStates[model]
	if state == nil {
		t.Fatalf("expected model state to be present")
	}
	if state.Quota.BackoffLevel != backoffLevel {
		t.Fatalf("expected BackoffLevel to be %d, got %d", backoffLevel, state.Quota.BackoffLevel)
	}
}

func TestManager_MarkResult_DisablesCredentialAfterNoQuota429(t *testing.T) {
	store := &quotaDisableStore{}
	m := NewManager(store, nil, nil)

	if _, errRegister := m.Register(context.Background(), &Auth{
		ID:       "codex-no-quota",
		Provider: "codex",
		Status:   StatusActive,
		Metadata: map[string]any{"access_token": "token"},
	}); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}
	store.saved = nil

	m.MarkResult(context.Background(), Result{
		AuthID:   "codex-no-quota",
		Provider: "codex",
		Model:    "gpt-5",
		Success:  false,
		Error: &Error{
			HTTPStatus: http.StatusTooManyRequests,
			Message:    `{"error":{"type":"usage_limit_reached","message":"usage limit reached"}}`,
		},
	})

	updated, ok := m.GetByID("codex-no-quota")
	if !ok || updated == nil {
		t.Fatal("expected auth to be present")
	}
	if !updated.Disabled {
		t.Fatal("expected no-quota 429 to disable credential")
	}
	if updated.Status != StatusDisabled {
		t.Fatalf("status = %q, want %q", updated.Status, StatusDisabled)
	}
	if store.saved == nil || !store.saved.Disabled || store.saved.Status != StatusDisabled {
		t.Fatalf("expected disabled credential to be persisted, got %#v", store.saved)
	}
}

func TestManager_MarkResult_DoesNotDisableCredentialAfterTransient429(t *testing.T) {
	m := NewManager(nil, nil, nil)

	if _, errRegister := m.Register(context.Background(), &Auth{
		ID:       "codex-transient-429",
		Provider: "codex",
		Status:   StatusActive,
		Metadata: map[string]any{"access_token": "token"},
	}); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}

	m.MarkResult(context.Background(), Result{
		AuthID:   "codex-transient-429",
		Provider: "codex",
		Model:    "gpt-5",
		Success:  false,
		Error: &Error{
			HTTPStatus: http.StatusTooManyRequests,
			Message:    `{"error":{"code":"websocket_connection_limit_reached","message":"too many websockets"}}`,
		},
	})

	updated, ok := m.GetByID("codex-transient-429")
	if !ok || updated == nil {
		t.Fatal("expected auth to be present")
	}
	if updated.Disabled || updated.Status == StatusDisabled {
		t.Fatalf("transient 429 disabled credential: disabled=%v status=%q", updated.Disabled, updated.Status)
	}
	if !strings.Contains(updated.StatusMessage, "too many websockets") {
		t.Fatalf("status message = %q, want transient error retained", updated.StatusMessage)
	}
}

func TestManager_CodexQuotaRecoveryLoopRecoversDueAutoDisabledCredential(t *testing.T) {
	store := &quotaDisableStore{}
	m := NewManager(store, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.StartAutoRefresh(ctx, time.Hour)
	defer m.StopAutoRefresh()

	if _, errRegister := m.Register(ctx, &Auth{
		ID:       "codex-recover-due",
		Provider: "codex",
		Status:   StatusActive,
		Metadata: map[string]any{"access_token": "token"},
	}); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}

	retryAfter := 25 * time.Millisecond
	m.MarkResult(ctx, Result{
		AuthID:     "codex-recover-due",
		Provider:   "codex",
		Model:      "gpt-5",
		Success:    false,
		RetryAfter: &retryAfter,
		Error: &Error{
			HTTPStatus: http.StatusTooManyRequests,
			Message:    `{"error":{"type":"usage_limit_reached","message":"usage limit reached"}}`,
		},
	})

	disabled, ok := m.GetByID("codex-recover-due")
	if !ok || disabled == nil {
		t.Fatal("expected auth to be present")
	}
	if !disabled.Disabled || disabled.Status != StatusDisabled {
		t.Fatalf("disabled state = disabled:%v status:%q, want auto-disabled", disabled.Disabled, disabled.Status)
	}
	if disabled.Quota.Reason != quotaAutoDisabledReason || disabled.Quota.NextRecoverAt.IsZero() {
		t.Fatalf("quota marker = %+v, want auto-disabled marker with recovery time", disabled.Quota)
	}

	waitForQuotaRecoveryCondition(t, time.Second, func() bool {
		updated, okUpdated := m.GetByID("codex-recover-due")
		return okUpdated && updated != nil && !updated.Disabled && updated.Status == StatusActive && !updated.Quota.Exceeded && updated.Quota.Reason == ""
	})
	if latest := store.latest(); latest == nil || latest.Disabled || latest.Status != StatusActive || latest.Quota.Exceeded {
		t.Fatalf("persisted recovery = %#v, want active credential with cleared quota", latest)
	}
}

func TestManager_CodexQuotaRecoveryLoopKeepsNotDueCredentialDisabled(t *testing.T) {
	m := NewManager(nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.StartAutoRefresh(ctx, time.Hour)
	defer m.StopAutoRefresh()

	if _, errRegister := m.Register(ctx, &Auth{ID: "codex-not-due", Provider: "codex", Status: StatusActive, Metadata: map[string]any{"access_token": "token"}}); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}
	retryAfter := time.Hour
	m.MarkResult(ctx, Result{
		AuthID:     "codex-not-due",
		Provider:   "codex",
		Model:      "gpt-5",
		Success:    false,
		RetryAfter: &retryAfter,
		Error:      &Error{HTTPStatus: http.StatusTooManyRequests, Message: `{"error":{"type":"usage_limit_reached"}}`},
	})
	time.Sleep(75 * time.Millisecond)
	updated, ok := m.GetByID("codex-not-due")
	if !ok || updated == nil {
		t.Fatal("expected auth to be present")
	}
	if !updated.Disabled || updated.Status != StatusDisabled || updated.Quota.Reason != quotaAutoDisabledReason {
		t.Fatalf("not-due credential = disabled:%v status:%q quota:%+v, want still auto-disabled", updated.Disabled, updated.Status, updated.Quota)
	}
}

func TestManager_CodexQuotaRecoveryLoopRecoversManualDisableWithAutoQuotaMarker(t *testing.T) {
	m := NewManager(nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.StartAutoRefresh(ctx, time.Hour)
	defer m.StopAutoRefresh()

	if _, errRegister := m.Register(ctx, &Auth{ID: "codex-manual-recover", Provider: "codex", Status: StatusActive, Metadata: map[string]any{"access_token": "token"}}); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}
	retryAfter := 25 * time.Millisecond
	m.MarkResult(ctx, Result{
		AuthID:     "codex-manual-recover",
		Provider:   "codex",
		Model:      "gpt-5",
		Success:    false,
		RetryAfter: &retryAfter,
		Error:      &Error{HTTPStatus: http.StatusTooManyRequests, Message: `{"error":{"type":"usage_limit_reached"}}`},
	})

	manual, ok := m.GetByID("codex-manual-recover")
	if !ok || manual == nil {
		t.Fatal("expected auth to be present")
	}
	if !manual.Disabled || manual.Status != StatusDisabled || manual.Quota.Reason != quotaAutoDisabledReason {
		t.Fatalf("expected auto quota disabled auth before manual update, got disabled=%v status=%q quota=%+v", manual.Disabled, manual.Status, manual.Quota)
	}
	manual.Disabled = true
	manual.Status = StatusDisabled
	manual.StatusMessage = "disabled via management API"
	if _, errUpdate := m.Update(ctx, manual); errUpdate != nil {
		t.Fatalf("manual update: %v", errUpdate)
	}

	waitForQuotaRecoveryCondition(t, time.Second, func() bool {
		updated, okUpdated := m.GetByID("codex-manual-recover")
		return okUpdated && updated != nil && !updated.Disabled && updated.Status == StatusActive && !updated.Quota.Exceeded && updated.Quota.Reason == ""
	})
}

func TestManager_CodexQuotaRecoveryLoopDoesNotRecoverManualDisableWithoutAutoQuotaMarker(t *testing.T) {
	m := NewManager(nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.StartAutoRefresh(ctx, time.Hour)
	defer m.StopAutoRefresh()

	if _, errRegister := m.Register(ctx, &Auth{ID: "codex-manual-disable", Provider: "codex", Status: StatusActive, Metadata: map[string]any{"access_token": "token"}}); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}
	retryAfter := 25 * time.Millisecond
	m.MarkResult(ctx, Result{
		AuthID:     "codex-manual-disable",
		Provider:   "codex",
		Model:      "gpt-5",
		Success:    false,
		RetryAfter: &retryAfter,
		Error:      &Error{HTTPStatus: http.StatusTooManyRequests, Message: `{"error":{"type":"usage_limit_reached"}}`},
	})

	manual, ok := m.GetByID("codex-manual-disable")
	if !ok || manual == nil {
		t.Fatal("expected auth to be present")
	}
	ClearAutoQuotaDisabledState(manual)
	manual.Disabled = true
	manual.Status = StatusDisabled
	manual.StatusMessage = "disabled via management API"
	if _, errUpdate := m.Update(ctx, manual); errUpdate != nil {
		t.Fatalf("manual update: %v", errUpdate)
	}

	time.Sleep(100 * time.Millisecond)
	updated, ok := m.GetByID("codex-manual-disable")
	if !ok || updated == nil {
		t.Fatal("expected auth to be present")
	}
	if !updated.Disabled || updated.Status != StatusDisabled || updated.StatusMessage != "disabled via management API" {
		t.Fatalf("manual credential recovered unexpectedly: disabled=%v status=%q message=%q", updated.Disabled, updated.Status, updated.StatusMessage)
	}
	if updated.Quota.Reason == quotaAutoDisabledReason {
		t.Fatalf("manual credential retained auto quota marker: %+v", updated.Quota)
	}
}

func TestManager_CodexQuotaRecoveryLoopWakesForEarlierEnqueue(t *testing.T) {
	m := NewManager(nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.StartAutoRefresh(ctx, time.Hour)
	defer m.StopAutoRefresh()

	for _, id := range []string{"codex-late", "codex-early"} {
		if _, errRegister := m.Register(ctx, &Auth{ID: id, Provider: "codex", Status: StatusActive, Metadata: map[string]any{"access_token": "token"}}); errRegister != nil {
			t.Fatalf("register %s: %v", id, errRegister)
		}
	}

	lateRetry := time.Hour
	m.MarkResult(ctx, Result{AuthID: "codex-late", Provider: "codex", Model: "gpt-5", Success: false, RetryAfter: &lateRetry, Error: &Error{HTTPStatus: http.StatusTooManyRequests, Message: `{"error":{"type":"usage_limit_reached"}}`}})
	earlyRetry := 25 * time.Millisecond
	m.MarkResult(ctx, Result{AuthID: "codex-early", Provider: "codex", Model: "gpt-5", Success: false, RetryAfter: &earlyRetry, Error: &Error{HTTPStatus: http.StatusTooManyRequests, Message: `{"error":{"type":"usage_limit_reached"}}`}})

	waitForQuotaRecoveryCondition(t, time.Second, func() bool {
		updated, okUpdated := m.GetByID("codex-early")
		return okUpdated && updated != nil && !updated.Disabled && updated.Status == StatusActive
	})
	late, ok := m.GetByID("codex-late")
	if !ok || late == nil {
		t.Fatal("expected late auth to be present")
	}
	if !late.Disabled || late.Status != StatusDisabled {
		t.Fatalf("late credential recovered early: disabled=%v status=%q", late.Disabled, late.Status)
	}
}

func TestManager_CodexQuotaRecoveryLoopSkipsStaleEarlierRecovery(t *testing.T) {
	m := NewManager(nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.StartAutoRefresh(ctx, time.Hour)
	defer m.StopAutoRefresh()

	if _, errRegister := m.Register(ctx, &Auth{ID: "codex-stale", Provider: "codex", Status: StatusActive, Metadata: map[string]any{"access_token": "token"}}); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}
	firstRetry := 25 * time.Millisecond
	m.MarkResult(ctx, Result{AuthID: "codex-stale", Provider: "codex", Model: "gpt-5", Success: false, RetryAfter: &firstRetry, Error: &Error{HTTPStatus: http.StatusTooManyRequests, Message: `{"error":{"type":"usage_limit_reached"}}`}})
	secondRetry := time.Hour
	m.MarkResult(ctx, Result{AuthID: "codex-stale", Provider: "codex", Model: "gpt-5", Success: false, RetryAfter: &secondRetry, Error: &Error{HTTPStatus: http.StatusTooManyRequests, Message: `{"error":{"type":"usage_limit_reached"}}`}})

	time.Sleep(100 * time.Millisecond)
	updated, ok := m.GetByID("codex-stale")
	if !ok || updated == nil {
		t.Fatal("expected auth to be present")
	}
	if !updated.Disabled || updated.Status != StatusDisabled || updated.Quota.Reason != quotaAutoDisabledReason {
		t.Fatalf("stale earlier recovery changed auth: disabled=%v status=%q quota=%+v", updated.Disabled, updated.Status, updated.Quota)
	}
}

func TestManager_CodexQuotaRecoveryLoopRebuildsExistingAutoDisabledCredential(t *testing.T) {
	m := NewManager(nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	recoverAt := time.Now().Add(-time.Second)
	if _, errRegister := m.Register(ctx, &Auth{
		ID:             "codex-rebuild",
		Provider:       "codex",
		Disabled:       true,
		Status:         StatusDisabled,
		StatusMessage:  "disabled after quota exhausted",
		NextRetryAfter: recoverAt,
		Metadata:       map[string]any{"access_token": "token"},
		Quota: QuotaState{
			Exceeded:      true,
			Reason:        quotaAutoDisabledReason,
			NextRecoverAt: recoverAt,
		},
	}); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}
	m.StartAutoRefresh(ctx, time.Hour)
	defer m.StopAutoRefresh()

	waitForQuotaRecoveryCondition(t, time.Second, func() bool {
		updated, okUpdated := m.GetByID("codex-rebuild")
		return okUpdated && updated != nil && !updated.Disabled && updated.Status == StatusActive && !updated.Quota.Exceeded
	})
}

func TestManager_Update_DisabledExistingDoesNotInheritModelStates(t *testing.T) {
	m := NewManager(nil, nil, nil)

	// Register a disabled auth with existing ModelStates.
	if _, err := m.Register(context.Background(), &Auth{
		ID:       "auth-disabled",
		Provider: "claude",
		Disabled: true,
		Status:   StatusDisabled,
		ModelStates: map[string]*ModelState{
			"stale-model": {
				Quota: QuotaState{BackoffLevel: 5},
			},
		},
	}); err != nil {
		t.Fatalf("register auth: %v", err)
	}

	// Update with empty ModelStates — should NOT inherit stale states.
	if _, err := m.Update(context.Background(), &Auth{
		ID:       "auth-disabled",
		Provider: "claude",
		Disabled: true,
		Status:   StatusDisabled,
	}); err != nil {
		t.Fatalf("update auth: %v", err)
	}

	updated, ok := m.GetByID("auth-disabled")
	if !ok || updated == nil {
		t.Fatalf("expected auth to be present")
	}
	if len(updated.ModelStates) != 0 {
		t.Fatalf("expected disabled auth NOT to inherit ModelStates, got %d entries", len(updated.ModelStates))
	}
}

func TestManager_Update_ActiveToDisabledDoesNotInheritModelStates(t *testing.T) {
	m := NewManager(nil, nil, nil)

	// Register an active auth with ModelStates (simulates existing live auth).
	if _, err := m.Register(context.Background(), &Auth{
		ID:       "auth-a2d",
		Provider: "claude",
		Status:   StatusActive,
		ModelStates: map[string]*ModelState{
			"stale-model": {
				Quota: QuotaState{BackoffLevel: 9},
			},
		},
	}); err != nil {
		t.Fatalf("register auth: %v", err)
	}

	// File watcher deletes config → synthesizes Disabled=true auth → Update.
	// Even though existing is active, incoming auth is disabled → skip inheritance.
	if _, err := m.Update(context.Background(), &Auth{
		ID:       "auth-a2d",
		Provider: "claude",
		Disabled: true,
		Status:   StatusDisabled,
	}); err != nil {
		t.Fatalf("update auth: %v", err)
	}

	updated, ok := m.GetByID("auth-a2d")
	if !ok || updated == nil {
		t.Fatalf("expected auth to be present")
	}
	if len(updated.ModelStates) != 0 {
		t.Fatalf("expected active→disabled transition NOT to inherit ModelStates, got %d entries", len(updated.ModelStates))
	}
}

func TestManager_Update_DisabledToActiveDoesNotInheritStaleModelStates(t *testing.T) {
	m := NewManager(nil, nil, nil)

	// Register a disabled auth with stale ModelStates.
	if _, err := m.Register(context.Background(), &Auth{
		ID:       "auth-d2a",
		Provider: "claude",
		Disabled: true,
		Status:   StatusDisabled,
		ModelStates: map[string]*ModelState{
			"stale-model": {
				Quota: QuotaState{BackoffLevel: 4},
			},
		},
	}); err != nil {
		t.Fatalf("register auth: %v", err)
	}

	// Re-enable: incoming auth is active, existing is disabled → skip inheritance.
	if _, err := m.Update(context.Background(), &Auth{
		ID:       "auth-d2a",
		Provider: "claude",
		Status:   StatusActive,
	}); err != nil {
		t.Fatalf("update auth: %v", err)
	}

	updated, ok := m.GetByID("auth-d2a")
	if !ok || updated == nil {
		t.Fatalf("expected auth to be present")
	}
	if len(updated.ModelStates) != 0 {
		t.Fatalf("expected disabled→active transition NOT to inherit stale ModelStates, got %d entries", len(updated.ModelStates))
	}
}

func TestManager_Update_ActiveInheritsModelStates(t *testing.T) {
	m := NewManager(nil, nil, nil)

	model := "active-model"
	backoffLevel := 3

	// Register an active auth with ModelStates.
	if _, err := m.Register(context.Background(), &Auth{
		ID:       "auth-active",
		Provider: "claude",
		Status:   StatusActive,
		ModelStates: map[string]*ModelState{
			model: {
				Quota: QuotaState{BackoffLevel: backoffLevel},
			},
		},
	}); err != nil {
		t.Fatalf("register auth: %v", err)
	}

	// Update with empty ModelStates — both sides active → SHOULD inherit.
	if _, err := m.Update(context.Background(), &Auth{
		ID:       "auth-active",
		Provider: "claude",
		Status:   StatusActive,
	}); err != nil {
		t.Fatalf("update auth: %v", err)
	}

	updated, ok := m.GetByID("auth-active")
	if !ok || updated == nil {
		t.Fatalf("expected auth to be present")
	}
	if len(updated.ModelStates) == 0 {
		t.Fatalf("expected active auth to inherit ModelStates")
	}
	state := updated.ModelStates[model]
	if state == nil {
		t.Fatalf("expected model state to be present")
	}
	if state.Quota.BackoffLevel != backoffLevel {
		t.Fatalf("expected BackoffLevel to be %d, got %d", backoffLevel, state.Quota.BackoffLevel)
	}
}
