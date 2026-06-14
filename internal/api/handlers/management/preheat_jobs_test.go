package management

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestPreheatJobRequestParsingAndStatusRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager := coreauth.NewManager(nil, nil, nil)
	auth := &coreauth.Auth{ID: "codex-one", Provider: "codex", Status: coreauth.StatusActive, Metadata: map[string]any{"type": "codex"}}
	if _, errRegister := manager.Register(context.Background(), auth); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)
	done := make(chan struct{})
	release := make(chan struct{})
	h.preheatJobs.preheatHook = func(context.Context, *coreauth.Auth) error {
		close(done)
		<-release
		return nil
	}

	body := `{"operation":"preheat","authIndices":["` + auth.EnsureIndex() + `"]}`
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v0/management/auth-files/preheat/jobs", strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	h.StartPreheatJob(ctx)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("start status = %d, want %d body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	var start map[string]any
	if errUnmarshal := json.Unmarshal(rec.Body.Bytes(), &start); errUnmarshal != nil {
		t.Fatalf("decode start response: %v", errUnmarshal)
	}
	jobID, _ := start["job_id"].(string)
	if jobID == "" {
		t.Fatalf("job_id missing from response: %#v", start)
	}
	if got := start["operation"]; got != preheatOperationPreheat {
		t.Fatalf("operation = %#v, want %q", got, preheatOperationPreheat)
	}
	if got := int(start["total"].(float64)); got != 1 {
		t.Fatalf("total = %d, want 1", got)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("preheat hook did not start")
	}

	statusRec := httptest.NewRecorder()
	statusCtx, _ := gin.CreateTestContext(statusRec)
	statusCtx.Params = gin.Params{{Key: "job_id", Value: jobID}}
	statusCtx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/auth-files/preheat/jobs/"+jobID, nil)
	h.GetPreheatJob(statusCtx)
	if statusRec.Code != http.StatusAccepted {
		t.Fatalf("running status route = %d, want %d body=%s", statusRec.Code, http.StatusAccepted, statusRec.Body.String())
	}

	close(release)
	waitForJobTerminal(t, h.preheatJobs, jobID)
	terminalRec := httptest.NewRecorder()
	terminalCtx, _ := gin.CreateTestContext(terminalRec)
	terminalCtx.Params = gin.Params{{Key: "job_id", Value: jobID}}
	terminalCtx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/auth-files/preheat/jobs/"+jobID, nil)
	h.GetPreheatJob(terminalCtx)
	if terminalRec.Code != http.StatusOK {
		t.Fatalf("terminal status route = %d, want %d body=%s", terminalRec.Code, http.StatusOK, terminalRec.Body.String())
	}
}

func TestPreheatJobUsesDetachedContext(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	auth := &coreauth.Auth{ID: "codex-detached", Provider: "codex", Status: coreauth.StatusActive, Metadata: map[string]any{"type": "codex"}}
	if _, errRegister := manager.Register(context.Background(), auth); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)
	seen := make(chan error, 1)
	h.preheatJobs.preheatHook = func(ctx context.Context, _ *coreauth.Auth) error {
		select {
		case <-ctx.Done():
			seen <- ctx.Err()
		default:
			seen <- nil
		}
		return nil
	}

	reqCtx, cancel := context.WithCancel(context.Background())
	cancel()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	body := `{"operation":"preheat","auth_index":"` + auth.EnsureIndex() + `"}`
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v0/management/auth-files/preheat/jobs", strings.NewReader(body)).WithContext(reqCtx)
	ctx.Request.Header.Set("Content-Type", "application/json")
	h.StartPreheatJob(ctx)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("start status = %d, want %d body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	select {
	case err := <-seen:
		if err != nil {
			t.Fatalf("job context was canceled by request: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("job did not run")
	}
}

func TestPreheatJobDedupesConcurrentAuthWork(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	auth := &coreauth.Auth{ID: "codex-dedupe", Provider: "codex", Status: coreauth.StatusActive, Metadata: map[string]any{"type": "codex"}}
	if _, errRegister := manager.Register(context.Background(), auth); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)
	started := make(chan struct{})
	release := make(chan struct{})
	var calls int
	var callsMu sync.Mutex
	h.preheatJobs.preheatHook = func(context.Context, *coreauth.Auth) error {
		callsMu.Lock()
		calls++
		callsMu.Unlock()
		select {
		case <-started:
		default:
			close(started)
		}
		<-release
		return nil
	}

	first := h.preheatJobs.startJob(preheatOperationPreheat, "manual", []*coreauth.Auth{auth})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first job did not start")
	}
	second := h.preheatJobs.startJob(preheatOperationPreheat, "manual", []*coreauth.Auth{auth})
	if second.Deduped != 1 || second.Completed != 1 {
		t.Fatalf("second job dedupe/completed = %d/%d, want 1/1", second.Deduped, second.Completed)
	}
	if got := second.Items[0].Status; got != preheatJobStatusSkipped {
		t.Fatalf("deduped item status = %q, want %q", got, preheatJobStatusSkipped)
	}
	close(release)
	waitForJobTerminal(t, h.preheatJobs, first.ID)
	callsMu.Lock()
	gotCalls := calls
	callsMu.Unlock()
	if gotCalls != 1 {
		t.Fatalf("preheat calls = %d, want 1", gotCalls)
	}
}

