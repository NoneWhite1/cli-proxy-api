package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gin "github.com/gin-gonic/gin"
	proxyconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	internallogging "github.com/router-for-me/CLIProxyAPI/v7/internal/logging"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/redisqueue"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	sdkaccess "github.com/router-for-me/CLIProxyAPI/v7/sdk/access"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()

	gin.SetMode(gin.TestMode)

	tmpDir := t.TempDir()
	authDir := filepath.Join(tmpDir, "auth")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatalf("failed to create auth dir: %v", err)
	}

	cfg := &proxyconfig.Config{
		SDKConfig: sdkconfig.SDKConfig{
			APIKeys: []string{"test-key"},
		},
		Port:                   0,
		AuthDir:                authDir,
		Debug:                  true,
		LoggingToFile:          false,
		UsageStatisticsEnabled: false,
	}

	authManager := auth.NewManager(nil, nil, nil)
	accessManager := sdkaccess.NewManager()

	configPath := filepath.Join(tmpDir, "config.yaml")
	return NewServer(cfg, authManager, accessManager, configPath)
}

func TestHealthz(t *testing.T) {
	server := newTestServer(t)

	t.Run("GET", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rr := httptest.NewRecorder()
		server.engine.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("unexpected status code: got %d want %d; body=%s", rr.Code, http.StatusOK, rr.Body.String())
		}

		var resp struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to parse response JSON: %v; body=%s", err, rr.Body.String())
		}
		if resp.Status != "ok" {
			t.Fatalf("unexpected response status: got %q want %q", resp.Status, "ok")
		}
	})

	t.Run("HEAD", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodHead, "/healthz", nil)
		rr := httptest.NewRecorder()
		server.engine.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("unexpected status code: got %d want %d; body=%s", rr.Code, http.StatusOK, rr.Body.String())
		}
		if rr.Body.Len() != 0 {
			t.Fatalf("expected empty body for HEAD request, got %q", rr.Body.String())
		}
	})
}

func TestManagementUsageRequiresManagementAuthAndPopsArray(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "test-management-key")

	prevQueueEnabled := redisqueue.Enabled()
	redisqueue.SetEnabled(false)
	t.Cleanup(func() {
		redisqueue.SetEnabled(false)
		redisqueue.SetEnabled(prevQueueEnabled)
	})

	server := newTestServer(t)

	redisqueue.Enqueue([]byte(`{"id":1}`))
	redisqueue.Enqueue([]byte(`{"id":2}`))

	missingKeyReq := httptest.NewRequest(http.MethodGet, "/v0/management/usage-queue?count=2", nil)
	missingKeyRR := httptest.NewRecorder()
	server.engine.ServeHTTP(missingKeyRR, missingKeyReq)
	if missingKeyRR.Code != http.StatusUnauthorized {
		t.Fatalf("missing key status = %d, want %d body=%s", missingKeyRR.Code, http.StatusUnauthorized, missingKeyRR.Body.String())
	}

	legacyReq := httptest.NewRequest(http.MethodGet, "/v0/management/usage?count=2", nil)
	legacyReq.Header.Set("Authorization", "Bearer test-management-key")
	legacyRR := httptest.NewRecorder()
	server.engine.ServeHTTP(legacyRR, legacyReq)
	if legacyRR.Code != http.StatusNotFound {
		t.Fatalf("legacy usage status = %d, want %d body=%s", legacyRR.Code, http.StatusNotFound, legacyRR.Body.String())
	}

	authReq := httptest.NewRequest(http.MethodGet, "/v0/management/usage-queue?count=2", nil)
	authReq.Header.Set("Authorization", "Bearer test-management-key")
	authRR := httptest.NewRecorder()
	server.engine.ServeHTTP(authRR, authReq)
	if authRR.Code != http.StatusOK {
		t.Fatalf("authenticated status = %d, want %d body=%s", authRR.Code, http.StatusOK, authRR.Body.String())
	}

	var payload []json.RawMessage
	if errUnmarshal := json.Unmarshal(authRR.Body.Bytes(), &payload); errUnmarshal != nil {
		t.Fatalf("unmarshal response: %v body=%s", errUnmarshal, authRR.Body.String())
	}
	if len(payload) != 2 {
		t.Fatalf("response records = %d, want 2", len(payload))
	}
	for i, raw := range payload {
		var record struct {
			ID int `json:"id"`
		}
		if errUnmarshal := json.Unmarshal(raw, &record); errUnmarshal != nil {
			t.Fatalf("unmarshal record %d: %v", i, errUnmarshal)
		}
		if record.ID != i+1 {
			t.Fatalf("record %d id = %d, want %d", i, record.ID, i+1)
		}
	}

	if remaining := redisqueue.PopOldest(1); len(remaining) != 0 {
		t.Fatalf("remaining queue = %q, want empty", remaining)
	}
}

func TestHomeEnabledHidesManagementEndpointsAndControlPanel(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "test-management-key")

	server := newTestServer(t)
	server.cfg.Home.Enabled = true

	t.Run("management endpoints return 404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v0/management/config", nil)
		req.Header.Set("Authorization", "Bearer test-management-key")
		rr := httptest.NewRecorder()
		server.engine.ServeHTTP(rr, req)
		if rr.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusNotFound, rr.Body.String())
		}
	})

	t.Run("management control panel returns 404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/management.html", nil)
		rr := httptest.NewRecorder()
		server.engine.ServeHTTP(rr, req)
		if rr.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusNotFound, rr.Body.String())
		}
	})
}

