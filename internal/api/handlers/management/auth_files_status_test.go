package management

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestPatchAuthFileStatusEnablesDisabledAuthByID(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	manager := coreauth.NewManager(nil, nil, nil)
	recoverAt := time.Now().Add(-time.Minute)
	record := &coreauth.Auth{
		ID:             "codex-disabled.json",
		FileName:       "codex-disabled.json",
		Provider:       "codex",
		Disabled:       true,
		Status:         coreauth.StatusDisabled,
		StatusMessage:  "disabled after quota exhausted",
		NextRetryAfter: recoverAt,
		Quota: coreauth.QuotaState{
			Exceeded:      true,
			Reason:        "codex_quota_auto_disabled",
			NextRecoverAt: recoverAt,
		},
	}
	if _, errRegister := manager.Register(context.Background(), record); errRegister != nil {
		t.Fatalf("failed to register auth record: %v", errRegister)
	}

	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPatch, "/v0/management/auth-files/status", strings.NewReader(`{"name":"codex-disabled.json","disabled":false}`))
	req.Header.Set("Content-Type", "application/json")
	ctx.Request = req

	h.PatchAuthFileStatus(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d with body %s", http.StatusOK, rec.Code, rec.Body.String())
	}
	updated, ok := manager.GetByID("codex-disabled.json")
	if !ok || updated == nil {
		t.Fatalf("expected auth record to exist after status patch")
	}
	if updated.Disabled {
		t.Fatalf("expected auth to be enabled")
	}
	if updated.Status != coreauth.StatusActive {
		t.Fatalf("status = %q, want %q", updated.Status, coreauth.StatusActive)
	}
	if updated.StatusMessage != "" {
		t.Fatalf("status message = %q, want empty", updated.StatusMessage)
	}
	if updated.Quota.Exceeded || updated.Quota.Reason != "" || !updated.NextRetryAfter.IsZero() {
		t.Fatalf("auto quota marker was not cleared: quota=%+v nextRetry=%s", updated.Quota, updated.NextRetryAfter)
	}
}

func TestPatchAuthFileStatusDisablesAutoQuotaAuthWithoutClearingRecoveryMarker(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	manager := coreauth.NewManager(nil, nil, nil)
	recoverAt := time.Now().Add(time.Hour)
	record := &coreauth.Auth{
		ID:             "codex-auto-disabled.json",
		FileName:       "codex-auto-disabled.json",
		Provider:       "codex",
		Disabled:       true,
		Status:         coreauth.StatusDisabled,
		StatusMessage:  "disabled after quota exhausted",
		NextRetryAfter: recoverAt,
		Quota: coreauth.QuotaState{
			Exceeded:      true,
			Reason:        "codex_quota_auto_disabled",
			NextRecoverAt: recoverAt,
		},
	}
	if _, errRegister := manager.Register(context.Background(), record); errRegister != nil {
		t.Fatalf("failed to register auth record: %v", errRegister)
	}

	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPatch, "/v0/management/auth-files/status", strings.NewReader(`{"name":"codex-auto-disabled.json","disabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	ctx.Request = req

	h.PatchAuthFileStatus(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d with body %s", http.StatusOK, rec.Code, rec.Body.String())
	}
	updated, ok := manager.GetByID("codex-auto-disabled.json")
	if !ok || updated == nil {
		t.Fatalf("expected auth record to exist after status patch")
	}
	if !updated.Disabled || updated.Status != coreauth.StatusDisabled {
		t.Fatalf("expected auth to remain disabled, got disabled=%v status=%q", updated.Disabled, updated.Status)
	}
	if updated.StatusMessage != "disabled via management API" {
		t.Fatalf("status message = %q, want management disabled message", updated.StatusMessage)
	}
	if !updated.Quota.Exceeded || updated.Quota.Reason != "codex_quota_auto_disabled" {
		t.Fatalf("auto quota marker was cleared: quota=%+v", updated.Quota)
	}
	if updated.Quota.NextRecoverAt.IsZero() || !updated.Quota.NextRecoverAt.Equal(recoverAt) {
		t.Fatalf("next recover at = %s, want %s", updated.Quota.NextRecoverAt, recoverAt)
	}
	if updated.NextRetryAfter.IsZero() || !updated.NextRetryAfter.Equal(recoverAt) {
		t.Fatalf("next retry after = %s, want %s", updated.NextRetryAfter, recoverAt)
	}
}