func TestPreheatJobStartReturnsStableSnapshot(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	auth := &coreauth.Auth{ID: "codex-snapshot", Provider: "codex", Status: coreauth.StatusActive, Metadata: map[string]any{"type": "codex"}}
	if _, errRegister := manager.Register(context.Background(), auth); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)
	h.preheatJobs.preheatHook = func(context.Context, *coreauth.Auth) error { return nil }

	snapshot := h.preheatJobs.startJob(preheatOperationPreheat, "manual", []*coreauth.Auth{auth})
	waitForJobTerminal(t, h.preheatJobs, snapshot.ID)

	if snapshot.Status != preheatJobStatusQueued {
		t.Fatalf("returned job snapshot status mutated to %q, want initial queued", snapshot.Status)
	}
	if snapshot.Completed != 0 {
		t.Fatalf("returned job snapshot completed mutated to %d, want 0", snapshot.Completed)
	}
	stored, ok := h.preheatJobs.job(snapshot.ID)
	if !ok {
		t.Fatal("stored job missing")
	}
	if stored.Status != preheatJobStatusSucceeded || stored.Completed != 1 {
		t.Fatalf("stored job = status %q completed %d, want succeeded/1", stored.Status, stored.Completed)
	}
}

func TestNormalizeCodexUsageRefreshStateRejectsWeeklyWindow(t *testing.T) {
	usage := map[string]any{
		"rate_limit": map[string]any{
			"primary_window": map[string]any{
				"limit_window_seconds": float64(604800),
				"reset_after_seconds":  float64(604800),
			},
		},
	}
	if _, err := normalizeCodexUsageRefreshState(usage, time.Unix(100, 0)); err == nil {
		t.Fatal("expected weekly 604800 window to be rejected")
	}
}