func TestInjectManagementPreheatPanelAddsScript(t *testing.T) {
	input := []byte(`<!doctype html><html><body><div id="root"></div></body></html>`)
	out := injectManagementPreheatPanel(input)
	body := string(out)

	if !strings.Contains(body, `window.__cliproxyCodexPreheat`) {
		t.Fatalf("injected html missing preheat marker: %s", body)
	}
	if !strings.Contains(body, `/v0/management/auth-files/preheat`) {
		t.Fatalf("injected html missing preheat endpoint: %s", body)
	}
	if strings.Index(body, `window.__cliproxyCodexPreheat`) > strings.Index(body, `</body>`) {
		t.Fatalf("preheat script should be inserted before </body>: %s", body)
	}
}

func TestInjectManagementPreheatPanelSkipsExistingScript(t *testing.T) {
	input := []byte(`<!doctype html><html><body>` + managementPreheatScript + `</body></html>`)
	out := injectManagementPreheatPanel(input)

	if string(out) != string(input) {
		t.Fatalf("expected current preheat script to remain unchanged")
	}
}

func TestInjectManagementPreheatPanelReplacesStaleScript(t *testing.T) {
	input := []byte(`<!doctype html><html><body><main></main><script>window.__cliproxyCodexPreheat=true;function parseAuthFiles(){return [];}</script></body></html>`)
	out := injectManagementPreheatPanel(input)
	body := string(out)

	if strings.Contains(body, `function parseAuthFiles(){return [];}`) {
		t.Fatalf("stale preheat script was not replaced: %s", body)
	}
	if !strings.Contains(body, `function refreshAuthFilesSoon`) {
		t.Fatalf("replacement script missing refresh behavior: %s", body)
	}
	if strings.Count(body, `window.__cliproxyCodexPreheat`) != 2 {
		t.Fatalf("preheat marker count = %d, want 2 in one current script: %s", strings.Count(body, `window.__cliproxyCodexPreheat`), body)
	}
}

func TestManagementControlPanelReplacesStalePreheatScriptFromStaticPath(t *testing.T) {
	staticPath := filepath.Join(t.TempDir(), "management.html")
	t.Setenv("MANAGEMENT_STATIC_PATH", staticPath)

	staleScript := `function parseAuthFiles(){return [];}`
	staleHTML := `<!doctype html><html><body><main></main><script>window.__cliproxyCodexPreheat=true;` + staleScript + `</script></body></html>`
	if err := os.WriteFile(staticPath, []byte(staleHTML), 0o644); err != nil {
		t.Fatalf("failed to write stale management asset: %v", err)
	}

	server := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/management.html", nil)
	rr := httptest.NewRecorder()
	server.engine.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
	body := rr.Body.String()
	if strings.Contains(body, staleScript) {
		t.Fatalf("served management panel kept stale preheat script: %s", body)
	}
	if !strings.Contains(body, `function refreshAuthFilesSoon`) {
		t.Fatalf("served management panel missing refreshed preheat script: %s", body)
	}
	if strings.Count(body, `window.__cliproxyCodexPreheat`) != 2 {
		t.Fatalf("served preheat marker count = %d, want 2 in one current script: %s", strings.Count(body, `window.__cliproxyCodexPreheat`), body)
	}
}

func TestInjectManagementPreheatPanelPatchesCompactQuotaBundle(t *testing.T) {
	compactQuotaGuard := `T=l&&Nx(n)===l?l:null,E=!!T&&!y&&!r,D=1;`
	compactQuotaCSS := `.AuthFilesPage-module__fileCardCompact___u9yZu .AuthFilesPage-module__quotaSection___hXy5f{display:none}`
	input := []byte(`<!doctype html><html><head><script>` + compactQuotaGuard + `</script><style>` + compactQuotaCSS + `.AuthFilesPage-module__quotaMessageAction___9r9cq{cursor:pointer}</style></head><body><div>点击此处刷新额度</div></body></html>`)
	out := string(injectManagementPreheatPanel(input))

	if strings.Contains(out, `T=l&&Nx(n)===l?l:null`) {
		t.Fatalf("auth-file quota render guard still depends on the active provider filter: %s", out)
	}
	if !strings.Contains(out, `T=Nx(n),E=!!T&&!y,D=1`) {
		t.Fatalf("auth-file quota render guard was not patched to render quota-capable provider cards: %s", out)
	}
	if strings.Contains(out, `E=!!T&&!y&&!r`) {
		t.Fatalf("compact quota render guard still excludes compact cards: %s", out)
	}
	if strings.Contains(out, compactQuotaCSS) {
		t.Fatalf("compact quota CSS still hides the quota section: %s", out)
	}
	if !strings.Contains(out, `.AuthFilesPage-module__fileCardCompact___u9yZu .AuthFilesPage-module__quotaSection___hXy5f{display:flex}`) {
		t.Fatalf("compact quota CSS was not patched visible: %s", out)
	}
	for _, token := range []string{`点击此处刷新额度`, `quotaMessageAction___9r9cq`, `window.__cliproxyCodexPreheat`} {
		if !strings.Contains(out, token) {
			t.Fatalf("patched management HTML should retain native quota token %q", token)
		}
	}
}

func TestInjectManagementPreheatPanelPatchesExistingCurrentScriptHTML(t *testing.T) {
	input := []byte(`<!doctype html><html><body><script>T=l&&Nx(n)===l?l:null,E=!!T&&!y&&!r,D=1;</script>` + managementPreheatScript + `</body></html>`)
	out := string(injectManagementPreheatPanel(input))

	if strings.Contains(out, `T=l&&Nx(n)===l?l:null`) {
		t.Fatalf("existing current preheat script HTML should still receive provider-independent quota bundle patch: %s", out)
	}
	if strings.Contains(out, `E=!!T&&!y&&!r`) {
		t.Fatalf("existing current preheat script HTML should still receive compact quota bundle patch: %s", out)
	}
	if !strings.Contains(out, `T=Nx(n),E=!!T&&!y,D=1`) {
		t.Fatalf("existing current preheat script HTML missing patched compact quota guard: %s", out)
	}
	if strings.Count(out, `window.__cliproxyCodexPreheat`) != 2 {
		t.Fatalf("existing current preheat script should not be duplicated; marker count = %d", strings.Count(out, `window.__cliproxyCodexPreheat`))
	}
}

