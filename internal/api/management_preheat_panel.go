package api

import "bytes"

var managementPreheatMarker = []byte("window.__cliproxyCodexPreheat")

const managementPreheatScript = `<script>
      (function () {
        if (window.__cliproxyCodexPreheat) return;
        window.__cliproxyCodexPreheat = true;

        var preheatEndpoint = "/v0/management/auth-files/preheat";
        var authFilesEndpoint = "/v0/management/auth-files";
        var authFileStatusEndpoint = "/v0/management/auth-files/status";
        var authFileFieldsEndpoint = "/v0/management/auth-files/fields";
        var apiCallEndpoint = "/v0/management/api-call";
        var codexUsageEndpoint = "https://chatgpt.com/backend-api/wham/usage";
        var codexUsageUserAgent = "codex_cli_rs/0.76.0 (Debian 13.0.0; x86_64) WindowsTerminal";
        var originalFetch = window.fetch ? window.fetch.bind(window) : null;
        var preheatIntervalMs = 1000;
        var autoPreheatLoopIntervalMs = 60000;
        var weeklyWindowSeconds = 604800;
        var state = { files: [], selectedAuthFiles: {}, refreshTimes: {}, showOnlyNoRefreshTime: false, autoRunning: false, autoBusy: false, loading: false, message: "", messageType: "muted", authHeaders: {} };
        var renderTimer = null;
        var refreshTimer = null;
        var suppressObserver = false;

        function methodOf(input, init) {
          return String((init && init.method) || (input && input.method) || "GET").toUpperCase();
        }

        function pathOf(input) {
          var raw = typeof input === "string" ? input : input && (input.url || input.href);
          if (!raw) return "";
          try {
            return new URL(raw, window.location.origin).pathname;
          } catch (_) {
            return "";
          }
        }

        function captureHeader(name, value) {
          var key = String(name || "").toLowerCase();
          if (key === "authorization" && value) state.authHeaders.Authorization = String(value);
          if (key === "x-management-key" && value) state.authHeaders["X-Management-Key"] = String(value);
        }

        function captureHeaders(headers) {
          if (!headers) return;
          if (headers.forEach) {
            headers.forEach(function (value, name) { captureHeader(name, value); });
            return;
          }
          Object.keys(headers).forEach(function (name) { captureHeader(name, headers[name]); });
        }

        function authHeaders() {
          var headers = { "Content-Type": "application/json" };
          if (state.authHeaders.Authorization) headers.Authorization = state.authHeaders.Authorization;
          if (state.authHeaders["X-Management-Key"]) headers["X-Management-Key"] = state.authHeaders["X-Management-Key"];
          try {
            var key = window.localStorage && (localStorage.getItem("managementKey") || localStorage.getItem("management-key"));
            if (key && !headers.Authorization) headers.Authorization = "Bearer " + key;
            if (key && !headers["X-Management-Key"]) headers["X-Management-Key"] = key;
          } catch (_) {
            return headers;
          }
          return headers;
        }

        function normalizeStoredRefreshState(value) {
          if (!value || typeof value !== "object") return null;
          if (!value.fetched_refresh_time) return { fetched_refresh_time: false };
          var out = {
            fetched_refresh_time: true,
            exact_seven_day_refresh: value.exact_seven_day_refresh === true,
            preheat_needed: value.preheat_needed === true,
            fetched_at: typeof value.fetched_at === "string" ? value.fetched_at : new Date().toISOString()
          };
          if (!out.exact_seven_day_refresh && typeof value.weekly_reset_at === "string" && !isNaN(Date.parse(value.weekly_reset_at))) {
            out.weekly_reset_at = value.weekly_reset_at;
          }
          return out;
        }

        function parseAuthFiles(data) {
          var files = Array.isArray(data) ? data : data && Array.isArray(data.files) ? data.files : [];
          state.files = files.filter(function (file) {
            var provider = String((file && (file.provider || file.type)) || "").trim().toLowerCase();
            return provider === "codex" && authIndexOf(file);
          });
          var nextRefreshTimes = {};
          state.files.forEach(function (file) {
            var index = authIndexOf(file);
            var normalized = normalizeStoredRefreshState(file);
            if (normalized && normalized.fetched_refresh_time) nextRefreshTimes[index] = normalized;
            if (index && state.selectedAuthFiles[index]) state.selectedAuthFiles[index] = file;
          });
          state.refreshTimes = nextRefreshTimes;
          scheduleRender();
          return state.files;
        }

        function authIndexOf(file) {
          return String((file && (file.auth_index || file.authIndex || file["auth-index"])) || "").trim();
        }

        function labelOf(file) {
          return String((file && (file.label || file.email || file.account || file.name || file.id || authIndexOf(file))) || "Codex");
        }

        function trackFetch(input, init) {
          captureHeaders(input && input.headers);
          captureHeaders(init && init.headers);
          var result = originalFetch.apply(this, arguments);
          var method = methodOf(input, init);
          var path = pathOf(input);
          if (method === "GET" && path === authFilesEndpoint) {
            result.then(function (response) {
              if (!response || !response.ok || !response.clone) return;
              response.clone().json().then(parseAuthFiles).catch(function () { return undefined; });
            }).catch(function () { return undefined; });
          } else if (method !== "GET" && path.indexOf(authFilesEndpoint) === 0) {
            result.then(function (response) {
              if (!response || !response.ok) return;
              refreshAuthFilesSoon(250);
            }).catch(function () { return undefined; });
          }
          return result;
        }

        function trackXHR() {
          if (!window.XMLHttpRequest || XMLHttpRequest.prototype.__cliproxyCodexPreheatTracked) return;
          var originalXHROpen = XMLHttpRequest.prototype.open;
          var originalXHRSend = XMLHttpRequest.prototype.send;
          var originalXHRSetRequestHeader = XMLHttpRequest.prototype.setRequestHeader;
          XMLHttpRequest.prototype.__cliproxyCodexPreheatTracked = true;
          XMLHttpRequest.prototype.open = function (method, url) {
            this.__cliproxyCodexPreheatMethod = String(method || "GET").toUpperCase();
            this.__cliproxyCodexPreheatPath = pathOf(url);
            return originalXHROpen.apply(this, arguments);
          };
          XMLHttpRequest.prototype.setRequestHeader = function (name, value) {
            captureHeader(name, value);
            return originalXHRSetRequestHeader.apply(this, arguments);
          };
          XMLHttpRequest.prototype.send = function () {
            var xhr = this;
            var method = xhr.__cliproxyCodexPreheatMethod || "GET";
            var path = xhr.__cliproxyCodexPreheatPath || "";
            if (path === authFilesEndpoint || (method !== "GET" && path.indexOf(authFilesEndpoint) === 0)) {
              xhr.addEventListener("load", function () {
                if (xhr.status < 200 || xhr.status >= 300) return;
                if (method === "GET" && path === authFilesEndpoint) {
                  try { parseAuthFiles(JSON.parse(xhr.responseText || "{}")); } catch (_) { return undefined; }
                } else if (method !== "GET") {
                  refreshAuthFilesSoon(250);
                }
              });
            }
            return originalXHRSend.apply(this, arguments);
          };
        }

        if (originalFetch) {
          window.fetch = trackFetch;
        }
        trackXHR();
        document.addEventListener("change", handleSelectionChange, true);

        function ensureStyle() {
          if (document.getElementById("codex-preheat-style")) return;
          var style = document.createElement("style");
          style.id = "codex-preheat-style";
          style.textContent = "#codex-preheat-panel{margin:12px 0 16px;padding:12px 14px;border:1px solid var(--border-color,#e3e1db);border-radius:12px;background:var(--floating-surface,#fffdf9);box-shadow:var(--shadow,0 1px 2px #00000014);display:flex;align-items:center;gap:10px;flex-wrap:wrap}#codex-preheat-panel button{height:34px;border:1px solid var(--primary-color,#8b8680);border-radius:8px;background:var(--primary-color,#8b8680);color:var(--primary-contrast,#fff);padding:0 12px;cursor:pointer}#codex-preheat-panel button[data-preheat-action='toggle-missing']{background:var(--bg-secondary,#f6f3ed);color:var(--text-primary,#2d2926)}#codex-preheat-panel button:disabled{cursor:not-allowed;opacity:.55}#codex-preheat-panel .codex-preheat-message{font-size:12px;color:var(--text-secondary,#6d6760)}#codex-preheat-panel .codex-preheat-message.success{color:var(--success-color,#10b981)}#codex-preheat-panel .codex-preheat-message.error{color:var(--error-color,#c65746)}#codex-preheat-panel .codex-preheat-counts{display:flex;gap:6px;flex-wrap:wrap;font-size:12px;color:var(--text-secondary,#6d6760)}#codex-preheat-panel .codex-preheat-pill,.codex-preheat-row-badge{border:1px solid var(--border-color,#e3e1db);border-radius:999px;background:var(--bg-secondary,#f6f3ed);padding:2px 8px;white-space:nowrap}.codex-preheat-row-badge{display:inline-flex;margin:4px 6px 4px 0;font-size:11px;color:var(--text-secondary,#6d6760)}.codex-preheat-row-badge.ready{border-color:var(--success-color,#10b981);color:var(--success-color,#10b981)}.codex-preheat-row-badge.scheduled{border-color:var(--primary-color,#8b8680);color:var(--text-primary,#2d2926)}";
          document.head.appendChild(style);
        }

        function shouldShow() {
          var path = locationPath();
          return path === "/auth-files";
        }

        function locationPath() {
          var hash = window.location.hash || "";
          if (hash.indexOf("#") === 0) hash = hash.slice(1);
          return hash.split("?")[0] || window.location.pathname;
        }

        function findHost() {
          var main = document.querySelector("main");
          if (main) return main.firstElementChild || main;
          return document.querySelector("#root") || document.body;
        }

        function render() {
          renderTimer = null;
          var panel = document.getElementById("codex-preheat-panel");
          if (!shouldShow()) {
            if (panel) panel.remove();
            restoreHiddenAuthRows();
            return;
          }
          ensureStyle();
          var host = findHost();
          if (!host) return;
          if (!panel) {
            panel = document.createElement("div");
            panel.id = "codex-preheat-panel";
          }

          var counts = refreshCounts();
          var manualBusy = state.loading || state.autoRunning || state.autoBusy || !originalFetch;
          var manualDisabled = manualBusy ? " disabled" : "";
          var autoDisabled = !originalFetch || (state.loading && !state.autoRunning) ? " disabled" : "";
          var togglePressed = state.showOnlyNoRefreshTime ? ' aria-pressed="true"' : ' aria-pressed="false"';
          suppressObserver = true;
          panel.innerHTML = '<button type="button" data-preheat-action="manual"' + manualDisabled + '>' + (state.loading ? "处理中..." : "预热选中账号") + '</button>' +
            '<button type="button" data-preheat-action="fetch-refresh"' + manualDisabled + '>获取选中刷新时间</button>' +
            '<button type="button" data-preheat-action="toggle-missing"' + togglePressed + '>' + (state.showOnlyNoRefreshTime ? "显示全部凭证" : "只显示未获取刷新时间") + '</button>' +
            '<button type="button" data-preheat-action="auto"' + autoDisabled + '>' + (state.autoRunning ? "停止自动预热" : "启动自动预热") + '</button>' +
            '<span class="codex-preheat-counts"><span class="codex-preheat-pill">未获取刷新时间：' + counts.missing + '</span><span class="codex-preheat-pill">已获取刷新时间：' + counts.fetched + '</span><span class="codex-preheat-pill">需要预热：' + counts.ready + '</span><span class="codex-preheat-pill">等待周限刷新：' + counts.scheduled + '</span></span>' +
            (state.message ? '<span class="codex-preheat-message ' + escapeHtml(state.messageType) + '">' + escapeHtml(state.message) + '</span>' : "");
          panel.querySelector("[data-preheat-action='manual']").addEventListener("click", preheatSelected);
          panel.querySelector("[data-preheat-action='fetch-refresh']").addEventListener("click", fetchSelectedRefreshTimes);
          panel.querySelector("[data-preheat-action='toggle-missing']").addEventListener("click", toggleMissingRefreshTimeFilter);
          panel.querySelector("[data-preheat-action='auto']").addEventListener("click", toggleAutoPreheat);

          if (!panel.parentNode) {
            host.insertBefore(panel, host.firstChild);
          }
          updateAuthRowDecorations();
          window.setTimeout(function () { suppressObserver = false; }, 0);
        }

        function escapeHtml(value) {
          return String(value).replace(/[&<>"]/g, function (ch) {
            return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[ch];
          });
        }

        function scheduleRender() {
          if (renderTimer) return;
          renderTimer = window.setTimeout(render, 80);
        }

        function refreshAuthFilesSoon(delay) {
          if (refreshTimer) window.clearTimeout(refreshTimer);
          refreshTimer = window.setTimeout(function () {
            refreshTimer = null;
            scheduleRender();
          }, delay || 250);
        }

        function fileForNode(node) {
          return state.files.find(function (candidate) { return rowTextMatchesFile(node, candidate); }) || null;
        }

        function rowTextMatchesFile(row, file) {
          var text = String((row && row.textContent) || "");
          return [labelOf(file), file && file.name, file && file.email, file && file.account, file && file.id, authIndexOf(file)].some(function (value) {
            value = String(value || "").trim();
            return value && text.indexOf(value) !== -1;
          });
        }

        function selectedCodexAuthFiles() {
          syncSelectedCodexAuthFiles();
          return Object.keys(state.selectedAuthFiles).map(function (index) { return state.selectedAuthFiles[index]; }).filter(function (file) { return file && authIndexOf(file); });
        }

        function handleSelectionChange(event) {
          var target = event && event.target;
          if (!target || String(target.type || "").toLowerCase() !== "checkbox") return;
          if (target.closest("#codex-preheat-panel") || !isSelectionCheckbox(target)) return;
          window.setTimeout(function () {
            syncSelectedCodexAuthFiles();
            scheduleRender();
          }, 0);
        }

        function syncSelectedCodexAuthFiles() {
          var selected = [];
          var checkboxes = Array.prototype.slice.call(document.querySelectorAll("input[type='checkbox']"));
          checkboxes.forEach(function (checkbox) {
            if (checkbox.closest("#codex-preheat-panel")) return;
            if (!isSelectionCheckbox(checkbox)) return;
            var file = fileForCheckbox(checkbox);
            var index = authIndexOf(file);
            if (!index) return;
            if (checkbox.checked) {
              state.selectedAuthFiles[index] = file;
              if (!selected.some(function (item) { return authIndexOf(item) === index; })) selected.push(file);
            } else {
              delete state.selectedAuthFiles[index];
            }
          });
          return selected;
        }

        function isSelectionCheckbox(checkbox) {
          var className = String(checkbox.className || "");
          var labelClass = String((checkbox.closest("label") && checkbox.closest("label").className) || "");
          return className.indexOf("SelectionCheckbox") !== -1 || labelClass.indexOf("SelectionCheckbox") !== -1 || labelClass.indexOf("cardSelection") !== -1;
        }

        function fileForCheckbox(checkbox) {
          var node = checkbox;
          while (node && node !== document.body) {
            var file = fileForNode(node);
            if (file) return file;
            node = node.parentElement;
          }
          return null;
        }

        function authRowForCheckbox(checkbox) {
          var file = fileForCheckbox(checkbox);
          var node = checkbox;
          var depth = 0;
          while (node && node !== document.body && depth < 8) {
            if (file && rowTextMatchesFile(node, file)) return node;
            node = node.parentElement;
            depth++;
          }
          return checkbox.closest("tr") || checkbox.closest("li") || checkbox.closest("div");
        }

        function updateAuthRowDecorations() {
          var checkboxes = Array.prototype.slice.call(document.querySelectorAll("input[type='checkbox']"));
          checkboxes.forEach(function (checkbox) {
            if (checkbox.closest("#codex-preheat-panel") || !isSelectionCheckbox(checkbox)) return;
            var file = fileForCheckbox(checkbox);
            var row = authRowForCheckbox(checkbox);
            if (!file || !row) return;
            var refresh = refreshStateFor(file);
            var badge = row.querySelector(".codex-preheat-row-badge");
            if (!badge) {
              badge = document.createElement("span");
              badge.className = "codex-preheat-row-badge";
              row.insertBefore(badge, row.firstChild);
            }
            badge.className = "codex-preheat-row-badge" + (refresh.preheat_needed ? " ready" : refresh.weekly_reset_at ? " scheduled" : "");
            badge.textContent = refreshLabel(refresh);
            if (state.showOnlyNoRefreshTime && refresh.fetched_refresh_time) {
              if (row.__cliproxyCodexPreheatDisplay === undefined) row.__cliproxyCodexPreheatDisplay = row.style.display || "";
              row.style.display = "none";
            } else if (row.__cliproxyCodexPreheatDisplay !== undefined) {
              row.style.display = row.__cliproxyCodexPreheatDisplay;
              delete row.__cliproxyCodexPreheatDisplay;
            }
          });
        }

        function restoreHiddenAuthRows() {
          Array.prototype.slice.call(document.querySelectorAll(".codex-preheat-row-badge")).forEach(function (badge) {
            var row = badge.parentElement;
            if (row && row.__cliproxyCodexPreheatDisplay !== undefined) {
              row.style.display = row.__cliproxyCodexPreheatDisplay;
              delete row.__cliproxyCodexPreheatDisplay;
            }
          });
        }

        function refreshStateFor(file) {
          var index = authIndexOf(file);
          return (index && state.refreshTimes[index]) || { fetched_refresh_time: false };
        }

        function refreshLabel(refresh) {
          if (!refresh || !refresh.fetched_refresh_time) return "未获取刷新时间";
          if (refresh.preheat_needed || refresh.exact_seven_day_refresh) return "已获取刷新时间：需要预热";
          if (refresh.weekly_reset_at) return "已获取刷新时间：" + formatResetTime(refresh.weekly_reset_at);
          return "已获取刷新时间";
        }

        function formatResetTime(value) {
          var time = Date.parse(value);
          if (isNaN(time)) return "未知";
          return new Date(time).toLocaleString(undefined, { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false });
        }

        function refreshCounts() {
          var counts = { missing: 0, fetched: 0, ready: 0, scheduled: 0 };
          state.files.forEach(function (file) {
            var refresh = refreshStateFor(file);
            if (!refresh.fetched_refresh_time) {
              counts.missing++;
              return;
            }
            counts.fetched++;
            if (refresh.preheat_needed || refresh.exact_seven_day_refresh) counts.ready++;
            if (refresh.weekly_reset_at) counts.scheduled++;
          });
          return counts;
        }

        function preheatAuthFile(file) {
          return originalFetch(preheatEndpoint, {
            method: "POST",
            headers: authHeaders(),
            body: JSON.stringify({ auth_index: authIndexOf(file) })
          }).then(function (response) {
            return response.text().then(function (text) {
              var data = {};
              try { data = text ? JSON.parse(text) : {}; } catch (_) { data = {}; }
              if (!response.ok) throw new Error(data.message || data.error || text || "预热失败");
              return data;
            });
          });
        }

        function loadAuthFilesForPreheat() {
          return originalFetch(authFilesEndpoint, {
            method: "GET",
            headers: authHeaders()
          }).then(function (response) {
            return response.text().then(function (text) {
              var data = {};
              try { data = text ? JSON.parse(text) : {}; } catch (_) { data = {}; }
              if (!response.ok) throw new Error(data.message || data.error || text || "加载凭证失败");
              return parseAuthFiles(data);
            });
          });
        }

        function managementAPICall(payload) {
          return originalFetch(apiCallEndpoint, {
            method: "POST",
            headers: authHeaders(),
            body: JSON.stringify(payload)
          }).then(function (response) {
            return response.text().then(function (text) {
              var data = {};
              try { data = text ? JSON.parse(text) : {}; } catch (_) { data = {}; }
              if (!response.ok) throw new Error(data.message || data.error || text || "接口调用失败");
              return data;
            });
          });
        }

        function sleep(ms) {
          return new Promise(function (resolve) { window.setTimeout(resolve, ms); });
        }

        function preheatSelectedWithInterval(selected) {
          var chain = Promise.resolve();
          selected.forEach(function (file, index) {
            chain = chain.then(function () {
              if (index > 0) return sleep(preheatIntervalMs).then(function () { return preheatAuthFile(file); });
              return preheatAuthFile(file);
            });
          });
          return chain;
        }

        function fetchRefreshTimesWithInterval(selected) {
          var chain = Promise.resolve();
          selected.forEach(function (file, index) {
            chain = chain.then(function () {
              if (index > 0) return sleep(preheatIntervalMs).then(function () { return fetchRefreshTimeForAuthFile(file); });
              return fetchRefreshTimeForAuthFile(file);
            });
          });
          return chain;
        }

        function isDisabledAuthFile(file) {
          return !!(file && (file.disabled === true || String(file.status || "").trim().toLowerCase() === "disabled"));
        }

        function isAutoQuotaDisabledAuthFile(file) {
          return !!(file && (file.quota_auto_disabled === true || file.quotaAutoDisabled === true));
        }

        function canAutoPreheatAuthFile(file) {
          return !isDisabledAuthFile(file) || isAutoQuotaDisabledAuthFile(file);
        }

        function authStatusNameOf(file) {
          return String((file && (file.id || file.name || file.fileName || file.file_name)) || "").trim();
        }

        function setAuthFileDisabled(file, disabled) {
          var name = authStatusNameOf(file);
          if (!name) return Promise.reject(new Error("缺少凭证名称"));
          return originalFetch(authFileStatusEndpoint, {
            method: "PATCH",
            headers: authHeaders(),
            body: JSON.stringify({ name: name, disabled: disabled })
          }).then(function (response) {
            return response.text().then(function (text) {
              var data = {};
              try { data = text ? JSON.parse(text) : {}; } catch (_) { data = {}; }
              if (!response.ok) throw new Error(data.message || data.error || text || "更新凭证状态失败");
              file.disabled = disabled;
              file.status = disabled ? "disabled" : "active";
              refreshAuthFilesSoon(250);
              return data;
            });
          });
        }

        function enableAuthFileForPreheat(file) {
          if (!isDisabledAuthFile(file)) return Promise.resolve(file);
          if (!isAutoQuotaDisabledAuthFile(file)) return Promise.reject(new Error("凭证已手动禁用，跳过自动预热"));
          return setAuthFileDisabled(file, false).then(function () { return file; });
        }

        function preheatAndRefreshAuthFile(file) {
          return enableAuthFileForPreheat(file).then(function () { return preheatAuthFile(file); }).then(function () { return fetchRefreshTimeForAuthFile(file); });
        }

        function preheatAndRefreshWithInterval(files) {
          var chain = Promise.resolve();
          files.forEach(function (file, index) {
            chain = chain.then(function () {
              if (index > 0) return sleep(preheatIntervalMs).then(function () { return preheatAndRefreshAuthFile(file); });
              return preheatAndRefreshAuthFile(file);
            });
          });
          return chain;
        }

        function numberValue(value) {
          if (typeof value === "number" && isFinite(value)) return value;
          if (typeof value === "string") {
            var trimmed = value.trim();
            if (!trimmed) return null;
            var parsed = Number(trimmed);
            if (isFinite(parsed)) return parsed;
          }
          return null;
        }

        function parseMaybeJSON(value) {
          if (!value) return null;
          if (typeof value === "object") return value;
          if (typeof value !== "string") return null;
          try {
            return JSON.parse(value);
          } catch (_) {
            return null;
          }
        }

        function base64URLDecode(value) {
          try {
            var normalized = String(value || "").replace(/-/g, "+").replace(/_/g, "/");
            normalized = normalized + "====".slice(0, (4 - normalized.length % 4) % 4);
            if (typeof window !== "undefined" && typeof window.atob === "function") return window.atob(normalized);
            if (typeof atob === "function") return atob(normalized);
          } catch (_) {
            return "";
          }
          return "";
        }

        function objectFromJSONOrJWT(value) {
          if (!value) return null;
          if (typeof value === "object" && !Array.isArray(value)) return value;
          if (typeof value !== "string") return null;
          var trimmed = value.trim();
          if (!trimmed) return null;
          try {
            var parsed = JSON.parse(trimmed);
            if (parsed && typeof parsed === "object") return parsed;
          } catch (_) {
            // Fall through to JWT parsing.
          }
          var parts = trimmed.split(".");
          if (parts.length < 2) return null;
          try {
            var decoded = base64URLDecode(parts[1]);
            return decoded ? JSON.parse(decoded) : null;
          } catch (_) {
            return null;
          }
        }

        function chatGPTAccountIDFromValue(value) {
          var obj = objectFromJSONOrJWT(value);
          if (!obj) return "";
          var nested = obj["https://api.openai.com/auth"];
          return String(obj.chatgpt_account_id || obj.chatgptAccountId || (nested && (nested.chatgpt_account_id || nested.chatgptAccountId)) || "").trim();
        }

        function chatGPTAccountIDOf(file) {
          var metadata = file && file.metadata && typeof file.metadata === "object" ? file.metadata : null;
          var attributes = file && file.attributes && typeof file.attributes === "object" ? file.attributes : null;
          var candidates = [file && file.id_token, file && file.idToken, metadata && metadata.id_token, metadata && metadata.idToken, attributes && attributes.id_token, attributes && attributes.idToken];
          for (var i = 0; i < candidates.length; i++) {
            var accountID = chatGPTAccountIDFromValue(candidates[i]);
            if (accountID) return accountID;
          }
          return "";
        }

        function weeklyWindowOf(usage) {
          if (!usage || typeof usage !== "object") return null;
          var rateLimits = [usage.rate_limit || usage.rateLimit, usage.code_review_rate_limit || usage.codeReviewRateLimit];
          for (var i = 0; i < rateLimits.length; i++) {
            var rateLimit = rateLimits[i];
            if (!rateLimit || typeof rateLimit !== "object") continue;
            var windows = [rateLimit.primary_window || rateLimit.primaryWindow, rateLimit.secondary_window || rateLimit.secondaryWindow];
            for (var j = 0; j < windows.length; j++) {
              var windowValue = windows[j];
              if (!windowValue || typeof windowValue !== "object") continue;
              var seconds = numberValue(windowValue.limit_window_seconds || windowValue.limitWindowSeconds);
              if (Math.round(seconds || 0) === weeklyWindowSeconds) return windowValue;
            }
          }
          return null;
        }

        function normalizeCodexUsageRefreshState(usage) {
          var weeklyWindow = weeklyWindowOf(usage);
          if (!weeklyWindow) return { fetched_refresh_time: false };
          var now = Date.now();
          var resetAfter = numberValue(weeklyWindow.reset_after_seconds || weeklyWindow.resetAfterSeconds);
          var resetAt = numberValue(weeklyWindow.reset_at || weeklyWindow.resetAt);
          var resetAtDelta = resetAt !== null && resetAt > 0 ? resetAt - Math.floor(now / 1000) : null;
          var normalized = {
            fetched_refresh_time: true,
            fetched_at: new Date(now).toISOString(),
            exact_seven_day_refresh: false,
            preheat_needed: false
          };
          if ((resetAfter !== null && Math.round(resetAfter) === weeklyWindowSeconds) || (resetAtDelta !== null && Math.round(resetAtDelta) === weeklyWindowSeconds)) {
            normalized.exact_seven_day_refresh = true;
            normalized.preheat_needed = true;
            return normalized;
          }
          var resetMs = 0;
          if (resetAt !== null && resetAt > 0) resetMs = resetAt * 1000;
          if (!resetMs && resetAfter !== null && resetAfter >= 0) resetMs = now + resetAfter * 1000;
          if (!resetMs || isNaN(resetMs)) return { fetched_refresh_time: false };
          normalized.weekly_reset_at = new Date(resetMs).toISOString();
          return normalized;
        }

        function refreshStatePayload(normalized) {
          normalized = normalized && typeof normalized === "object" ? normalized : { fetched_refresh_time: false };
          return {
            fetched_refresh_time: normalized.fetched_refresh_time === true,
            exact_seven_day_refresh: normalized.exact_seven_day_refresh === true,
            preheat_needed: normalized.preheat_needed === true,
            weekly_reset_at: typeof normalized.weekly_reset_at === "string" ? normalized.weekly_reset_at : "",
            fetched_at: typeof normalized.fetched_at === "string" ? normalized.fetched_at : ""
          };
        }

        function persistRefreshStateForAuthFile(file, normalized) {
          var name = authStatusNameOf(file);
          if (!name) return Promise.reject(new Error("缺少凭证名称"));
          var payload = refreshStatePayload(normalized);
          payload.name = name;
          return originalFetch(authFileFieldsEndpoint, {
            method: "PATCH",
            headers: authHeaders(),
            body: JSON.stringify(payload)
          }).then(function (response) {
            return response.text().then(function (text) {
              var data = {};
              try { data = text ? JSON.parse(text) : {}; } catch (_) { data = {}; }
              if (!response.ok) throw new Error(data.message || data.error || text || "保存刷新时间失败");
              Object.keys(payload).forEach(function (key) {
                if (key !== "name") file[key] = payload[key];
              });
              return normalized;
            });
          });
        }

        function fetchRefreshTimeForAuthFile(file) {
          var index = authIndexOf(file);
          if (!index) return Promise.reject(new Error("缺少 Codex 凭证索引"));
          var headers = {
            Authorization: "Bearer $TOKEN$",
            "Content-Type": "application/json",
            "User-Agent": codexUsageUserAgent
          };
          var accountID = chatGPTAccountIDOf(file);
          if (accountID) headers["Chatgpt-Account-Id"] = accountID;
          return managementAPICall({
            authIndex: index,
            method: "GET",
            url: codexUsageEndpoint,
            header: headers
          }).then(function (data) {
            var statusCode = numberValue(data.status_code || data.statusCode) || 0;
            if (statusCode < 200 || statusCode >= 300) throw new Error(String(data.body || data.bodyText || "获取刷新时间失败"));
            var usage = parseMaybeJSON(data.body || data.bodyText);
            var normalized = normalizeCodexUsageRefreshState(usage);
            if (!normalized.fetched_refresh_time) {
              return persistRefreshStateForAuthFile(file, normalized).then(function () {
                delete state.refreshTimes[index];
                scheduleRender();
                return normalized;
              });
            }
            return persistRefreshStateForAuthFile(file, normalized).then(function () {
              state.refreshTimes[index] = normalized;
              scheduleRender();
              return normalized;
            });
          });
        }

        function exactSevenDayRefreshAuthFiles(files) {
          var candidates = Array.isArray(files) ? files : state.files;
          return candidates.filter(function (file) {
            var refresh = refreshStateFor(file);
            return authIndexOf(file) && canAutoPreheatAuthFile(file) && refresh.fetched_refresh_time && refresh.exact_seven_day_refresh && refresh.preheat_needed;
          });
        }

        function sortWeeklyResetAuthFiles(files) {
          var candidates = Array.isArray(files) ? files : state.files;
          return candidates.filter(function (file) {
            var refresh = refreshStateFor(file);
            return authIndexOf(file) && canAutoPreheatAuthFile(file) && refresh.fetched_refresh_time && refresh.weekly_reset_at && !isNaN(Date.parse(refresh.weekly_reset_at));
          }).sort(function (a, b) {
            return Date.parse(refreshStateFor(a).weekly_reset_at) - Date.parse(refreshStateFor(b).weekly_reset_at);
          });
        }

        function preheatSelected() {
          if (state.loading || state.autoRunning || state.autoBusy || !originalFetch) return;
          var selected = selectedCodexAuthFiles();
          if (!selected.length) {
            state.messageType = "error";
            state.message = "请先勾选 Codex 凭证";
            scheduleRender();
            return;
          }
          state.loading = true;
          state.message = "";
          scheduleRender();
          preheatSelectedWithInterval(selected).then(function () {
            state.messageType = "success";
            state.message = "已预热选中账号：" + selected.length + " 个";
          }).catch(function (error) {
            state.messageType = "error";
            state.message = error && error.message ? error.message : "预热失败";
          }).finally(function () {
            state.loading = false;
            scheduleRender();
          });
        }

        function fetchSelectedRefreshTimes() {
          if (state.loading || state.autoRunning || state.autoBusy || !originalFetch) return;
          var selected = selectedCodexAuthFiles();
          if (!selected.length) {
            state.messageType = "error";
            state.message = "请先勾选 Codex 凭证";
            scheduleRender();
            return;
          }
          state.loading = true;
          state.message = "";
          scheduleRender();
          fetchRefreshTimesWithInterval(selected).then(function () {
            state.messageType = "success";
            state.message = "已获取选中刷新时间：" + selected.length + " 个";
          }).catch(function (error) {
            state.messageType = "error";
            state.message = error && error.message ? error.message : "获取刷新时间失败";
          }).finally(function () {
            state.loading = false;
            scheduleRender();
          });
        }

        function toggleMissingRefreshTimeFilter() {
          state.showOnlyNoRefreshTime = !state.showOnlyNoRefreshTime;
          updateAuthRowDecorations();
          scheduleRender();
        }

        function toggleAutoPreheat() {
          if (state.autoRunning) {
            state.autoRunning = false;
            state.messageType = "muted";
            state.message = "自动预热已停止";
            scheduleRender();
            return;
          }
          if (!originalFetch) return;
          state.autoRunning = true;
          state.messageType = "muted";
          state.message = "自动预热已启动";
          scheduleRender();
          autoPreheatLoop();
        }

        function autoPreheatLoop() {
          if (!state.autoRunning || state.autoBusy) return Promise.resolve();
          state.autoBusy = true;
          return loadAuthFilesForPreheat().then(function (files) {
            if (!state.autoRunning) return "stop";
            var exact = exactSevenDayRefreshAuthFiles(files);
            if (exact.length) {
              state.messageType = "muted";
              state.message = "正在处理已到达 7 天刷新时间的 Codex 账号：" + exact.length + " 个";
              scheduleRender();
              return preheatAndRefreshWithInterval(exact).then(function () { return "continue"; });
            }
            var scheduled = sortWeeklyResetAuthFiles(files);
            var next = scheduled[0];
            if (!next) {
              state.messageType = "muted";
              state.message = "没有已获取周限刷新时间的 Codex 账号";
              scheduleRender();
              return "wait";
            }
            var refresh = refreshStateFor(next);
            var resetTime = Date.parse(refresh.weekly_reset_at);
            if (!isNaN(resetTime) && resetTime <= Date.now()) {
              state.messageType = "muted";
              state.message = "正在预热到达周限刷新时间的账号：" + labelOf(next);
              scheduleRender();
              return preheatAndRefreshAuthFile(next).then(function () { return "continue"; });
            }
            state.messageType = "muted";
            state.message = "等待最近周限刷新：" + labelOf(next) + " " + formatResetTime(refresh.weekly_reset_at);
            scheduleRender();
            return "wait";
          }).then(function (action) {
            state.autoBusy = false;
            scheduleRender();
            if (!state.autoRunning) return undefined;
            if (action === "continue") return sleep(autoPreheatLoopIntervalMs).then(autoPreheatLoop);
            return sleep(autoPreheatLoopIntervalMs).then(autoPreheatLoop);
          }).catch(function (error) {
            state.autoBusy = false;
            state.autoRunning = false;
            state.messageType = "error";
            state.message = error && error.message ? error.message : "自动预热失败";
            scheduleRender();
          });
        }

        var observer = new MutationObserver(function () {
          if (!suppressObserver) scheduleRender();
        });
        observer.observe(document.body, { childList: true, subtree: true });
        ["pushState", "replaceState"].forEach(function (name) {
          var original = history[name];
          history[name] = function () {
            var result = original.apply(this, arguments);
            scheduleRender();
            return result;
          };
        });
        window.addEventListener("popstate", scheduleRender);
        scheduleRender();
      })();
    </script>`