func TestPreheatJobRefreshTimeOperationUsesPlusFiveHourWindow(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	auth := &coreauth.Auth{ID: "codex-plus-refresh", Provider: "codex", Status: coreauth.StatusActive, Metadata: map[string]any{"type": "codex"}}
	if _, errRegister := manager.Register(context.Background(), auth); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)

	now := time.Unix(1_700_000_000, 0).UTC()
	fiveHourReset := now.Add(2 * time.Hour).UTC().Format(time.RFC3339)
	weeklyGateReset := now.Add(72 * time.Hour).UTC().Format(time.RFC3339)
	usage := map[string]any{
		"plan_type": "plus",
		"rate_limit": map[string]any{
			"primary_window": map[string]any{
				"limit_window_seconds": float64(604800),
				"used_percent":         float64(100),
				"reset_after_seconds":  float64(72 * 60 * 60),
			},
			"secondary_window": map[string]any{
				"limit_window_seconds": float64(5 * 60 * 60),
				"used_percent":         float64(100),
				"reset_after_seconds":  float64(2 * 60 * 60),
			},
		},
	}
	h.preheatJobs.refreshHook = func(context.Context, *coreauth.Auth) (codexRefreshState, error) {
		return normalizeCodexUsageRefreshState(usage, now)
	}

	snapshot := h.preheatJobs.startJob(preheatOperationRefreshTime, "manual", []*coreauth.Auth{auth})
	waitForJobTerminal(t, h.preheatJobs, snapshot.ID)
	stored, ok := h.preheatJobs.job(snapshot.ID)
	if !ok || stored == nil {
		t.Fatal("stored job missing")
	}
	if stored.Status != preheatJobStatusSucceeded {
		t.Fatalf("refresh_time job status = %q, want %q; item=%+v", stored.Status, preheatJobStatusSucceeded, stored.Items[0])
	}
	state := stored.Items[0].RefreshState
	if state == nil || !state.FetchedRefreshTime {
		t.Fatalf("refresh state = %+v, want fetched state", state)
	}
	if state.WeeklyResetAt != fiveHourReset {
		t.Fatalf("WeeklyResetAt = %q, want 5-hour reset %q", state.WeeklyResetAt, fiveHourReset)
	}
	if state.WeeklyGateResetAt != weeklyGateReset {
		t.Fatalf("WeeklyGateResetAt = %q, want weekly gate reset %q", state.WeeklyGateResetAt, weeklyGateReset)
	}
}

func TestPreheatAutoSkipsDueFiveHourWhenWeeklyGateFuture(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	now := time.Unix(1_700_000_000, 0).UTC()
	record := &coreauth.Auth{
		ID:       "codex-weekly-gated",
		Provider: "codex",
		Disabled: true,
		Status:   coreauth.StatusDisabled,
		Metadata: map[string]any{
			"fetched_refresh_time":    true,
			"exact_seven_day_refresh": true,
			"preheat_needed":          true,
			"weekly_gate_reset_at":    now.Add(time.Hour).UTC().Format(time.RFC3339),
		},
		Quota: coreauth.QuotaState{Exceeded: true, Reason: "codex_quota_auto_disabled", NextRecoverAt: now.Add(-time.Minute)},
	}
	if _, errRegister := manager.Register(context.Background(), record); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)
	due := h.preheatJobs.dueAutoAuths(now)
	if len(due) != 0 {
		t.Fatalf("due auto auths = %d, want 0 while weekly gate is in the future", len(due))
	}
}