func TestInjectManagementPreheatPanelPatchesCurrentManagementBundleQuotaCards(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "static", "management.html"))
	if err != nil {
		t.Fatalf("failed to read current management bundle: %v", err)
	}
	out := string(injectManagementPreheatPanel(data))

	if strings.Contains(out, `T=l&&Nx(n)===l?l:null`) {
		t.Fatalf("current management bundle should not require the active auth-files filter before rendering quota cards")
	}
	if strings.Contains(out, `E=!!T&&!y&&!r`) {
		t.Fatalf("current management bundle should not suppress quota cards in compact mode")
	}
	if strings.Contains(out, `.AuthFilesPage-module__fileCardCompact___u9yZu .AuthFilesPage-module__quotaSection___hXy5f{display:none}`) {
		t.Fatalf("current management bundle should not hide compact quota sections")
	}
	for _, token := range []string{`T=Nx(n),E=!!T&&!y`, `.AuthFilesPage-module__fileCardCompact___u9yZu .AuthFilesPage-module__quotaSection___hXy5f{display:flex}`, `点击此处刷新额度`, `window.__cliproxyCodexPreheat`} {
		if !strings.Contains(out, token) {
			t.Fatalf("current management bundle missing patched quota token %q", token)
		}
	}
}

func TestManagementPreheatPanelRefreshesAfterAuthFileMutation(t *testing.T) {
	script := managementPreheatScript
	if !strings.Contains(script, `function refreshAuthFilesSoon`) {
		t.Fatalf("preheat script should debounce auth-files refreshes after imports")
	}
	if !strings.Contains(script, `method !== "GET"`) {
		t.Fatalf("preheat script should detect non-GET auth-files mutations")
	}
	if !strings.Contains(script, `path.indexOf(authFilesEndpoint) === 0`) {
		t.Fatalf("preheat script should refresh after auth-files subtree mutations")
	}
	if !strings.Contains(script, `refreshAuthFilesSoon(250)`) {
		t.Fatalf("preheat script should schedule a refresh after successful auth file mutations")
	}
	if !strings.Contains(script, `loadAuthFilesForPreheat().then(function () { scheduleRender(); }).catch(function () { scheduleRender(); })`) {
		t.Fatalf("preheat script should load the authoritative auth-files list after mutations")
	}
}

func TestManagementPreheatPanelUsesAuthFilesSelection(t *testing.T) {
	script := managementPreheatScript
	if strings.Contains(script, `<select aria-label="选择 Codex 凭证"`) {
		t.Fatalf("preheat script should not render a separate credential dropdown")
	}
	if !strings.Contains(script, `function selectedCodexAuthFiles`) {
		t.Fatalf("preheat script should collect selected Codex rows from the auth-files page")
	}
	if !strings.Contains(script, `querySelectorAll("input[type='checkbox']")`) {
		t.Fatalf("preheat script should sync existing auth-files page rows")
	}
	if !strings.Contains(script, `startPreheatJob("preheat", selected)`) {
		t.Fatalf("preheat script should start one backend preheat job for selected credentials")
	}
	if !strings.Contains(script, `预热选中账号`) {
		t.Fatalf("preheat script should add a preheat-selected action on the auth-files page")
	}
}

func TestManagementPreheatPanelRendersRefreshTimeControls(t *testing.T) {
	script := managementPreheatScript
	for _, token := range []string{
		`预热选中账号`,
		`获取选中刷新时间`,
		`只显示未获取刷新时间`,
		`启动自动预热`,
		`停止自动预热`,
		`未获取刷新时间`,
		`已获取刷新时间`,
	} {
		if !strings.Contains(script, token) {
			t.Fatalf("preheat script missing refresh-time UI token %q", token)
		}
	}
	for _, action := range []string{`data-preheat-action="manual"`, `data-preheat-action="fetch-refresh"`, `data-preheat-action="toggle-missing"`, `data-preheat-action="auto"`} {
		if !strings.Contains(script, action) {
			t.Fatalf("preheat script should render action %s", action)
		}
	}
}

func TestManagementPreheatPanelUsesBackendPreheatJobs(t *testing.T) {
	script := managementPreheatScript
	for _, token := range []string{
		`var preheatJobEndpoint = "/v0/management/auth-files/preheat/jobs"`,
		`function startPreheatJob`,
		`function pollPreheatJob`,
		`body: JSON.stringify({ operation: operation, auth_indices: indexes })`,
		`preheatJobEndpoint + "/" + encodeURIComponent(jobID)`,
		`function terminalPreheatJobStatus`,
		`state.jobPollTimer = window.setTimeout(function () { pollPreheatJob(jobID, operation); }, jobPollIntervalMs)`,
	} {
		if !strings.Contains(script, token) {
			t.Fatalf("preheat script should use backend job token %q", token)
		}
	}
	for _, token := range []string{
		`/v0/management/api-call`,
		`https://chatgpt.com/backend-api/wham/usage`,
		`Authorization: "Bearer $TOKEN$"`,
		`function fetchRefreshTimeForAuthFile`,
		`function persistRefreshStateForAuthFile`,
		`authFileFieldsEndpoint`,
		`preheatSelectedWithInterval`,
		`fetchRefreshTimesWithInterval`,
		`preheatAndRefreshWithInterval`,
		`autoPreheatLoop`,
	} {
		if strings.Contains(script, token) {
			t.Fatalf("preheat script should not contain browser-owned long-work token %q", token)
		}
	}
}

