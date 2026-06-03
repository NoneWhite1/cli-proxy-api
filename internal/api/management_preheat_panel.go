package api

import "bytes"

var managementPreheatMarker = []byte("window.__cliproxyCodexPreheat")

const managementPreheatScript = `<script>
      (function () {
        if (window.__cliproxyCodexPreheat) return;
        window.__cliproxyCodexPreheat = true;

        var authFilesEndpoint = "/v0/management/auth-files";
        var preheatJobEndpoint = "/v0/management/auth-files/preheat/jobs";
        var preheatAutoEndpoint = "/v0/management/auth-files/preheat/auto";
        var originalFetch = window.fetch ? window.fetch.bind(window) : null;
        var jobPollIntervalMs = 1000;
        var autoStatusPollIntervalMs = 5000;
        var state = { files: [], authFilesByIndex: {}, selectedAuthFiles: {}, refreshTimes: {}, showOnlyNoRefreshTime: false, autoRunning: false, autoBusy: false, autoStatusLoaded: false, loading: false, message: "", messageType: "muted", authHeaders: {}, jobPollTimer: null, autoPollTimer: null };
        var renderTimer = null;
        var refreshTimer = null;
        var selectionSyncTimer = null;
        var hostFilteredSelectionPending = false;
        var hostSelectionClearPending = false;
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
          var nextAuthFilesByIndex = {};
          var nextRefreshTimes = {};
          state.files = files.filter(isCodexAuthFile);
          state.files.forEach(function (file) {
            var index = authIndexOf(file);
            nextAuthFilesByIndex[index] = file;
            var normalized = normalizeStoredRefreshState(file);
            if (normalized && normalized.fetched_refresh_time) nextRefreshTimes[index] = normalized;
          });
          var nextSelectedAuthFiles = {};
          Object.keys(state.selectedAuthFiles).forEach(function (index) {
            var selected = nextAuthFilesByIndex[index] || state.authFilesByIndex[index] || state.selectedAuthFiles[index];
            if (selected && isCodexAuthFile(selected)) {
              nextAuthFilesByIndex[index] = nextAuthFilesByIndex[index] || selected;
              nextSelectedAuthFiles[index] = nextAuthFilesByIndex[index];
            }
          });
          state.authFilesByIndex = nextAuthFilesByIndex;
          state.refreshTimes = nextRefreshTimes;
          state.selectedAuthFiles = nextSelectedAuthFiles;
          scheduleRender();
          return state.files;
        }

        function authIndexOf(file) {
          return String((file && (file.auth_index || file.authIndex || file["auth-index"])) || "").trim();
        }

        function isCodexAuthFile(file) {
          var provider = String((file && (file.provider || file.type)) || "").trim().toLowerCase();
          return provider === "codex" && authIndexOf(file);
        }

        function rememberAuthFile(file) {
          var index = authIndexOf(file);
          if (!index) return null;
          state.authFilesByIndex[index] = file;
          return file;
        }

        function knownCodexAuthFiles() {
          var seen = {};
          var files = [];
          function add(file) {
            var index = authIndexOf(file);
            if (!index || seen[index] || !isCodexAuthFile(file)) return;
            seen[index] = true;
            files.push(file);
          }
          state.files.forEach(add);
          Object.keys(state.authFilesByIndex).forEach(function (index) { add(state.authFilesByIndex[index]); });
          return files;
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
        document.addEventListener("click", handleSelectionChange, true);
        document.addEventListener("input", handleSelectionChange, true);
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
          ensureAutoStatus();
          ensureStyle();
          var host = findHost();
          if (!host) return;
          if (!panel) {
            panel = document.createElement("div");
            panel.id = "codex-preheat-panel";
          }

          var counts = refreshCounts();
          var manualBusy = state.loading || state.autoBusy || !originalFetch;
          var manualDisabled = manualBusy ? " disabled" : "";
          var autoDisabled = !originalFetch || (state.loading && !state.autoRunning) ? " disabled" : "";
          var togglePressed = state.showOnlyNoRefreshTime ? ' aria-pressed="true"' : ' aria-pressed="false"';
          suppressObserver = true;
          panel.innerHTML = '<button type="button" data-preheat-action="manual"' + manualDisabled + '>' + (state.loading ? "处理中..." : "预热选中账号") + '</button>' +
            '<button type="button" data-preheat-action="fetch-refresh"' + manualDisabled + '>获取选中刷新时间</button>' +
            '<button type="button" data-preheat-action="toggle-missing"' + togglePressed + '>' + (state.showOnlyNoRefreshTime ? "显示全部凭证" : "只显示未获取刷新时间") + '</button>' +
            '<button type="button" data-preheat-action="auto"' + autoDisabled + '>' + (state.autoRunning ? "停止自动预热" : "启动自动预热") + '</button>' +
            '<span class="codex-preheat-counts"><span class="codex-preheat-pill">未获取刷新时间：' + counts.missing + '</span><span class="codex-preheat-pill">已获取刷新时间：' + counts.fetched + '</span><span class="codex-preheat-pill">需要预热：' + counts.ready + '</span><span class="codex-preheat-pill">等待限额刷新：' + counts.scheduled + '</span></span>' +
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
            if (!originalFetch) {
              scheduleRender();
              return;
            }
            loadAuthFilesForPreheat().then(function () { scheduleRender(); }).catch(function () { scheduleRender(); });
          }, delay || 250);
        }

        function fileForNode(node) {
          return knownCodexAuthFiles().find(function (candidate) { return rowTextMatchesFile(node, candidate); }) || null;
        }

        function rowTextMatchesFile(row, file) {
          var text = String((row && row.textContent) || "");
          return [labelOf(file), file && file.name, file && file.email, file && file.account, file && file.id, authIndexOf(file)].some(function (value) {
            value = String(value || "").trim();
            return value && text.indexOf(value) !== -1;
          });
        }

        function readHostAuthFilesUIState() {
          function read(storage) {
            if (!storage) return null;
            var value = storage.getItem("authFilesPage.uiState");
            if (!value) return null;
            var parsed = JSON.parse(value);
            return parsed && typeof parsed === "object" ? parsed : null;
          }
          try {
            return read(window.localStorage) || read(window.sessionStorage) || {};
          } catch (_) {
            return {};
          }
        }

        function normalizeHostProvider(value) {
          value = String(value || "").trim().toLowerCase().replace(/_/g, "-");
          if (value === "x-ai" || value === "grok") return "xai";
          return value;
        }

        function isRuntimeOnlyAuthFile(file) {
          var value = file && (file.runtime_only !== undefined ? file.runtime_only : file.runtimeOnly);
          if (typeof value === "boolean") return value;
          if (typeof value === "string") return value.trim().toLowerCase() === "true";
          return false;
        }

        function hostProblemMessage(file) {
          var value = file && (file.status_message !== undefined ? file.status_message : file.statusMessage);
          if (typeof value === "string") return value.trim();
          return value == null ? "" : String(value).trim();
        }

        function hostSearchMatches(file, search) {
          search = String(search || "").trim();
          if (!search) return true;
          var regex = null;
          if (search.indexOf("*") !== -1) {
            regex = new RegExp(search.split("*").map(function (part) { return part.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"); }).join(".*"), "i");
          }
          var lowerSearch = search.toLowerCase();
          return [file && file.name, file && file.type, file && file.provider].some(function (value) {
            value = String(value || "");
            return regex ? regex.test(value) : value.toLowerCase().indexOf(lowerSearch) !== -1;
          });
        }

        function hostFileMatchesUIState(file, ui) {
          ui = ui && typeof ui === "object" ? ui : {};
          if (!isCodexAuthFile(file) || isRuntimeOnlyAuthFile(file)) return false;
          var filter = normalizeHostProvider(ui.filter || "all");
          var provider = normalizeHostProvider(file && (file.type || file.provider));
          if (filter && filter !== "all" && provider !== filter) return false;
          if (ui.problemOnly === true && !hostProblemMessage(file)) return false;
          if (ui.disabledOnly === true && file && file.disabled !== true) return false;
          return hostSearchMatches(file, ui.search);
        }

        function hostFilteredCodexAuthFiles() {
          var ui = readHostAuthFilesUIState();
          return knownCodexAuthFiles().filter(function (file) { return hostFileMatchesUIState(file, ui); });
        }

        function selectHostFilteredCodexAuthFiles() {
          var selected = [];
          hostFilteredCodexAuthFiles().forEach(function (file) {
            var index = authIndexOf(file);
            if (!index) return;
            rememberAuthFile(file);
            state.selectedAuthFiles[index] = state.authFilesByIndex[index] || file;
            selected.push(state.selectedAuthFiles[index]);
          });
          return selected;
        }

        function applyPendingHostSelection() {
          if (hostSelectionClearPending) {
            hostSelectionClearPending = false;
            hostFilteredSelectionPending = false;
            state.selectedAuthFiles = {};
            return [];
          }
          if (!hostFilteredSelectionPending) return [];
          hostFilteredSelectionPending = false;
          return selectHostFilteredCodexAuthFiles();
        }

        function hostControlFor(target) {
          var node = target;
          while (node && node !== document.body) {
            var tag = String(node.tagName || "").toLowerCase();
            var role = node.getAttribute && String(node.getAttribute("role") || "").toLowerCase();
            if (tag === "button" || role === "button") return node;
            node = node.parentElement;
          }
          return null;
        }

        function hostControlText(target) {
          var control = hostControlFor(target);
          if (!control) return "";
          return [control.textContent, control.getAttribute && control.getAttribute("title"), control.getAttribute && control.getAttribute("aria-label")].map(function (value) { return String(value || "").trim(); }).join(" ").replace(/\s+/g, " ");
        }

        function isHostFilteredSelectionControl(target) {
          var text = hostControlText(target);
          return text.indexOf("全选筛选结果") !== -1 || text.indexOf("全選篩選結果") !== -1 || text.indexOf("Select filtered") !== -1 || text.indexOf("Выбрать по фильтру") !== -1;
        }

        function isHostSelectionClearControl(target) {
          var text = hostControlText(target);
          return text.indexOf("取消选择") !== -1 || text.indexOf("取消選擇") !== -1 || text.indexOf("Deselect") !== -1 || text.indexOf("Отменить") !== -1;
        }

        function selectedCodexAuthFiles() {
          syncSelectedCodexAuthFiles();
          return Object.keys(state.selectedAuthFiles).map(function (index) { return state.authFilesByIndex[index] || state.selectedAuthFiles[index]; }).filter(function (file) { return file && authIndexOf(file); });
        }

        function handleSelectionChange(event) {
          var target = event && event.target;
          if (!target) return;
          if (target.closest("#codex-preheat-panel")) return;
          if (isHostFilteredSelectionControl(target)) hostFilteredSelectionPending = true;
          if (isHostSelectionClearControl(target)) hostSelectionClearPending = true;
          if (String(target.type || "").toLowerCase() === "checkbox" && !isSelectionCheckbox(target)) return;
          scheduleSelectionSync();
        }

        function scheduleSelectionSync() {
          if (selectionSyncTimer) window.clearTimeout(selectionSyncTimer);
          selectionSyncTimer = window.setTimeout(function () {
            selectionSyncTimer = null;
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
              rememberAuthFile(file);
              state.selectedAuthFiles[index] = state.authFilesByIndex[index] || file;
              if (!selected.some(function (item) { return authIndexOf(item) === index; })) selected.push(state.selectedAuthFiles[index]);
            } else {
              delete state.selectedAuthFiles[index];
            }
          });
          applyPendingHostSelection();
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

        function classNameHasPrefix(className, prefix) {
          return String(className || "").split(/\s+/).some(function (part) {
            return part.indexOf(prefix) === 0 || part.indexOf("__" + prefix) !== -1;
          });
        }

        function isAuthFileCard(node) {
          var className = node && node.className;
          return classNameHasPrefix(className, "fileCard___") || classNameHasPrefix(className, "fileCardCompact___");
        }

        function isAuthFileRow(node) {
          var tag = String((node && node.tagName) || "").toLowerCase();
          return tag === "tr" || tag === "li";
        }

        function authContainerForCheckbox(checkbox, file) {
          var node = checkbox;
          while (node && node !== document.body) {
            if (file && (isAuthFileCard(node) || isAuthFileRow(node)) && rowTextMatchesFile(node, file)) return node;
            node = node.parentElement;
          }
          return null;
        }

        function authRowForCheckbox(checkbox) {
          return authContainerForCheckbox(checkbox, fileForCheckbox(checkbox));
        }

        function updateAuthRowDecorations() {
          var checkboxes = Array.prototype.slice.call(document.querySelectorAll("input[type='checkbox']"));
          checkboxes.forEach(function (checkbox) {
            if (checkbox.closest("#codex-preheat-panel") || !isSelectionCheckbox(checkbox)) return;
            var file = fileForCheckbox(checkbox);
            var row = authRowForCheckbox(checkbox);
            if (!file || !row) return;
            var index = authIndexOf(file);
            if (index && state.selectedAuthFiles[index] && !checkbox.checked) checkbox.checked = true;
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
          knownCodexAuthFiles().forEach(function (file) {
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

        function loadAuthFilesForPreheat() {
          return jsonRequest(authFilesEndpoint, { method: "GET", headers: authHeaders() }, "加载凭证失败").then(function (data) {
            parseAuthFiles(data);
            return knownCodexAuthFiles();
          });
        }

        function jsonRequest(endpoint, init, fallbackMessage) {
          return originalFetch(endpoint, init || {}).then(function (response) {
            return response.text().then(function (text) {
              var data = {};
              try { data = text ? JSON.parse(text) : {}; } catch (_) { data = {}; }
              if (!response.ok) throw new Error(data.message || data.error || text || fallbackMessage || "请求失败");
              return data;
            });
          });
        }

        function selectedAuthIndexes(files) {
          var seen = {};
          var indexes = [];
          files.forEach(function (file) {
            var index = authIndexOf(file);
            if (!index || seen[index]) return;
            seen[index] = true;
            indexes.push(index);
          });
          return indexes;
        }

        function startPreheatJob(operation, files) {
          var indexes = selectedAuthIndexes(files);
          if (!indexes.length) return Promise.reject(new Error("请先勾选 Codex 凭证"));
          state.loading = true;
          state.messageType = "muted";
          state.message = operation === "refresh_time" ? "已提交后台刷新时间任务" : "已提交后台预热任务";
          scheduleRender();
          return jsonRequest(preheatJobEndpoint, {
            method: "POST",
            headers: authHeaders(),
            body: JSON.stringify({ operation: operation, auth_indices: indexes })
          }, "启动后台任务失败").then(function (job) {
            pollPreheatJob(job.job_id, operation);
            return job;
          }).catch(function (error) {
            state.loading = false;
            state.messageType = "error";
            state.message = error && error.message ? error.message : "启动后台任务失败";
            scheduleRender();
          });
        }

        function countOf(job, key) {
          var value = job && job[key];
          if (typeof value === "number" && isFinite(value)) return value;
          var parsed = parseInt(String(value || "0"), 10);
          return isNaN(parsed) ? 0 : parsed;
        }

        function terminalPreheatJobStatus(status) {
          status = String(status || "").toLowerCase();
          return status === "succeeded" || status === "failed" || status === "partial" || status === "completed" || status === "cancelled";
        }

        function actionLabel(operation) {
          if (operation === "refresh_time") return "获取刷新时间";
          if (operation === "preheat_refresh") return "预热并刷新时间";
          return "预热";
        }

        function updateMessageFromJob(job, operation) {
          var total = countOf(job, "total");
          var completed = countOf(job, "completed");
          var failed = countOf(job, "failed");
          var deduped = countOf(job, "deduped");
          var label = actionLabel(operation || (job && job.operation));
          var suffix = "：" + completed + "/" + total;
          if (failed) suffix += "，失败 " + failed;
          if (deduped) suffix += "，跳过重复 " + deduped;
          state.messageType = failed ? "error" : "muted";
          state.message = "后台" + label + "处理中" + suffix;
        }

        function finishJobMessage(job, operation) {
          var total = countOf(job, "total");
          var completed = countOf(job, "completed");
          var failed = countOf(job, "failed");
          var deduped = countOf(job, "deduped");
          var label = actionLabel(operation || (job && job.operation));
          if (String(job && job.status).toLowerCase() === "failed" || failed >= total && total > 0) {
            state.messageType = "error";
            state.message = "后台" + label + "失败：" + failed + "/" + total;
          } else if (failed > 0 || String(job && job.status).toLowerCase() === "partial") {
            state.messageType = "error";
            state.message = "后台" + label + "部分完成：成功 " + (completed - failed) + "，失败 " + failed + (deduped ? "，跳过重复 " + deduped : "");
          } else {
            state.messageType = "success";
            state.message = "后台" + label + "已完成：" + completed + " 个" + (deduped ? "，跳过重复 " + deduped : "");
          }
        }

        function pollPreheatJob(jobID, operation) {
          if (!jobID || !originalFetch) return Promise.resolve();
          if (state.jobPollTimer) {
            window.clearTimeout(state.jobPollTimer);
            state.jobPollTimer = null;
          }
          return jsonRequest(preheatJobEndpoint + "/" + encodeURIComponent(jobID), { method: "GET", headers: authHeaders() }, "获取后台任务状态失败").then(function (job) {
            if (terminalPreheatJobStatus(job.status)) {
              state.loading = false;
              finishJobMessage(job, operation);
              refreshAuthFilesSoon(100);
              scheduleRender();
              return job;
            }
            state.loading = true;
            updateMessageFromJob(job, operation);
            scheduleRender();
            state.jobPollTimer = window.setTimeout(function () { pollPreheatJob(jobID, operation); }, jobPollIntervalMs);
            return job;
          }).catch(function (error) {
            state.loading = false;
            state.messageType = "error";
            state.message = error && error.message ? error.message : "获取后台任务状态失败";
            scheduleRender();
          });
        }

        function ensureAutoStatus() {
          if (state.autoStatusLoaded || !originalFetch || !shouldShow()) return;
          state.autoStatusLoaded = true;
          fetchPreheatAutoStatus(true);
        }

        function applyAutoStatus(data, quiet) {
          data = data && typeof data === "object" ? data : {};
          state.autoRunning = data.enabled === true;
          state.autoBusy = data.busy === true;
          if (!quiet) {
            if (state.autoRunning) {
              state.messageType = "muted";
              state.message = state.autoBusy ? "后端自动预热正在处理" : "后端自动预热已启动";
            } else {
              state.messageType = "muted";
              state.message = "自动预热已停止";
            }
          }
          scheduleRender();
          if (state.autoRunning) pollPreheatAutoStatus();
        }

        function fetchPreheatAutoStatus(quiet) {
          if (!originalFetch) return Promise.resolve();
          return jsonRequest(preheatAutoEndpoint, { method: "GET", headers: authHeaders() }, "获取自动预热状态失败").then(function (data) {
            applyAutoStatus(data, quiet);
            return data;
          }).catch(function (error) {
            if (!quiet) {
              state.messageType = "error";
              state.message = error && error.message ? error.message : "获取自动预热状态失败";
              scheduleRender();
            }
          });
        }

        function pollPreheatAutoStatus() {
          if (state.autoPollTimer) window.clearTimeout(state.autoPollTimer);
          if (!state.autoRunning || !originalFetch) return;
          state.autoPollTimer = window.setTimeout(function () {
            state.autoPollTimer = null;
            fetchPreheatAutoStatus(true);
          }, autoStatusPollIntervalMs);
        }

        function preheatSelected() {
          if (state.loading || state.autoBusy || !originalFetch) return;
          var selected = selectedCodexAuthFiles();
          if (!selected.length) {
            state.messageType = "error";
            state.message = "请先勾选 Codex 凭证";
            scheduleRender();
            return;
          }
          startPreheatJob("preheat", selected);
        }

        function fetchSelectedRefreshTimes() {
          if (state.loading || state.autoBusy || !originalFetch) return;
          var selected = selectedCodexAuthFiles();
          if (!selected.length) {
            state.messageType = "error";
            state.message = "请先勾选 Codex 凭证";
            scheduleRender();
            return;
          }
          startPreheatJob("refresh_time", selected);
        }

        function toggleMissingRefreshTimeFilter() {
          state.showOnlyNoRefreshTime = !state.showOnlyNoRefreshTime;
          updateAuthRowDecorations();
          scheduleRender();
        }

        function toggleAutoPreheat() {
          if (!originalFetch || (state.loading && !state.autoRunning)) return;
          var enable = !state.autoRunning;
          state.autoBusy = true;
          state.messageType = "muted";
          state.message = enable ? "正在启动后端自动预热" : "正在停止后端自动预热";
          scheduleRender();
          return jsonRequest(preheatAutoEndpoint, {
            method: "PATCH",
            headers: authHeaders(),
            body: JSON.stringify({ enabled: enable })
          }, "更新自动预热状态失败").then(function (data) {
            applyAutoStatus(data, true);
            state.autoBusy = data && data.busy === true;
            state.messageType = "muted";
            state.message = enable ? "自动预热已在后端启动" : "自动预热已停止";
            if (!enable && state.autoPollTimer) {
              window.clearTimeout(state.autoPollTimer);
              state.autoPollTimer = null;
            }
            refreshAuthFilesSoon(250);
            scheduleRender();
          }).catch(function (error) {
            state.autoBusy = false;
            state.messageType = "error";
            state.message = error && error.message ? error.message : "更新自动预热状态失败";
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