func TestPreheatAutoRunsDueAutoDisabledCodexAuthAtFetchedRefreshTime(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	now := time.Now().UTC()
	recoverAt := now.Add(-time.Minute)
	auth := &coreauth.Auth{
		ID:             "codex-auto-due",
		Provider:       "codex",
		Disabled:       true,
		Status:         coreauth.StatusDisabled,
		StatusMessage:  "disabled after quota exhausted",
		NextRetryAfter: recoverAt,
		Metadata: map[string]any{
			"fetched_refresh_time":    true,
			"exact_seven_day_refresh": false,
			"preheat_needed":          false,
			"weekly_reset_at":         recoverAt.Format(time.RFC3339),
		},
		Quota: coreauth.QuotaState{Exceeded: true, Reason: "codex_quota_auto_disabled", NextRecoverAt: recoverAt},
	}
	if _, errRegister := manager.Register(context.Background(), auth); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)

	var preheatCalls int
	h.preheatJobs.preheatHook = func(_ context.Context, got *coreauth.Auth) error {
		if got == nil || got.ID != auth.ID {
			return fmt.Errorf("preheat auth = %v, want %s", got, auth.ID)
		}
		if got.Disabled || got.Status == coreauth.StatusDisabled {
			return fmt.Errorf("preheat auth remained disabled: disabled=%v status=%q", got.Disabled, got.Status)
		}
		preheatCalls++
		return nil
	}
	h.preheatJobs.refreshHook = func(_ context.Context, got *coreauth.Auth) (codexRefreshState, error) {
		if got == nil || got.ID != auth.ID {
			return codexRefreshState{}, fmt.Errorf("refresh auth = %v, want %s", got, auth.ID)
		}
		return codexRefreshState{
			FetchedRefreshTime:   true,
			ExactSevenDayRefresh: false,
			PreheatNeeded:        false,
			WeeklyResetAt:        now.Add(time.Hour).UTC().Format(time.RFC3339),
			FetchedAt:            now.UTC().Format(time.RFC3339),
		}, nil
	}

	h.preheatJobs.mu.Lock()
	h.preheatJobs.auto.Enabled = true
	h.preheatJobs.mu.Unlock()
	h.preheatJobs.scanAutoNow()

	auto := h.preheatJobs.autoSnapshot()
	if auto.LastJobID == "" {
		t.Fatal("auto scan did not start a job")
	}
	waitForJobTerminal(t, h.preheatJobs, auto.LastJobID)
	job, ok := h.preheatJobs.job(auto.LastJobID)
	if !ok || job == nil {
		t.Fatal("auto job missing")
	}
	if job.Operation != preheatOperationPreheatRefresh || job.Source != "auto" || job.Status != preheatJobStatusSucceeded {
		t.Fatalf("auto job = operation %q source %q status %q, want auto preheat_refresh succeeded", job.Operation, job.Source, job.Status)
	}
	if preheatCalls != 1 {
		t.Fatalf("preheat calls = %d, want 1", preheatCalls)
	}
	updated, ok := manager.GetByID(auth.ID)
	if !ok || updated == nil {
		t.Fatal("updated auth missing")
	}
	if updated.Disabled || updated.Status != coreauth.StatusActive || updated.Quota.Exceeded || updated.Quota.Reason != "" || !updated.NextRetryAfter.IsZero() {
		t.Fatalf("updated auth = disabled:%v status:%q quota:%+v nextRetry:%s, want active with cleared quota", updated.Disabled, updated.Status, updated.Quota, updated.NextRetryAfter)
	}
}