func TestManagementPreheatPanelReadsBackendRefreshTimeState(t *testing.T) {
	script := managementPreheatScript
	for _, token := range []string{
		`function normalizeStoredRefreshState`,
		`fetched_refresh_time`,
		`exact_seven_day_refresh`,
		`preheat_needed`,
		`weekly_reset_at`,
		`function rememberAuthFile`,
		`function knownCodexAuthFiles`,
		`var nextAuthFilesByIndex = {}`,
		`var nextRefreshTimes = {}`,
		`state.authFilesByIndex = nextAuthFilesByIndex`,
		`state.refreshTimes = nextRefreshTimes`,
	} {
		if !strings.Contains(script, token) {
			t.Fatalf("preheat script missing backend refresh-state display token %q", token)
		}
	}
	for _, token := range []string{
		`var monthlyWindowSeconds`,
		`var quotaResetWindowSeconds`,
		`function firstDefinedValue`,
		`function apiCallBodyOf`,
		`function isQuotaResetWindowSeconds`,
		`function rateLimitCandidatesOf`,
		`function quotaResetWindowOf`,
		`function refreshStatePayload`,
		`limit_window_seconds`,
		`reset_after_seconds`,
		`body_text`,
		`604` + `800`,
		`delete state.refreshTimes[index]`,
	} {
		if strings.Contains(script, token) {
			t.Fatalf("preheat script should not contain backend-owned refresh parser token %q", token)
		}
	}
}

func TestManagementPreheatPanelFetchesSelectedRefreshTimesThroughBackendJob(t *testing.T) {
	script := managementPreheatScript
	if !strings.Contains(script, `function fetchSelectedRefreshTimes`) {
		t.Fatalf("preheat script should expose a selected refresh-time fetch action")
	}
	if !strings.Contains(script, `startPreheatJob("refresh_time", selected)`) {
		t.Fatalf("selected refresh-time fetch should start one backend refresh_time job")
	}
	for _, token := range []string{
		`function fetchRefreshTimesWithInterval`,
		`return sleep(preheatIntervalMs).then(function () { return fetchRefreshTimeForAuthFile(file); })`,
		`Promise.all(selected.map(fetchRefreshTimeForAuthFile))`,
	} {
		if strings.Contains(script, token) {
			t.Fatalf("refresh-time fetch should not be browser-owned; found %q", token)
		}
	}
}

func TestManagementPreheatPanelAutoPreheatUsesBackendStatus(t *testing.T) {
	script := managementPreheatScript
	for _, token := range []string{
		`var preheatAutoEndpoint = "/v0/management/auth-files/preheat/auto"`,
		`function ensureAutoStatus`,
		`function fetchPreheatAutoStatus`,
		`function pollPreheatAutoStatus`,
		`function toggleAutoPreheat`,
		`method: "PATCH"`,
		`body: JSON.stringify({ enabled: enable })`,
		`method: "GET"`,
		`state.autoRunning = data.enabled === true`,
		`state.autoPollTimer = window.setTimeout(function ()`,
	} {
		if !strings.Contains(script, token) {
			t.Fatalf("preheat script missing backend auto token %q", token)
		}
	}
	for _, token := range []string{
		`function autoPreheatLoop`,
		`function sortResetAuthFiles`,
		`var dueScheduled = scheduled.filter`,
		`preheatAndRefreshWithInterval(due)`,
		`sleep(autoPreheatLoopIntervalMs).then(autoPreheatLoop)`,
	} {
		if strings.Contains(script, token) {
			t.Fatalf("auto preheat should be backend-owned; found %q", token)
		}
	}
}

func TestManagementPreheatPanelBackendOwnsAutoEnableAndDisabledSkip(t *testing.T) {
	script := managementPreheatScript
	for _, token := range []string{
		`authFileStatusEndpoint`,
		`function isDisabledAuthFile`,
		`function authStatusNameOf`,
		`function setAuthFileDisabled`,
		`function enableAuthFileForPreheat`,
		`setAuthFileDisabled(file, false)`,
		`function isAutoQuotaDisabledAuthFile`,
		`function canAutoPreheatAuthFile`,
		`quota_auto_disabled`,
	} {
		if strings.Contains(script, token) {
			t.Fatalf("backend should own auto enable/disabled skip behavior; found %q", token)
		}
	}
}

func TestManagementPreheatPanelBlocksManualActionsDuringBackendWork(t *testing.T) {
	script := managementPreheatScript
	if !strings.Contains(script, `var manualBusy = state.loading || state.autoBusy || !originalFetch`) {
		t.Fatalf("manual controls should be disabled while backend work is active")
	}
	guard := `if (state.loading || state.autoBusy || !originalFetch) return;`
	if strings.Count(script, guard) < 2 {
		t.Fatalf("manual preheat and refresh-time fetch handlers should return while backend work is active")
	}
}

func TestManagementPreheatPanelNoLongerUsesStaticRefreshIntervalAuthMetadata(t *testing.T) {
	script := managementPreheatScript
	for _, token := range []string{`function refreshIntervalOf`, `function planTypeOf`, `function sevenDayRefreshIntervalCodexAuthFiles`, `refreshIntervalOf(file) === sevenDayRefreshIntervalMs`, `planTypeOf(file) === "free"`, `function weeklyWindowOf`, `var weeklyWindowSeconds`, `function sortWeeklyResetAuthFiles`, `function exactSevenDayRefreshAuthFiles`, `等待周限刷新`, `7 天刷新时间`} {
		if strings.Contains(script, token) {
			t.Fatalf("preheat script should not rely on stale auth-files metadata heuristic %q", token)
		}
	}
}

