package management

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

const (
	preheatJobStatusQueued    = "queued"
	preheatJobStatusRunning   = "running"
	preheatJobStatusSucceeded = "succeeded"
	preheatJobStatusFailed    = "failed"
	preheatJobStatusSkipped   = "skipped"
	preheatJobStatusPartial   = "partial"

	preheatOperationPreheat        = "preheat"
	preheatOperationRefreshTime    = "refresh_time"
	preheatOperationPreheatRefresh = "preheat_refresh"

	codexUsageURL              = "https://chatgpt.com/backend-api/wham/usage"
	codexUsageUserAgent        = "codex_cli_rs/0.76.0 (Debian 13.0.0; x86_64) WindowsTerminal"
	fiveHourQuotaWindowSeconds = 5 * 60 * 60
	weeklyQuotaWindowSeconds   = 7 * 24 * 60 * 60
)

var monthlyQuotaWindowSeconds = map[int64]struct{}{
	2419200: {},
	2505600: {},
	2592000: {},
	2678400: {},
}

type preheatJobManager struct {
	h *Handler

	mu       sync.Mutex
	jobs     map[string]*preheatJob
	inFlight map[string]string
	auto     preheatAutoState
	stopAuto chan struct{}

	usageLimiter *usageStartLimiter

	preheatHook func(context.Context, *coreauth.Auth) error
	refreshHook func(context.Context, *coreauth.Auth) (codexRefreshState, error)
}

type usageStartLimiter struct {
	mu        sync.Mutex
	nextStart time.Time
	interval  time.Duration
	now       func() time.Time
	sleep     func(time.Duration)
}

type preheatAutoState struct {
	Enabled   bool       `json:"enabled"`
	Busy      bool       `json:"busy"`
	LastJobID string     `json:"last_job_id"`
	LastError string     `json:"last_error"`
	LastRunAt *time.Time `json:"last_run_at"`
	NextRunAt *time.Time `json:"next_run_at"`
}