func TestPreheatAutoRunsQueuedSecondItemWhenFirstItemDeduped(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	now := time.Now().UTC()
	recoverAt := now.Add(-time.Minute)
	busy := &coreauth.Auth{ID: "codex-busy", Provider: "codex", Status: coreauth.StatusActive, Metadata: map[string]any{"type": "codex"}}
	dueSecond := &coreauth.Auth{
		ID:             "codex-due-second",
		Provider:       "codex",
		Disabled:       true,
		Status:         coreauth.StatusDisabled,
		StatusMessage:  "disabled after quota exhausted",
		NextRetryAfter: recoverAt,
		Metadata: map[string]any{
			"fetched_refresh_time":    true,
			"exact_seven_day_refresh": false,
			"preheat_needed":          false,
			"weekly_reset_at":         recoverAt.Format(time.RFC3339),
		},
		Quota: coreauth.QuotaState{Exceeded: true, Reason: "codex_quota_auto_disabled", NextRecoverAt: recoverAt},
	}
	if _, errRegister := manager.Register(context.Background(), busy); errRegister != nil {
		t.Fatalf("register busy auth: %v", errRegister)
	}
	if _, errRegister := manager.Register(context.Background(), dueSecond); errRegister != nil {
		t.Fatalf("register due auth: %v", errRegister)
	}
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)

	busyStarted := make(chan struct{})
	releaseBusy := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-releaseBusy:
		default:
			close(releaseBusy)
		}
	})
	var busyStartedOnce sync.Once
	var callsMu sync.Mutex
	duePreheatCalls := 0
	h.preheatJobs.preheatHook = func(_ context.Context, auth *coreauth.Auth) error {
		if auth == nil {
			return fmt.Errorf("preheat auth is nil")
		}
		switch auth.ID {
		case busy.ID:
			busyStartedOnce.Do(func() { close(busyStarted) })
			<-releaseBusy
			return nil
		case dueSecond.ID:
			callsMu.Lock()
			duePreheatCalls++
			callsMu.Unlock()
			return nil
		default:
			return fmt.Errorf("unexpected preheat auth %q", auth.ID)
		}
	}
	h.preheatJobs.refreshHook = func(_ context.Context, auth *coreauth.Auth) (codexRefreshState, error) {
		if auth == nil || auth.ID != dueSecond.ID {
			return codexRefreshState{}, fmt.Errorf("refresh auth = %v, want %s", auth, dueSecond.ID)
		}
		return codexRefreshState{
			FetchedRefreshTime:   true,
			ExactSevenDayRefresh: false,
			PreheatNeeded:        false,
			WeeklyResetAt:        now.Add(time.Hour).UTC().Format(time.RFC3339),
			FetchedAt:            now.UTC().Format(time.RFC3339),
		}, nil
	}

	busyJob := h.preheatJobs.startJob(preheatOperationPreheat, "manual", []*coreauth.Auth{busy})
	select {
	case <-busyStarted:
	case <-time.After(time.Second):
		t.Fatal("busy preheat job did not start")
	}

	autoJob := h.preheatJobs.startJob(preheatOperationPreheatRefresh, "auto", []*coreauth.Auth{busy, dueSecond})
	if len(autoJob.Items) != 2 {
		t.Fatalf("auto job items = %d, want 2", len(autoJob.Items))
	}
	if autoJob.Items[0].Status != preheatJobStatusSkipped || !autoJob.Items[0].Deduped {
		t.Fatalf("first auto item = status %q deduped %v, want skipped deduped", autoJob.Items[0].Status, autoJob.Items[0].Deduped)
	}
	if autoJob.Items[1].Status != preheatJobStatusQueued {
		t.Fatalf("second auto item = status %q, want queued", autoJob.Items[1].Status)
	}

	close(releaseBusy)
	waitForJobTerminal(t, h.preheatJobs, autoJob.ID)
	waitForJobTerminal(t, h.preheatJobs, busyJob.ID)
	stored, ok := h.preheatJobs.job(autoJob.ID)
	if !ok || stored == nil || len(stored.Items) != 2 {
		t.Fatalf("stored auto job missing or malformed: %#v", stored)
	}
	if stored.Items[0].Status != preheatJobStatusSkipped {
		t.Fatalf("first auto item status = %q, want skipped", stored.Items[0].Status)
	}
	if stored.Items[1].Status != preheatJobStatusSucceeded {
		t.Fatalf("second auto item status = %q, want succeeded", stored.Items[1].Status)
	}
	callsMu.Lock()
	gotDuePreheatCalls := duePreheatCalls
	callsMu.Unlock()
	if gotDuePreheatCalls != 1 {
		t.Fatalf("due second preheat calls = %d, want 1", gotDuePreheatCalls)
	}
	updated, ok := manager.GetByID(dueSecond.ID)
	if !ok || updated == nil {
		t.Fatal("updated due auth missing")
	}
	if updated.Disabled || updated.Status != coreauth.StatusActive || updated.Quota.Exceeded || updated.Quota.Reason != "" || !updated.NextRetryAfter.IsZero() {
		t.Fatalf("updated due auth = disabled:%v status:%q quota:%+v nextRetry:%s, want active with cleared quota", updated.Disabled, updated.Status, updated.Quota, updated.NextRetryAfter)
	}
}