func TestManagementPreheatPanelRetainsSelectionAcrossPages(t *testing.T) {
	script := managementPreheatScript
	for _, token := range []string{
		`authFilesByIndex: {}`,
		`selectedAuthFiles: {}`,
		`function syncSelectedCodexAuthFiles`,
		`function knownCodexAuthFiles`,
		`var nextSelectedAuthFiles = {}`,
		`nextAuthFilesByIndex[index] = file`,
		`var selected = nextAuthFilesByIndex[index] || state.authFilesByIndex[index] || state.selectedAuthFiles[index]`,
		`if (selected && isCodexAuthFile(selected)) {`,
		`nextAuthFilesByIndex[index] = nextAuthFilesByIndex[index] || selected`,
		`nextSelectedAuthFiles[index] = nextAuthFilesByIndex[index]`,
		`state.selectedAuthFiles = nextSelectedAuthFiles`,
		`state.selectedAuthFiles[index] = state.authFilesByIndex[index] || file`,
		`return state.authFilesByIndex[index] || state.selectedAuthFiles[index]`,
		`Object.keys(state.selectedAuthFiles).map`,
		`delete state.selectedAuthFiles[index]`,
	} {
		if !strings.Contains(script, token) {
			t.Fatalf("preheat script should retain selected Codex credentials across pages; missing %q", token)
		}
	}
	if strings.Contains(script, `if (nextAuthFilesByIndex[index]) nextSelectedAuthFiles[index] = nextAuthFilesByIndex[index]`) {
		t.Fatalf("preheat script should not prune selected credentials just because the latest auth-files response omitted them")
	}
}

func TestManagementPreheatPanelReconcilesObservedAuthFiles(t *testing.T) {
	script := managementPreheatScript
	for _, token := range []string{
		`var nextAuthFilesByIndex = {}`,
		`var nextRefreshTimes = {}`,
		`var nextSelectedAuthFiles = {}`,
		`nextAuthFilesByIndex[index] = file`,
		`if (normalized && normalized.fetched_refresh_time) nextRefreshTimes[index] = normalized`,
		`var selected = nextAuthFilesByIndex[index] || state.authFilesByIndex[index] || state.selectedAuthFiles[index]`,
		`nextSelectedAuthFiles[index] = nextAuthFilesByIndex[index]`,
		`state.authFilesByIndex = nextAuthFilesByIndex`,
		`state.refreshTimes = nextRefreshTimes`,
		`state.selectedAuthFiles = nextSelectedAuthFiles`,
	} {
		if !strings.Contains(script, token) {
			t.Fatalf("preheat script should reconcile auth-file, refresh-time, and selection caches from the latest auth-files response; missing %q", token)
		}
	}
	if strings.Contains(script, `if (nextAuthFilesByIndex[index]) nextSelectedAuthFiles[index] = nextAuthFilesByIndex[index]`) {
		t.Fatalf("preheat script should preserve selected known credentials outside the latest auth-files response")
	}
	if strings.Contains(script, `delete state.refreshTimes[index]`) {
		t.Fatalf("preheat script should reconcile refresh state by replacing the observed refresh map, not ad-hoc deletes")
	}
}

func TestManagementPreheatPanelSchedulesSelectionSyncAfterBulkSelection(t *testing.T) {
	script := managementPreheatScript
	for _, token := range []string{
		`var selectionSyncTimer = null`,
		`document.addEventListener("click", handleSelectionChange, true)`,
		`document.addEventListener("input", handleSelectionChange, true)`,
		`document.addEventListener("change", handleSelectionChange, true)`,
		`function scheduleSelectionSync`,
		`if (selectionSyncTimer) window.clearTimeout(selectionSyncTimer)`,
		`selectionSyncTimer = window.setTimeout(function ()`,
		`syncSelectedCodexAuthFiles();`,
		`if (target.closest("#codex-preheat-panel")) return`,
		`if (String(target.type || "").toLowerCase() === "checkbox" && !isSelectionCheckbox(target)) return`,
		`scheduleSelectionSync();`,
	} {
		if !strings.Contains(script, token) {
			t.Fatalf("preheat script should schedule selection sync after host bulk-selection UI changes; missing %q", token)
		}
	}
}

func TestManagementPreheatPanelIncludesHostFilteredSelection(t *testing.T) {
	script := managementPreheatScript
	for _, token := range []string{
		`var hostFilteredSelectionPending = false`,
		`var hostSelectionClearPending = false`,
		`"authFilesPage.uiState"`,
		`function readHostAuthFilesUIState`,
		`function isHostFilteredSelectionControl`,
		`function isHostSelectionClearControl`,
		`全选筛选结果`,
		`全選篩選結果`,
		`Select filtered`,
		`Выбрать по фильтру`,
		`function applyPendingHostSelection`,
		`function selectHostFilteredCodexAuthFiles`,
		`function hostFilteredCodexAuthFiles`,
		`function hostFileMatchesUIState`,
		`function normalizeHostProvider`,
		`function isRuntimeOnlyAuthFile`,
		`knownCodexAuthFiles().filter(function (file) { return hostFileMatchesUIState(file, ui); })`,
		`state.selectedAuthFiles[index] = state.authFilesByIndex[index] || file`,
		`applyPendingHostSelection();`,
	} {
		if !strings.Contains(script, token) {
			t.Fatalf("preheat script should include host filtered selections beyond rendered rows; missing %q", token)
		}
	}
}

func TestManagementPreheatPanelStartsSelectedPreheatJobWithoutBrowserDelay(t *testing.T) {
	script := managementPreheatScript
	if !strings.Contains(script, `startPreheatJob("preheat", selected)`) {
		t.Fatalf("preheat script should start a backend job for selected credentials")
	}
	for _, token := range []string{
		`var preheatIntervalMs = 1000`,
		`function sleep(ms)`,
		`function preheatSelectedWithInterval`,
		`return sleep(preheatIntervalMs)`,
		`Promise.all(selected.map(preheatAuthFile))`,
	} {
		if strings.Contains(script, token) {
			t.Fatalf("preheat script should not own per-credential preheat delay; found %q", token)
		}
	}
}