func injectManagementPreheatPanel(html []byte) []byte {
	if len(html) == 0 {
		return html
	}
	if bytes.Contains(html, []byte(managementPreheatScript)) {
		return html
	}

	html = removeManagementPreheatScripts(html)
	return appendManagementPreheatScript(html)
}

func removeManagementPreheatScripts(html []byte) []byte {
	out := html
	searchStart := 0
	for {
		lower := bytes.ToLower(out)
		relOpen := bytes.Index(lower[searchStart:], []byte("<script"))
		if relOpen < 0 {
			return out
		}

		open := searchStart + relOpen
		relOpenEnd := bytes.IndexByte(lower[open:], '>')
		if relOpenEnd < 0 {
			return out
		}

		contentStart := open + relOpenEnd + 1
		relClose := bytes.Index(lower[contentStart:], []byte("</script>"))
		if relClose < 0 {
			return out
		}

		closeStart := contentStart + relClose
		closeEnd := closeStart + len("</script>")
		if bytes.Contains(out[contentStart:closeStart], managementPreheatMarker) {
			next := make([]byte, 0, len(out)-(closeEnd-open))
			next = append(next, out[:open]...)
			next = append(next, out[closeEnd:]...)
			out = next
			searchStart = open
			continue
		}

		searchStart = closeEnd
	}
}

func appendManagementPreheatScript(html []byte) []byte {
	insertion := []byte("\n    " + managementPreheatScript + "\n  ")
	bodyEnd := []byte("</body>")
	idx := bytes.LastIndex(html, bodyEnd)
	if idx < 0 {
		out := make([]byte, 0, len(html)+len(insertion))
		out = append(out, html...)
		out = append(out, insertion...)
		return out
	}

	out := make([]byte, 0, len(html)+len(insertion))
	out = append(out, html[:idx]...)
	out = append(out, insertion...)
	out = append(out, html[idx:]...)
	return out
}