func TestPersistCodexRefreshStateOnlyWritesRefreshStateFields(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	auth := &coreauth.Auth{
		ID:       "codex-refresh",
		Provider: "codex",
		Metadata: map[string]any{
			"type":                    "codex",
			"access_token":            "keep-token",
			"fetched_refresh_time":    false,
			"exact_seven_day_refresh": true,
			"preheat_needed":          true,
			"weekly_reset_at":         "old",
			"weekly_gate_reset_at":    "old-gate",
			"fetched_at":              "old",
		},
	}
	if _, errRegister := manager.Register(context.Background(), auth); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)
	state := codexRefreshState{
		FetchedRefreshTime:   true,
		ExactSevenDayRefresh: false,
		PreheatNeeded:        false,
		WeeklyResetAt:        "2026-06-08T12:00:00Z",
		WeeklyGateResetAt:    "2026-06-09T12:00:00Z",
		FetchedAt:            "2026-06-01T12:00:00Z",
	}
	if err := h.persistCodexRefreshState(context.Background(), auth.ID, state); err != nil {
		t.Fatalf("persist refresh state: %v", err)
	}
	updated, ok := manager.GetByID(auth.ID)
	if !ok || updated == nil {
		t.Fatal("updated auth missing")
	}
	if got := updated.Metadata["access_token"]; got != "keep-token" {
		t.Fatalf("access_token = %#v, want preserved", got)
	}
	if got := updated.Metadata["fetched_refresh_time"]; got != true {
		t.Fatalf("fetched_refresh_time = %#v, want true", got)
	}
	if got := updated.Metadata["exact_seven_day_refresh"]; got != false {
		t.Fatalf("exact_seven_day_refresh = %#v, want false", got)
	}
	if got := updated.Metadata["preheat_needed"]; got != false {
		t.Fatalf("preheat_needed = %#v, want false", got)
	}
	if got := updated.Metadata["weekly_reset_at"]; got != state.WeeklyResetAt {
		t.Fatalf("weekly_reset_at = %#v, want %q", got, state.WeeklyResetAt)
	}
	if got := updated.Metadata["weekly_gate_reset_at"]; got != state.WeeklyGateResetAt {
		t.Fatalf("weekly_gate_reset_at = %#v, want %q", got, state.WeeklyGateResetAt)
	}
	if got := updated.Metadata["fetched_at"]; got != state.FetchedAt {
		t.Fatalf("fetched_at = %#v, want %q", got, state.FetchedAt)
	}
}

func TestUsageStartLimiterSpacesStartsByOneSecond(t *testing.T) {
	base := time.Unix(1000, 0)
	var sleeps []time.Duration
	limiter := &usageStartLimiter{
		interval:  time.Second,
		nextStart: base.Add(time.Second),
		now: func() time.Time {
			return base
		},
		sleep: func(d time.Duration) {
			sleeps = append(sleeps, d)
		},
	}
	if err := limiter.wait(context.Background()); err != nil {
		t.Fatalf("wait returned error: %v", err)
	}
	if len(sleeps) != 1 || sleeps[0] != time.Second {
		t.Fatalf("sleeps = %v, want [1s]", sleeps)
	}
}

func TestUsageStartLimiterConcurrentReservationsAreSpaced(t *testing.T) {
	base := time.Unix(2000, 0)
	const callers = 4
	var mu sync.Mutex
	sleeps := make([]time.Duration, 0, callers-1)
	limiter := &usageStartLimiter{
		interval: 10 * time.Millisecond,
		now: func() time.Time {
			return base
		},
		sleep: func(d time.Duration) {
			mu.Lock()
			sleeps = append(sleeps, d)
			mu.Unlock()
		},
	}

	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- limiter.wait(context.Background())
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("wait returned error: %v", err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(sleeps) != callers-1 {
		t.Fatalf("recorded sleeps = %v, want %d delayed callers", sleeps, callers-1)
	}
	want := map[time.Duration]int{
		10 * time.Millisecond: 1,
		20 * time.Millisecond: 1,
		30 * time.Millisecond: 1,
	}
	for _, sleep := range sleeps {
		want[sleep]--
	}
	for duration, count := range want {
		if count != 0 {
			t.Fatalf("sleep reservations = %v, missing/extra count for %s: %d", sleeps, duration, count)
		}
	}
}

func TestUsageStartLimiterCanceledWaitReturnsError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	base := time.Unix(3000, 0)
	limiter := &usageStartLimiter{
		interval:  time.Second,
		nextStart: base.Add(time.Second),
		now:       func() time.Time { return base },
		sleep: func(time.Duration) {
			t.Fatal("sleep should not run after context cancellation")
		},
	}
	if err := limiter.wait(ctx); err == nil {
		t.Fatal("expected canceled wait to return an error")
	}
}

func waitForJobTerminal(t *testing.T, manager *preheatJobManager, jobID string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		job, ok := manager.job(jobID)
		if ok && isTerminalJobStatus(job.Status) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	job, _ := manager.job(jobID)
	t.Fatalf("job %s did not become terminal: %#v", jobID, job)
}