func TestManagementPreheatPanelWalksSelectionAncestors(t *testing.T) {
	script := managementPreheatScript
	for _, token := range []string{
		`function fileForCheckbox`,
		`function authContainerForCheckbox`,
		`while (node && node !== document.body)`,
		`function classNameHasPrefix`,
		`function isAuthFileCard`,
		`classNameHasPrefix(className, "fileCard___")`,
		`classNameHasPrefix(className, "fileCardCompact___")`,
		`part.indexOf("__" + prefix) !== -1`,
		`function isAuthFileRow`,
		`return authContainerForCheckbox(checkbox, fileForCheckbox(checkbox))`,
	} {
		if !strings.Contains(script, token) {
			t.Fatalf("preheat script should resolve and hide whole credential cards; missing %q", token)
		}
	}
}

func TestManagementPreheatPanelHandlesStyledCompactCardsVisibly(t *testing.T) {
	script := managementPreheatScript
	for _, token := range []string{
		`function isAuthFileSemanticCard`,
		`quotaSection`,
		`cardActions`,
		`cardHeader`,
		`.codex-preheat-quota-visible [class*=\"quotaSection\"]`,
		`display:flex!important`,
		`function markAuthRowForQuotaVisibility`,
		`markAuthRowForQuotaVisibility(row)`,
	} {
		if !strings.Contains(script, token) {
			t.Fatalf("preheat script should add robust structural fallback and quota visibility styles for styled cards; missing %q", token)
		}
	}
	if strings.Count(script, `row.classList.add("codex-preheat-quota-visible")`) != 1 {
		t.Fatalf("preheat script should add quota visibility only through the row-scoped helper")
	}
}

func TestManagementPreheatPanelMissingRefreshTimeFilterScopesRows(t *testing.T) {
	script := managementPreheatScript
	for _, token := range []string{
		`fileCard___`,
		`__" + prefix`,
		`fileCardCompact___`,
		`return null;`,
		`if (checkbox.closest("#codex-preheat-panel") || !isSelectionCheckbox(checkbox)) return`,
		`body: JSON.stringify({ operation: operation, auth_indices: indexes })`,
	} {
		if !strings.Contains(script, token) {
			t.Fatalf("missing-refresh-time filter should scope row hiding to credential cards and preserve request/selection contracts; missing %q", token)
		}
	}
	for _, token := range []string{
		`if (!fallback && file && rowTextMatchesFile(node, file)) fallback = node`,
		`checkbox.closest("div")`,
	} {
		if strings.Contains(script, token) {
			t.Fatalf("missing-refresh-time filter must not hide arbitrary shared ancestors; found %q", token)
		}
	}
}

func TestManagementPreheatPanelIgnoresNonSelectionCheckboxes(t *testing.T) {
	script := managementPreheatScript
	if !strings.Contains(script, `function isSelectionCheckbox`) {
		t.Fatalf("preheat script should distinguish row selection checkboxes from status toggles")
	}
	if !strings.Contains(script, `SelectionCheckbox`) {
		t.Fatalf("preheat script should target the auth-files selection checkbox class")
	}
	if !strings.Contains(script, `cardSelection`) {
		t.Fatalf("preheat script should target auth-files card selection wrappers")
	}
}

func TestManagementPreheatPanelDoesNotLoadAuthFilesBeforeAuthFilesPage(t *testing.T) {
	script := managementPreheatScript
	if strings.Contains(script, `loadAuthFiles();`) {
		t.Fatalf("preheat script should not issue standalone auth-files requests before the management UI is authenticated")
	}
	if !strings.Contains(script, `path === "/auth-files"`) {
		t.Fatalf("preheat script should only render its action on the auth-files page")
	}
}

func TestManagementPreheatPanelTracksAuthFilesXHR(t *testing.T) {
	script := managementPreheatScript
	if !strings.Contains(script, `XMLHttpRequest.prototype.open`) {
		t.Fatalf("preheat script should observe management UI XHR auth-files requests")
	}
	if !strings.Contains(script, `XMLHttpRequest.prototype.send`) {
		t.Fatalf("preheat script should hook XHR send for auth-files responses")
	}
	if !strings.Contains(script, `xhr.addEventListener("load"`) {
		t.Fatalf("preheat script should parse successful XHR auth-files responses")
	}
	if !strings.Contains(script, `JSON.parse(xhr.responseText || "{}")`) {
		t.Fatalf("preheat script should parse XHR auth-files JSON payloads")
	}
}

func TestManagementPreheatPanelReusesObservedManagementAuth(t *testing.T) {
	script := managementPreheatScript
	if !strings.Contains(script, `state.authHeaders`) {
		t.Fatalf("preheat script should store management auth headers observed from the app")
	}
	if !strings.Contains(script, `XMLHttpRequest.prototype.setRequestHeader`) {
		t.Fatalf("preheat script should observe XHR Authorization headers")
	}
	if !strings.Contains(script, `headers.Authorization`) {
		t.Fatalf("preheat script should reuse observed Authorization for preheat requests")
	}
}

