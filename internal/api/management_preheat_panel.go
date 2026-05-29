package api

import "bytes"

var managementPreheatMarker = []byte("window.__cliproxyCodexPreheat")

const managementPreheatScript = `<script>
      (function () {
        if (window.__cliproxyCodexPreheat) return;
        window.__cliproxyCodexPreheat = true;

        var preheatEndpoint = "/v0/management/auth-files/preheat";
        var authFilesEndpoint = "/v0/management/auth-files";
        var originalFetch = window.fetch ? window.fetch.bind(window) : null;
        var state = { files: [], loading: false, message: "", messageType: "muted", authHeaders: {} };
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

        function parseAuthFiles(data) {
          var files = Array.isArray(data) ? data : data && Array.isArray(data.files) ? data.files : [];
          state.files = files.filter(function (file) {
            var provider = String((file && (file.provider || file.type)) || "").trim().toLowerCase();
            return provider === "codex" && authIndexOf(file);
          });
          scheduleRender();
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

        function ensureStyle() {
          if (document.getElementById("codex-preheat-style")) return;
          var style = document.createElement("style");
          style.id = "codex-preheat-style";
          style.textContent = "#codex-preheat-panel{margin:12px 0 16px;padding:12px 14px;border:1px solid var(--border-color,#e3e1db);border-radius:12px;background:var(--floating-surface,#fffdf9);box-shadow:var(--shadow,0 1px 2px #00000014);display:flex;align-items:center;gap:10px;flex-wrap:wrap}#codex-preheat-panel button{height:34px;border:1px solid var(--primary-color,#8b8680);border-radius:8px;background:var(--primary-color,#8b8680);color:var(--primary-contrast,#fff);padding:0 12px;cursor:pointer}#codex-preheat-panel button:disabled{cursor:not-allowed;opacity:.55}#codex-preheat-panel .codex-preheat-message{font-size:12px;color:var(--text-secondary,#6d6760)}#codex-preheat-panel .codex-preheat-message.success{color:var(--success-color,#10b981)}#codex-preheat-panel .codex-preheat-message.error{color:var(--error-color,#c65746)}";
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
            return;
          }
          ensureStyle();
          var host = findHost();
          if (!host) return;
          if (!panel) {
            panel = document.createElement("div");
            panel.id = "codex-preheat-panel";
          }

          suppressObserver = true;
          panel.innerHTML = '<button type="button"' + (state.loading ? " disabled" : "") + ">" + (state.loading ? "预热中..." : "预热选中账号") + '</button>' + (state.message ? '<span class="codex-preheat-message ' + escapeHtml(state.messageType) + '">' + escapeHtml(state.message) + '</span>' : "");
          panel.querySelector("button").addEventListener("click", preheatSelected);

          if (!panel.parentNode) {
            host.insertBefore(panel, host.firstChild);
          }
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

        function rowTextMatchesFile(row, file) {
          var text = String((row && row.textContent) || "");
          return [labelOf(file), file && file.name, file && file.email, file && file.account, file && file.id, authIndexOf(file)].some(function (value) {
            value = String(value || "").trim();
            return value && text.indexOf(value) !== -1;
          });
        }

        function selectedCodexAuthFiles() {
          var selected = [];
          var checkboxes = Array.prototype.slice.call(document.querySelectorAll("input[type='checkbox']:checked"));
          checkboxes.forEach(function (checkbox) {
            if (checkbox.closest("#codex-preheat-panel")) return;
            if (!isSelectionCheckbox(checkbox)) return;
            var file = fileForCheckbox(checkbox);
            if (file && !selected.some(function (item) { return authIndexOf(item) === authIndexOf(file); })) selected.push(file);
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
            var file = state.files.find(function (candidate) { return rowTextMatchesFile(node, candidate); });
            if (file) return file;
            node = node.parentElement;
          }
          return null;
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

        function preheatSelected() {
          if (state.loading || !originalFetch) return;
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
          Promise.all(selected.map(preheatAuthFile)).then(function () {
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