type preheatJob struct {
	ID        string            `json:"job_id"`
	Status    string            `json:"status"`
	Operation string            `json:"operation"`
	Source    string            `json:"source,omitempty"`
	Total     int               `json:"total"`
	Completed int               `json:"completed"`
	Failed    int               `json:"failed"`
	Deduped   int               `json:"deduped"`
	PollURL   string            `json:"poll_url"`
	Items     []*preheatJobItem `json:"items"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
	StartedAt *time.Time        `json:"started_at,omitempty"`
	EndedAt   *time.Time        `json:"ended_at,omitempty"`
}

type preheatJobItem struct {
	AuthID       string             `json:"auth_id"`
	AuthIndex    string             `json:"auth_index"`
	Status       string             `json:"status"`
	Error        string             `json:"error,omitempty"`
	Deduped      bool               `json:"deduped,omitempty"`
	RefreshState *codexRefreshState `json:"refresh_state,omitempty"`
	StartedAt    *time.Time         `json:"started_at,omitempty"`
	EndedAt      *time.Time         `json:"ended_at,omitempty"`
}

type codexRefreshState struct {
	FetchedRefreshTime   bool   `json:"fetched_refresh_time"`
	ExactSevenDayRefresh bool   `json:"exact_seven_day_refresh"`
	PreheatNeeded        bool   `json:"preheat_needed"`
	WeeklyResetAt        string `json:"weekly_reset_at"`
	WeeklyGateResetAt    string `json:"weekly_gate_reset_at"`
	FetchedAt            string `json:"fetched_at"`
}

type codexQuotaWindow struct {
	window  map[string]any
	seconds int64
}

type startPreheatJobRequest struct {
	Operation       string   `json:"operation"`
	AuthIndex       string   `json:"auth_index"`
	AuthIndexCamel  string   `json:"authIndex"`
	AuthIndexPascal string   `json:"AuthIndex"`
	AuthIndices     []string `json:"auth_indices"`
	AuthIndicesAlt  []string `json:"authIndices"`
	AuthIndexes     []string `json:"authIndexes"`
}

func newPreheatJobManager(h *Handler) *preheatJobManager {
	return &preheatJobManager{
		h:            h,
		jobs:         make(map[string]*preheatJob),
		inFlight:     make(map[string]string),
		usageLimiter: &usageStartLimiter{interval: time.Second, sleep: time.Sleep},
	}
}

func (h *Handler) StartPreheatJob(c *gin.Context) {
	if h == nil || h.authManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "core auth manager unavailable"})
		return
	}
	if h.preheatJobs == nil {
		h.preheatJobs = newPreheatJobManager(h)
	}
	var req startPreheatJobRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	operation := strings.ToLower(strings.TrimSpace(req.Operation))
	if !validPreheatOperation(operation) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid operation"})
		return
	}
	auths, err := h.codexAuthsForJob(req.authIndexes())
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(auths) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "auth not found"})
		return
	}
	job := h.preheatJobs.startJob(operation, "manual", auths)
	c.JSON(http.StatusAccepted, job.snapshot())
}

func (h *Handler) GetPreheatJob(c *gin.Context) {
	if h == nil || h.preheatJobs == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "job not found"})
		return
	}
	job, ok := h.preheatJobs.job(c.Param("job_id"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "job not found"})
		return
	}
	status := http.StatusOK
	if !isTerminalJobStatus(job.Status) {
		status = http.StatusAccepted
	}
	c.JSON(status, job.snapshot())
}

func (h *Handler) GetPreheatAuto(c *gin.Context) {
	if h == nil || h.preheatJobs == nil {
		c.JSON(http.StatusOK, preheatAutoState{})
		return
	}
	c.JSON(http.StatusOK, h.preheatJobs.autoSnapshot())
}

func (h *Handler) PatchPreheatAuto(c *gin.Context) {
	if h == nil || h.authManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "core auth manager unavailable"})
		return
	}
	if h.preheatJobs == nil {
		h.preheatJobs = newPreheatJobManager(h)
	}
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	state := h.preheatJobs.setAutoEnabled(*req.Enabled)
	c.JSON(http.StatusOK, state)
}

func (r startPreheatJobRequest) authIndexes() []string {
	indexes := make([]string, 0, 1+len(r.AuthIndices)+len(r.AuthIndicesAlt)+len(r.AuthIndexes))
	for _, value := range []string{r.AuthIndex, r.AuthIndexCamel, r.AuthIndexPascal} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			indexes = append(indexes, trimmed)
		}
	}
	indexes = append(indexes, r.AuthIndices...)
	indexes = append(indexes, r.AuthIndicesAlt...)
	indexes = append(indexes, r.AuthIndexes...)
	return uniqueStrings(indexes)
}

func (h *Handler) codexAuthsForJob(indexes []string) ([]*coreauth.Auth, error) {
	if len(indexes) == 0 {
		return nil, fmt.Errorf("missing auth_index")
	}
	auths := make([]*coreauth.Auth, 0, len(indexes))
	for _, index := range indexes {
		auth := h.authByIndex(index)
		if auth == nil {
			return nil, fmt.Errorf("auth not found: %s", index)
		}
		if !strings.EqualFold(strings.TrimSpace(auth.Provider), "codex") {
			return nil, fmt.Errorf("auth is not codex: %s", index)
		}
		auth.EnsureIndex()
		auths = append(auths, auth)
	}
	return auths, nil
}

func validPreheatOperation(operation string) bool {
	switch operation {
	case preheatOperationPreheat, preheatOperationRefreshTime, preheatOperationPreheatRefresh:
		return true
	default:
		return false
	}
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func (m *preheatJobManager) startJob(operation, source string, auths []*coreauth.Auth) *preheatJob {
	job := &preheatJob{ID: newJobID(), Status: preheatJobStatusQueued, Operation: operation, Source: source, Total: len(auths), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	job.PollURL = "/v0/management/auth-files/preheat/jobs/" + job.ID
	job.Items = make([]*preheatJobItem, 0, len(auths))

	m.mu.Lock()
	for _, auth := range auths {
		if auth == nil {
			continue
		}
		auth.EnsureIndex()
		item := &preheatJobItem{AuthID: auth.ID, AuthIndex: auth.Index, Status: preheatJobStatusQueued}
		if _, exists := m.inFlight[auth.ID]; exists {
			item.Status = preheatJobStatusSkipped
			item.Deduped = true
			item.EndedAt = timePtr(time.Now().UTC())
			job.Completed++
			job.Deduped++
		} else {
			m.inFlight[auth.ID] = job.ID
		}
		job.Items = append(job.Items, item)
	}
	m.jobs[job.ID] = job
	snapshot := job.clone()
	m.mu.Unlock()

	go m.runJob(context.Background(), job.ID)
	return snapshot
}

func (m *preheatJobManager) runJob(ctx context.Context, jobID string) {
	m.updateJob(jobID, func(job *preheatJob) {
		now := time.Now().UTC()
		job.Status = preheatJobStatusRunning
		job.StartedAt = &now
		job.UpdatedAt = now
	})

	var wg sync.WaitGroup
	for _, idx := range m.itemsForRun(jobID) {
		idx := idx
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.runItem(ctx, jobID, idx)
		}()
	}
	wg.Wait()

	m.updateJob(jobID, func(job *preheatJob) {
		now := time.Now().UTC()
		if job.Failed > 0 && job.Completed > job.Failed {
			job.Status = preheatJobStatusPartial
		} else if job.Failed > 0 {
			job.Status = preheatJobStatusFailed
		} else {
			job.Status = preheatJobStatusSucceeded
		}
		job.EndedAt = &now
		job.UpdatedAt = now
	})
}

func (m *preheatJobManager) itemsForRun(jobID string) []int {
	m.mu.Lock()
	defer m.mu.Unlock()
	job := m.jobs[jobID]
	if job == nil {
		return nil
	}
	out := make([]int, 0, len(job.Items))
	for idx, item := range job.Items {
		if item != nil && item.Status == preheatJobStatusQueued && !item.Deduped {
			out = append(out, idx)
		}
	}
	return out
}

func (m *preheatJobManager) runItem(ctx context.Context, jobID string, itemIndex int) {
	var authID string
	m.updateItem(jobID, itemIndex, func(item *preheatJobItem) {
		now := time.Now().UTC()
		item.Status = preheatJobStatusRunning
		item.StartedAt = &now
		authID = item.AuthID
	})
	defer func() {
		m.mu.Lock()
		delete(m.inFlight, authID)
		m.mu.Unlock()
	}()

	err := m.runAuthOperation(ctx, jobID, itemIndex, authID)
	m.updateItem(jobID, itemIndex, func(item *preheatJobItem) {
		now := time.Now().UTC()
		item.EndedAt = &now
		if err != nil {
			item.Status = preheatJobStatusFailed
			item.Error = err.Error()
			return
		}
		item.Status = preheatJobStatusSucceeded
	})
}

func (m *preheatJobManager) runAuthOperation(ctx context.Context, jobID string, itemIndex int, authID string) error {
	if m == nil || m.h == nil || m.h.authManager == nil {
		return fmt.Errorf("auth manager unavailable")
	}
	job, ok := m.job(jobID)
	if !ok {
		return fmt.Errorf("job not found")
	}
	auth, ok := m.h.authManager.GetByID(authID)
	if !ok || auth == nil {
		return fmt.Errorf("auth not found")
	}
	if job.Source == "auto" {
		var err error
		auth, err = m.prepareAutoAuth(ctx, auth.ID)
		if err != nil {
			return err
		}
		if auth == nil {
			return fmt.Errorf("auth skipped")
		}
	}

	switch job.Operation {
	case preheatOperationPreheat:
		return m.preheat(ctx, auth)
	case preheatOperationRefreshTime:
		state, err := m.refresh(ctx, auth)
		if err == nil {
			m.setItemRefreshState(jobID, itemIndex, state)
		}
		return err
	case preheatOperationPreheatRefresh:
		if err := m.preheat(ctx, auth); err != nil {
			return err
		}
		latest, ok := m.h.authManager.GetByID(auth.ID)
		if ok && latest != nil {
			auth = latest
		}
		state, err := m.refresh(ctx, auth)
		if err == nil {
			m.setItemRefreshState(jobID, itemIndex, state)
		}
		return err
	default:
		return fmt.Errorf("invalid operation")
	}
}

func (m *preheatJobManager) prepareAutoAuth(ctx context.Context, authID string) (*coreauth.Auth, error) {
	auth, ok := m.h.authManager.GetByID(authID)
	if !ok || auth == nil {
		return nil, fmt.Errorf("auth not found")
	}
	if authQuotaAutoDisabled(auth) {
		coreauth.ClearAutoQuotaDisabledState(auth)
		auth.Disabled = false
		auth.Status = coreauth.StatusActive
		auth.StatusMessage = ""
		auth.UpdatedAt = time.Now().UTC()
		updated, err := m.h.authManager.Update(ctx, auth)
		if err != nil {
			return nil, err
		}
		return updated, nil
	}
	if auth.Disabled || auth.Status == coreauth.StatusDisabled {
		return nil, fmt.Errorf("auth manually disabled")
	}
	return auth, nil
}

func (m *preheatJobManager) preheat(ctx context.Context, auth *coreauth.Auth) error {
	if m.preheatHook != nil {
		return m.preheatHook(ctx, auth)
	}
	return m.h.preheatCodexAuth(ctx, auth)
}

func (m *preheatJobManager) refresh(ctx context.Context, auth *coreauth.Auth) (codexRefreshState, error) {
	if m.refreshHook != nil {
		return m.refreshHook(ctx, auth)
	}
	return m.h.fetchAndPersistCodexRefreshState(ctx, auth, m.usageLimiter)
}

func (m *preheatJobManager) updateJob(jobID string, fn func(*preheatJob)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if job := m.jobs[jobID]; job != nil {
		fn(job)
	}
}

func (m *preheatJobManager) updateItem(jobID string, itemIndex int, fn func(*preheatJobItem)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job := m.jobs[jobID]
	if job == nil || itemIndex < 0 || itemIndex >= len(job.Items) || job.Items[itemIndex] == nil {
		return
	}
	before := job.Items[itemIndex].Status
	fn(job.Items[itemIndex])
	after := job.Items[itemIndex].Status
	if before != after {
		if after == preheatJobStatusSucceeded || after == preheatJobStatusSkipped || after == preheatJobStatusFailed {
			job.Completed++
		}
		if after == preheatJobStatusFailed {
			job.Failed++
		}
	}
	job.UpdatedAt = time.Now().UTC()
}

func (m *preheatJobManager) setItemRefreshState(jobID string, itemIndex int, state codexRefreshState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job := m.jobs[jobID]
	if job == nil || itemIndex < 0 || itemIndex >= len(job.Items) || job.Items[itemIndex] == nil {
		return
	}
	job.Items[itemIndex].RefreshState = &state
	job.UpdatedAt = time.Now().UTC()
}

func (m *preheatJobManager) job(jobID string) (*preheatJob, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job := m.jobs[strings.TrimSpace(jobID)]
	if job == nil {
		return nil, false
	}
	return job.clone(), true
}

func (m *preheatJobManager) autoSnapshot() preheatAutoState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneAutoState(m.auto)
}

func (m *preheatJobManager) setAutoEnabled(enabled bool) preheatAutoState {
	m.mu.Lock()
	if enabled == m.auto.Enabled {
		state := cloneAutoState(m.auto)
		m.mu.Unlock()
		if enabled {
			go m.scanAutoNow()
		}
		return state
	}
	m.auto.Enabled = enabled
	if enabled {
		m.stopAuto = make(chan struct{})
		next := time.Now().UTC()
		m.auto.NextRunAt = &next
		go m.autoLoop(m.stopAuto)
		go m.scanAutoNow()
	} else if m.stopAuto != nil {
		close(m.stopAuto)
		m.stopAuto = nil
		m.auto.NextRunAt = nil
	}
	state := cloneAutoState(m.auto)
	m.mu.Unlock()
	return state
}

func (m *preheatJobManager) autoLoop(stop <-chan struct{}) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			m.scanAutoNow()
		}
	}
}

func (m *preheatJobManager) scanAutoNow() {
	m.mu.Lock()
	if !m.auto.Enabled || m.auto.Busy {
		m.mu.Unlock()
		return
	}
	m.auto.Busy = true
	now := time.Now().UTC()
	m.auto.LastRunAt = &now
	next := now.Add(time.Minute)
	m.auto.NextRunAt = &next
	m.mu.Unlock()

	jobID := ""
	errText := ""
	due := m.dueAutoAuths(time.Now())
	if len(due) > 0 {
		jobSnapshot := m.startJob(preheatOperationPreheatRefresh, "auto", due)
		jobID = jobSnapshot.ID
	}

	m.mu.Lock()
	m.auto.Busy = false
	m.auto.LastJobID = jobID
	m.auto.LastError = errText
	m.mu.Unlock()
}

func (m *preheatJobManager) dueAutoAuths(now time.Time) []*coreauth.Auth {
	if m == nil || m.h == nil || m.h.authManager == nil {
		return nil
	}
	out := make([]*coreauth.Auth, 0)
	for _, auth := range m.h.authManager.List() {
		if auth == nil || !strings.EqualFold(strings.TrimSpace(auth.Provider), "codex") {
			continue
		}
		if auth.Disabled || auth.Status == coreauth.StatusDisabled {
			if !authQuotaAutoDisabled(auth) {
				continue
			}
		}
		state := refreshStateFromAuth(auth)
		if !state.FetchedRefreshTime {
			continue
		}
		if state.WeeklyGateResetAt != "" {
			gateAt, err := time.Parse(time.RFC3339, state.WeeklyGateResetAt)
			if err == nil && gateAt.After(now) {
				continue
			}
		}
		if state.ExactSevenDayRefresh && state.PreheatNeeded {
			out = append(out, auth)
			continue
		}
		if state.WeeklyResetAt == "" {
			continue
		}
		resetAt, err := time.Parse(time.RFC3339, state.WeeklyResetAt)
		if err == nil && !resetAt.After(now) {
			out = append(out, auth)
		}
	}
	return out
}

func cloneAutoState(in preheatAutoState) preheatAutoState {
	out := in
	if in.LastRunAt != nil {
		v := *in.LastRunAt
		out.LastRunAt = &v
	}
	if in.NextRunAt != nil {
		v := *in.NextRunAt
		out.NextRunAt = &v
	}
	return out
}

func (j *preheatJob) clone() *preheatJob {
	if j == nil {
		return nil
	}
	out := *j
	out.Items = make([]*preheatJobItem, len(j.Items))
	for i, item := range j.Items {
		if item == nil {
			continue
		}
		copyItem := *item
		if item.RefreshState != nil {
			state := *item.RefreshState
			copyItem.RefreshState = &state
		}
		out.Items[i] = &copyItem
	}
	return &out
}

func (j *preheatJob) snapshot() gin.H {
	return gin.H{
		"job_id":     j.ID,
		"status":     j.Status,
		"operation":  j.Operation,
		"source":     j.Source,
		"total":      j.Total,
		"completed":  j.Completed,
		"failed":     j.Failed,
		"deduped":    j.Deduped,
		"poll_url":   j.PollURL,
		"items":      j.Items,
		"created_at": j.CreatedAt,
		"updated_at": j.UpdatedAt,
		"started_at": j.StartedAt,
		"ended_at":   j.EndedAt,
	}
}

func isTerminalJobStatus(status string) bool {
	switch status {
	case preheatJobStatusSucceeded, preheatJobStatusFailed, preheatJobStatusPartial:
		return true
	default:
		return false
	}
}

func newJobID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("job-%d", time.Now().UnixNano())
	}
	return "job-" + hex.EncodeToString(b[:])
}

func timePtr(t time.Time) *time.Time { return &t }

func (h *Handler) fetchAndPersistCodexRefreshState(ctx context.Context, auth *coreauth.Auth, limiter *usageStartLimiter) (codexRefreshState, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if h == nil || h.authManager == nil || auth == nil {
		return codexRefreshState{}, fmt.Errorf("auth manager unavailable")
	}
	token, errToken := h.resolveTokenForAuth(ctx, auth)
	if errToken != nil {
		return codexRefreshState{}, fmt.Errorf("auth token refresh failed")
	}
	if strings.TrimSpace(token) == "" {
		return codexRefreshState{}, fmt.Errorf("auth token not found")
	}
	req, errReq := http.NewRequestWithContext(ctx, http.MethodGet, codexUsageURL, nil)
	if errReq != nil {
		return codexRefreshState{}, errReq
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", codexUsageUserAgent)
	if accountID := chatGPTAccountIDForAuth(auth); accountID != "" {
		req.Header.Set("Chatgpt-Account-Id", accountID)
	}
	client := &http.Client{Transport: h.apiCallTransport(auth)}
	if limiter != nil {
		if errWait := limiter.wait(ctx); errWait != nil {
			return codexRefreshState{}, errWait
		}
	}
	resp, errDo := client.Do(req)
	if errDo != nil {
		log.WithError(errDo).Debug("codex usage refresh request failed")
		return codexRefreshState{}, fmt.Errorf("request failed")
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			log.Errorf("response body close error: %v", errClose)
		}
	}()
	body, errRead := io.ReadAll(resp.Body)
	if errRead != nil {
		return codexRefreshState{}, fmt.Errorf("failed to read response")
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return codexRefreshState{}, fmt.Errorf("usage request failed with status %d", resp.StatusCode)
	}
	var usage map[string]any
	if errUnmarshal := json.Unmarshal(body, &usage); errUnmarshal != nil {
		return codexRefreshState{}, fmt.Errorf("parse Codex refresh time failed")
	}
	state, errState := normalizeCodexUsageRefreshState(usage, time.Now())
	if errState != nil {
		return codexRefreshState{}, errState
	}
	if errPersist := h.persistCodexRefreshState(ctx, auth.ID, state); errPersist != nil {
		return codexRefreshState{}, errPersist
	}
	return state, nil
}

func (l *usageStartLimiter) wait(ctx context.Context) error {
	if l == nil {
		return nil
	}
	nowFunc := l.now
	if nowFunc == nil {
		nowFunc = time.Now
	}
	l.mu.Lock()
	now := nowFunc()
	reserved := now
	if l.nextStart.After(now) {
		reserved = l.nextStart
	}
	l.nextStart = reserved.Add(l.interval)
	l.mu.Unlock()

	if wait := reserved.Sub(now); wait > 0 {
		if ctx != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
		}
		if l.sleep != nil {
			l.sleep(wait)
		} else {
			timer := time.NewTimer(wait)
			defer timer.Stop()
			if ctx == nil {
				<-timer.C
			} else {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-timer.C:
				}
			}
		}
	}
	if ctx != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
	return nil
}

func normalizeCodexUsageRefreshState(usage map[string]any, now time.Time) (codexRefreshState, error) {
	state := codexRefreshState{FetchedRefreshTime: true, FetchedAt: now.UTC().Format(time.RFC3339), ExactSevenDayRefresh: false, PreheatNeeded: false}
	windows := quotaWindowsOf(usage)
	if fiveHourWindow, ok := firstQuotaWindowWithSeconds(windows, fiveHourQuotaWindowSeconds); ok {
		if err := setCodexRefreshStateFromWindow(&state, fiveHourWindow.window, fiveHourWindow.seconds, now); err != nil {
			return codexRefreshState{FetchedRefreshTime: false}, err
		}
		state.WeeklyGateResetAt = weeklyGateResetAt(windows, now)
		return state, nil
	}
	window, seconds, ok := quotaResetWindowOf(usage)
	if !ok {
		return codexRefreshState{FetchedRefreshTime: false}, fmt.Errorf("unrecognized monthly quota window")
	}
	if err := setCodexRefreshStateFromWindow(&state, window, seconds, now); err != nil {
		return codexRefreshState{FetchedRefreshTime: false}, err
	}
	return state, nil
}

func setCodexRefreshStateFromWindow(state *codexRefreshState, window map[string]any, seconds int64, now time.Time) error {
	if state == nil || window == nil {
		return fmt.Errorf("missing reset time")
	}
	resetAfter, hasResetAfter := numberField(window, "reset_after_seconds", "resetAfterSeconds")
	resetAt, hasResetAt := numberField(window, "reset_at", "resetAt")
	if (hasResetAfter && int64(resetAfter+0.5) == seconds) || (hasResetAt && int64(resetAt)-now.Unix() == seconds) {
		state.ExactSevenDayRefresh = true
		state.PreheatNeeded = true
		return nil
	}
	var reset time.Time
	if hasResetAt && resetAt > 0 {
		reset = time.Unix(int64(resetAt), 0).UTC()
	} else if hasResetAfter && resetAfter >= 0 {
		reset = now.Add(time.Duration(resetAfter * float64(time.Second))).UTC()
	}
	if reset.IsZero() {
		return fmt.Errorf("missing reset time")
	}
	state.WeeklyResetAt = reset.Format(time.RFC3339)
	return nil
}

func quotaResetWindowOf(usage map[string]any) (map[string]any, int64, bool) {
	for _, window := range quotaWindowsOf(usage) {
		if _, monthly := monthlyQuotaWindowSeconds[window.seconds]; monthly {
			return window.window, window.seconds, true
		}
	}
	return nil, 0, false
}

func quotaWindowsOf(usage map[string]any) []codexQuotaWindow {
	windows := make([]codexQuotaWindow, 0)
	for _, rateLimit := range rateLimitCandidatesOf(usage) {
		for _, key := range []string{"primary_window", "primaryWindow", "primary", "secondary_window", "secondaryWindow", "secondary"} {
			window, _ := rateLimit[key].(map[string]any)
			if window == nil {
				continue
			}
			secondsFloat, ok := numberFieldValue(firstPresent(window, "limit_window_seconds", "limitWindowSeconds"))
			seconds := int64(secondsFloat + 0.5)
			if ok && seconds > 0 {
				windows = append(windows, codexQuotaWindow{window: window, seconds: seconds})
			}
		}
	}
	return windows
}

func firstQuotaWindowWithSeconds(windows []codexQuotaWindow, seconds int64) (codexQuotaWindow, bool) {
	for _, window := range windows {
		if window.seconds == seconds {
			return window, true
		}
	}
	return codexQuotaWindow{}, false
}

func weeklyGateResetAt(windows []codexQuotaWindow, now time.Time) string {
	for _, window := range windows {
		if window.seconds != weeklyQuotaWindowSeconds || !quotaWindowLimitReached(window) {
			continue
		}
		reset, ok := resetTimeFromWindow(window.window, now)
		if ok {
			return reset.Format(time.RFC3339)
		}
	}
	return ""
}

func quotaWindowLimitReached(window codexQuotaWindow) bool {
	if reached, ok := boolField(window.window, "limit_reached", "limitReached"); ok && reached {
		return true
	}
	used, ok := numberField(window.window, "used_percent", "usedPercent")
	return ok && used >= 100
}

func resetTimeFromWindow(window map[string]any, now time.Time) (time.Time, bool) {
	resetAfter, hasResetAfter := numberField(window, "reset_after_seconds", "resetAfterSeconds")
	resetAt, hasResetAt := numberField(window, "reset_at", "resetAt")
	if hasResetAt && resetAt > 0 {
		return time.Unix(int64(resetAt), 0).UTC(), true
	}
	if hasResetAfter && resetAfter >= 0 {
		return now.Add(time.Duration(resetAfter * float64(time.Second))).UTC(), true
	}
	return time.Time{}, false
}

func rateLimitCandidatesOf(usage map[string]any) []map[string]any {
	if usage == nil {
		return nil
	}
	out := make([]map[string]any, 0, 4)
	for _, key := range []string{"rate_limit", "rateLimit", "code_review_rate_limit", "codeReviewRateLimit", "code_review_rate_limits", "codeReviewRateLimits"} {
		if value, ok := usage[key].(map[string]any); ok && value != nil {
			out = append(out, value)
		}
	}
	additional := firstPresent(usage, "additional_rate_limits", "additionalRateLimits")
	switch typed := additional.(type) {
	case []any:
		for _, item := range typed {
			if m, ok := item.(map[string]any); ok {
				if rl, ok := firstPresent(m, "rate_limit", "rateLimit").(map[string]any); ok {
					out = append(out, rl)
				}
			}
		}
	case map[string]any:
		if rl, ok := firstPresent(typed, "rate_limit", "rateLimit").(map[string]any); ok {
			out = append(out, rl)
		}
	}
	return out
}

func firstPresent(m map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := m[key]; ok {
			return value
		}
	}
	return nil
}

func numberField(m map[string]any, keys ...string) (float64, bool) {
	return numberFieldValue(firstPresent(m, keys...))
}

func numberFieldValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		if f, err := typed.Float64(); err == nil {
			return f, true
		}
	case string:
		var n json.Number = json.Number(strings.TrimSpace(typed))
		if f, err := n.Float64(); err == nil {
			return f, true
		}
	}
	return 0, false
}

func boolField(m map[string]any, keys ...string) (bool, bool) {
	switch typed := firstPresent(m, keys...).(type) {
	case bool:
		return typed, true
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return false, false
		}
		parsed, err := strconv.ParseBool(trimmed)
		if err == nil {
			return parsed, true
		}
	}
	return false, false
}

func (h *Handler) persistCodexRefreshState(ctx context.Context, authID string, state codexRefreshState) error {
	latest, ok := h.authManager.GetByID(authID)
	if !ok || latest == nil {
		return fmt.Errorf("auth not found")
	}
	if latest.Metadata == nil {
		latest.Metadata = make(map[string]any)
	}
	for _, key := range refreshStateKeys {
		delete(latest.Metadata, key)
	}
	latest.Metadata["fetched_refresh_time"] = state.FetchedRefreshTime
	latest.Metadata["exact_seven_day_refresh"] = state.ExactSevenDayRefresh
	latest.Metadata["preheat_needed"] = state.PreheatNeeded
	latest.Metadata["weekly_reset_at"] = state.WeeklyResetAt
	latest.Metadata["weekly_gate_reset_at"] = state.WeeklyGateResetAt
	latest.Metadata["fetched_at"] = state.FetchedAt
	latest.UpdatedAt = time.Now().UTC()
	_, err := h.authManager.Update(ctx, latest)
	return err
}

func refreshStateFromAuth(auth *coreauth.Auth) codexRefreshState {
	state := codexRefreshState{}
	if auth == nil || auth.Metadata == nil {
		return state
	}
	state.FetchedRefreshTime, _ = authFileBoolValue(auth.Metadata["fetched_refresh_time"])
	state.ExactSevenDayRefresh, _ = authFileBoolValue(auth.Metadata["exact_seven_day_refresh"])
	state.PreheatNeeded, _ = authFileBoolValue(auth.Metadata["preheat_needed"])
	state.WeeklyResetAt, _ = auth.Metadata["weekly_reset_at"].(string)
	state.WeeklyGateResetAt, _ = auth.Metadata["weekly_gate_reset_at"].(string)
	state.FetchedAt, _ = auth.Metadata["fetched_at"].(string)
	return state
}

func chatGPTAccountIDForAuth(auth *coreauth.Auth) string {
	if auth == nil {
		return ""
	}
	if auth.Metadata != nil {
		for _, key := range []string{"account_id", "accountId", "chatgpt_account_id", "chatgptAccountId"} {
			if value, ok := auth.Metadata[key].(string); ok && strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
	}
	if claims := extractCodexIDTokenClaims(auth); claims != nil {
		if value, ok := claims["chatgpt_account_id"].(string); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