func TestAmpProviderModelRoutes(t *testing.T) {
	testCases := []struct {
		name         string
		path         string
		wantStatus   int
		wantContains string
	}{
		{
			name:         "openai root models",
			path:         "/api/provider/openai/models",
			wantStatus:   http.StatusOK,
			wantContains: `"object":"list"`,
		},
		{
			name:         "groq root models",
			path:         "/api/provider/groq/models",
			wantStatus:   http.StatusOK,
			wantContains: `"object":"list"`,
		},
		{
			name:         "openai models",
			path:         "/api/provider/openai/v1/models",
			wantStatus:   http.StatusOK,
			wantContains: `"object":"list"`,
		},
		{
			name:         "anthropic models",
			path:         "/api/provider/anthropic/v1/models",
			wantStatus:   http.StatusOK,
			wantContains: `"data"`,
		},
		{
			name:         "google models v1",
			path:         "/api/provider/google/v1/models",
			wantStatus:   http.StatusOK,
			wantContains: `"models"`,
		},
		{
			name:         "google models v1beta",
			path:         "/api/provider/google/v1beta/models",
			wantStatus:   http.StatusOK,
			wantContains: `"models"`,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			server := newTestServer(t)

			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.Header.Set("Authorization", "Bearer test-key")

			rr := httptest.NewRecorder()
			server.engine.ServeHTTP(rr, req)

			if rr.Code != tc.wantStatus {
				t.Fatalf("unexpected status code for %s: got %d want %d; body=%s", tc.path, rr.Code, tc.wantStatus, rr.Body.String())
			}
			if body := rr.Body.String(); !strings.Contains(body, tc.wantContains) {
				t.Fatalf("response body for %s missing %q: %s", tc.path, tc.wantContains, body)
			}
		})
	}
}

func TestModelsWithClientVersionReturnsCodexCatalog(t *testing.T) {
	modelRegistry := registry.GetGlobalRegistry()
	clientID := "test-client-version-catalog"
	modelRegistry.RegisterClient(clientID, "openai", []*registry.ModelInfo{
		{
			ID:            "gpt-5.5",
			Object:        "model",
			Created:       1776902400,
			OwnedBy:       "openai",
			Type:          "openai",
			DisplayName:   "GPT 5.5",
			Description:   "Frontier model for complex coding, research, and real-world work.",
			ContextLength: 272000,
			Thinking:      &registry.ThinkingSupport{Levels: []string{"low", "medium", "high", "xhigh"}},
		},
		{
			ID:            "custom-codex-model-test",
			Object:        "model",
			OwnedBy:       "test",
			Type:          "openai",
			DisplayName:   "Custom Codex Model",
			Description:   "Custom model from registry",
			ContextLength: 123456,
			Thinking:      &registry.ThinkingSupport{Levels: []string{"none", "minimal", "low", "medium", "unsupported", "high", "xhigh"}},
		},
		{ID: "grok-imagine-image-quality", Object: "model", OwnedBy: "xai", Type: "openai"},
		{ID: "gpt-image-2", Object: "model", OwnedBy: "openai", Type: "openai"},
		{ID: "grok-imagine-image", Object: "model", OwnedBy: "xai", Type: "openai"},
		{ID: "grok-imagine-video", Object: "model", OwnedBy: "xai", Type: "openai"},
		{ID: "grok-imagine-video-1.5-preview", Object: "model", OwnedBy: "xai", Type: "openai"},
	})
	t.Cleanup(func() {
		modelRegistry.UnregisterClient(clientID)
	})

	server := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/models?client_version", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("User-Agent", "claude-cli/1.0")

	rr := httptest.NewRecorder()
	server.engine.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var resp struct {
		Models []map[string]any `json:"models"`
		Object string           `json:"object"`
		Data   []any            `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response JSON: %v; body=%s", err, rr.Body.String())
	}
	if resp.Object != "" || resp.Data != nil {
		t.Fatalf("expected codex catalog format without object/data, got object=%q data=%v", resp.Object, resp.Data)
	}
	if len(resp.Models) == 0 {
		t.Fatal("expected codex catalog models")
	}

	var gpt55 map[string]any
	var custom map[string]any
	for _, model := range resp.Models {
		switch slug, _ := model["slug"].(string); slug {
		case "gpt-5.5":
			gpt55 = model
		case "custom-codex-model-test":
			custom = model
		}
	}
	if gpt55 == nil {
		t.Fatal("expected gpt-5.5 codex catalog entry")
	}
	if _, ok := gpt55["minimal_client_version"]; !ok {
		t.Fatal("expected minimal_client_version in codex catalog")
	}
	serviceTiers, ok := gpt55["service_tiers"].([]any)
	if !ok || len(serviceTiers) != 1 {
		t.Fatalf("expected gpt-5.5 priority service tier, got %#v", gpt55["service_tiers"])
	}
	if custom == nil {
		t.Fatal("expected custom model codex catalog entry")
	}
	if got, _ := custom["display_name"].(string); got != "Custom Codex Model" {
		t.Fatalf("custom display_name = %q, want Custom Codex Model", got)
	}
	if got, _ := custom["description"].(string); got != "Custom model from registry" {
		t.Fatalf("custom description = %q, want Custom model from registry", got)
	}
	if got, _ := custom["context_window"].(float64); got != 123456 {
		t.Fatalf("custom context_window = %v, want 123456", custom["context_window"])
	}
	assertCodexSupportedReasoningLevels(t, custom, []string{"none", "low", "medium", "high", "xhigh"})
	if custom["base_instructions"] != gpt55["base_instructions"] {
		t.Fatal("expected custom model to use gpt-5.5 base_instructions fallback")
	}
	if _, ok := custom["available_in_plans"].([]any); !ok {
		t.Fatalf("expected custom model to use gpt-5.5 available_in_plans fallback, got %#v", custom["available_in_plans"])
	}
	if got, _ := custom["prefer_websockets"].(bool); got {
		t.Fatalf("custom prefer_websockets = %v, want false", custom["prefer_websockets"])
	}
	if _, ok := custom["apply_patch_tool_type"]; ok {
		t.Fatal("expected custom model to omit apply_patch_tool_type")
	}
	if _, ok := custom["upgrade"]; ok {
		t.Fatal("expected custom model to omit upgrade")
	}
	if _, ok := custom["availability_nux"]; ok {
		t.Fatal("expected custom model to omit availability_nux")
	}

	hiddenModels := map[string]bool{
		"grok-imagine-image-quality":     false,
		"gpt-image-2":                    false,
		"grok-imagine-image":             false,
		"grok-imagine-video":             false,
		"grok-imagine-video-1.5-preview": false,
	}
	for _, model := range resp.Models {
		slug, _ := model["slug"].(string)
		if _, ok := hiddenModels[slug]; !ok {
			continue
		}
		if visibility, _ := model["visibility"].(string); visibility != "hide" {
			t.Fatalf("%s visibility = %q, want hide", slug, visibility)
		}
		hiddenModels[slug] = true
	}
	for slug, found := range hiddenModels {
		if !found {
			t.Fatalf("expected hidden model %s in codex catalog", slug)
		}
	}
}

func assertCodexSupportedReasoningLevels(t *testing.T, model map[string]any, want []string) {
	t.Helper()

	rawLevels, ok := model["supported_reasoning_levels"].([]any)
	if !ok {
		t.Fatalf("expected supported_reasoning_levels, got %#v", model["supported_reasoning_levels"])
	}
	if len(rawLevels) != len(want) {
		t.Fatalf("supported_reasoning_levels length = %d, want %d: %#v", len(rawLevels), len(want), rawLevels)
	}
	for index, rawLevel := range rawLevels {
		levelEntry, ok := rawLevel.(map[string]any)
		if !ok {
			t.Fatalf("supported_reasoning_levels[%d] = %#v, want object", index, rawLevel)
		}
		if got, _ := levelEntry["effort"].(string); got != want[index] {
			t.Fatalf("supported_reasoning_levels[%d].effort = %q, want %q", index, got, want[index])
		}
	}
}

func TestDefaultRequestLoggerFactory_UsesResolvedLogDirectory(t *testing.T) {
	t.Setenv("WRITABLE_PATH", "")
	t.Setenv("writable_path", "")

	originalWD, errGetwd := os.Getwd()
	if errGetwd != nil {
		t.Fatalf("failed to get current working directory: %v", errGetwd)
	}

	tmpDir := t.TempDir()
	if errChdir := os.Chdir(tmpDir); errChdir != nil {
		t.Fatalf("failed to switch working directory: %v", errChdir)
	}
	defer func() {
		if errChdirBack := os.Chdir(originalWD); errChdirBack != nil {
			t.Fatalf("failed to restore working directory: %v", errChdirBack)
		}
	}()

	// Force ResolveLogDirectory to fallback to auth-dir/logs by making ./logs not a writable directory.
	if errWriteFile := os.WriteFile(filepath.Join(tmpDir, "logs"), []byte("not-a-directory"), 0o644); errWriteFile != nil {
		t.Fatalf("failed to create blocking logs file: %v", errWriteFile)
	}

	configDir := filepath.Join(tmpDir, "config")
	if errMkdirConfig := os.MkdirAll(configDir, 0o755); errMkdirConfig != nil {
		t.Fatalf("failed to create config dir: %v", errMkdirConfig)
	}
	configPath := filepath.Join(configDir, "config.yaml")

	authDir := filepath.Join(tmpDir, "auth")
	if errMkdirAuth := os.MkdirAll(authDir, 0o700); errMkdirAuth != nil {
		t.Fatalf("failed to create auth dir: %v", errMkdirAuth)
	}

	cfg := &proxyconfig.Config{
		SDKConfig: proxyconfig.SDKConfig{
			RequestLog: false,
		},
		AuthDir:           authDir,
		ErrorLogsMaxFiles: 10,
	}

	logger := defaultRequestLoggerFactory(cfg, configPath)
	fileLogger, ok := logger.(*internallogging.FileRequestLogger)
	if !ok {
		t.Fatalf("expected *FileRequestLogger, got %T", logger)
	}

	errLog := fileLogger.LogRequestWithOptions(
		"/v1/chat/completions",
		http.MethodPost,
		map[string][]string{"Content-Type": []string{"application/json"}},
		[]byte(`{"input":"hello"}`),
		http.StatusBadGateway,
		map[string][]string{"Content-Type": []string{"application/json"}},
		[]byte(`{"error":"upstream failure"}`),
		nil,
		nil,
		nil,
		nil,
		nil,
		true,
		"issue-1711",
		time.Now(),
		time.Now(),
	)
	if errLog != nil {
		t.Fatalf("failed to write forced error request log: %v", errLog)
	}

	authLogsDir := filepath.Join(authDir, "logs")
	authEntries, errReadAuthDir := os.ReadDir(authLogsDir)
	if errReadAuthDir != nil {
		t.Fatalf("failed to read auth logs dir %s: %v", authLogsDir, errReadAuthDir)
	}
	foundErrorLogInAuthDir := false
	for _, entry := range authEntries {
		if strings.HasPrefix(entry.Name(), "error-") && strings.HasSuffix(entry.Name(), ".log") {
			foundErrorLogInAuthDir = true
			break
		}
	}
	if !foundErrorLogInAuthDir {
		t.Fatalf("expected forced error log in auth fallback dir %s, got entries: %+v", authLogsDir, authEntries)
	}

	configLogsDir := filepath.Join(configDir, "logs")
	configEntries, errReadConfigDir := os.ReadDir(configLogsDir)
	if errReadConfigDir != nil && !os.IsNotExist(errReadConfigDir) {
		t.Fatalf("failed to inspect config logs dir %s: %v", configLogsDir, errReadConfigDir)
	}
	for _, entry := range configEntries {
		if strings.HasPrefix(entry.Name(), "error-") && strings.HasSuffix(entry.Name(), ".log") {
			t.Fatalf("unexpected forced error log in config dir %s", configLogsDir)
		}
	}
}
