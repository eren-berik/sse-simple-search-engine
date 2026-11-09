(() => {
  "use strict";

  const $ = (id) => document.getElementById(id);
  const typeLabels = { video: "Video", text: "Metin" };
  const sortLabels = { popularity: "Popülerlik", relevance: "Alakalılık" };
  const roleLabels = { admin: "Admin", viewer: "Viewer" };
  const fmtNumber = new Intl.NumberFormat("tr-TR", { maximumFractionDigits: 2 });
  const fmtDate = new Intl.DateTimeFormat("tr-TR", { dateStyle: "medium" });
  const fmtDateTime = new Intl.DateTimeFormat("tr-TR", { dateStyle: "short", timeStyle: "short" });

  // ---------------------------------------------------------------- session
  // Tokens live in sessionStorage: they survive reloads but not closing the tab.
  const SESSION_KEY = "session";
  let session = null;

  function loadSession() {
    try { session = JSON.parse(sessionStorage.getItem(SESSION_KEY)); } catch { session = null; }
  }

  function saveSession(data) {
    session = {
      accessToken: data.access_token,
      refreshToken: data.refresh_token,
      user: data.user,
    };
    try { sessionStorage.setItem(SESSION_KEY, JSON.stringify(session)); } catch { /* storage unavailable */ }
  }

  function clearSession() {
    session = null;
    try { sessionStorage.removeItem(SESSION_KEY); } catch { /* storage unavailable */ }
  }

  const isAdmin = () => session?.user?.role === "admin";

  // ---------------------------------------------------------------- api
  // Turkish messages for common error codes; other errors show the API's message.
  const errorMessages = {
    invalid_credentials: "Kullanıcı adı veya şifre hatalı.",
    username_taken: "Bu kullanıcı adı zaten alınmış.",
    invalid_refresh_token: "Oturumunuzun süresi doldu, lütfen tekrar giriş yapın.",
    forbidden: "Bu işlem için yetkiniz yok.",
    rate_limited: "Çok fazla istek gönderildi, lütfen biraz bekleyin.",
    sync_in_progress: "Şu anda başka bir senkronizasyon çalışıyor, birazdan tekrar deneyin.",
    bad_gateway: "Servise şu anda ulaşılamıyor.",
    gateway_timeout: "Servis zamanında yanıt vermedi.",
    auth_unavailable: "Kimlik doğrulama şu anda kullanılamıyor.",
  };

  class ApiError extends Error {
    constructor(status, code, message) {
      super(message);
      this.status = status;
      this.code = code;
    }
  }

  async function request(path, { method = "GET", body, token } = {}) {
    const headers = {};
    if (body !== undefined) headers["Content-Type"] = "application/json";
    if (token) headers.Authorization = `Bearer ${token}`;
    const res = await fetch(path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) });
    const data = res.status === 204 ? null : await res.json().catch(() => null);
    if (!res.ok) {
      const code = data?.error?.code;
      throw new ApiError(res.status, code, errorMessages[code] || data?.error?.message || `İstek başarısız (${res.status})`);
    }
    return { data, headers: res.headers };
  }

  // Concurrent 401s share one refresh, so a rotated refresh token is never used twice.
  let refreshing = null;
  function refreshSession() {
    if (!refreshing) {
      refreshing = request("/api/v1/auth/refresh", { method: "POST", body: { refresh_token: session.refreshToken } })
        .then(({ data }) => saveSession(data.data))
        .finally(() => { refreshing = null; });
    }
    return refreshing;
  }

  // api calls an authenticated endpoint, refreshing the access token once if it expired.
  async function api(path, options = {}) {
    if (!session) throw new ApiError(401, "unauthorized", "Oturum açmanız gerekiyor.");
    try {
      return await request(path, { ...options, token: session.accessToken });
    } catch (err) {
      if (!(err instanceof ApiError) || err.status !== 401) throw err;
      try {
        await refreshSession();
      } catch {
        signOut("Oturumunuzun süresi doldu, lütfen tekrar giriş yapın.");
        throw err;
      }
      return request(path, { ...options, token: session.accessToken });
    }
  }

  // ---------------------------------------------------------------- ui helpers
  function showError(el, message) {
    el.textContent = message || "";
    el.hidden = !message;
  }

  let toastTimer;
  function toast(message) {
    const el = $("toast");
    el.textContent = message;
    el.hidden = false;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => { el.hidden = true; }, 3500);
  }

  function cell(text, className) {
    const td = document.createElement("td");
    if (className) td.className = className;
    td.textContent = text;
    return td;
  }

  function badge(text, className) {
    const span = document.createElement("span");
    span.className = `badge ${className}`;
    span.textContent = text;
    return span;
  }

  function selectTab(container, selected) {
    for (const tab of container.querySelectorAll(".tab")) {
      tab.setAttribute("aria-selected", String(tab === selected));
    }
  }

  // ---------------------------------------------------------------- views
  function showAuth(message) {
    $("app-view").hidden = true;
    $("user-box").hidden = true;
    $("auth-view").hidden = false;
    showError($("auth-error"), message);
  }

  function showApp() {
    $("auth-view").hidden = true;
    $("app-view").hidden = false;
    $("user-box").hidden = false;
    $("user-name").textContent = session.user.username;
    const role = $("user-role");
    role.textContent = roleLabels[session.user.role] || session.user.role;
    role.className = `role ${session.user.role}`;

    for (const el of document.querySelectorAll("[data-admin-only]")) {
      if (el.tagName === "SECTION") continue; // panels are toggled by the tabs
      el.hidden = !isAdmin();
    }
    openPanel("contents-panel");
    state.page = 1;
    loadProviderOptions();
    loadContents();
  }

  function signOut(message) {
    clearSession();
    showAuth(message);
  }

  // ---------------------------------------------------------------- auth forms
  for (const tab of document.querySelectorAll("[data-auth-tab]")) {
    tab.addEventListener("click", () => {
      const isLogin = tab.dataset.authTab === "login";
      selectTab(tab.parentElement, tab);
      $("login-form").hidden = !isLogin;
      $("register-form").hidden = isLogin;
      showError($("auth-error"), "");
    });
  }

  async function submitAuth(form, path, body) {
    const button = form.querySelector("button[type=submit]");
    button.disabled = true;
    try {
      const { data } = await request(path, { method: "POST", body });
      saveSession(data.data);
      form.reset();
      showApp();
    } catch (err) {
      showError($("auth-error"), err.message);
    } finally {
      button.disabled = false;
    }
  }

  $("login-form").addEventListener("submit", (e) => {
    e.preventDefault();
    const f = new FormData(e.target);
    submitAuth(e.target, "/api/v1/auth/login", { username: f.get("username"), password: f.get("password") });
  });

  $("register-form").addEventListener("submit", (e) => {
    e.preventDefault();
    const f = new FormData(e.target);
    submitAuth(e.target, "/api/v1/auth/register", {
      username: f.get("username"), password: f.get("password"), role: f.get("role"),
    });
  });

  $("logout-btn").addEventListener("click", async () => {
    const refreshToken = session?.refreshToken;
    signOut();
    if (refreshToken) {
      request("/api/v1/auth/logout", { method: "POST", body: { refresh_token: refreshToken } }).catch(() => {});
    }
  });

  // ---------------------------------------------------------------- panels
  function openPanel(id) {
    for (const panel of ["contents-panel", "providers-panel"]) {
      $(panel).hidden = panel !== id || (panel === "providers-panel" && !isAdmin());
    }
    const tab = document.querySelector(`[data-panel="${id}"]`);
    if (tab) selectTab(tab.parentElement, tab);
    showError($("error"), "");
    if (id === "providers-panel") loadProviders();
  }

  for (const tab of document.querySelectorAll("[data-panel]")) {
    tab.addEventListener("click", () => openPanel(tab.dataset.panel));
  }

  // ---------------------------------------------------------------- contents
  const state = { page: 1, totalPages: 0 };

  function renderContents(items, meta) {
    const rows = $("rows");
    rows.replaceChildren();
    const showRelevance = meta.sort === "relevance";
    $("relevance-col").hidden = !showRelevance;
    // The API falls back to popularity when relevance is requested without a keyword; say so.
    $("sort-hint").hidden = !($("sort").value === "relevance" && !showRelevance);

    if (items.length === 0) {
      const tr = document.createElement("tr");
      const td = cell("Sonuç bulunamadı.", "empty");
      td.colSpan = 6;
      tr.append(td);
      rows.append(tr);
    }

    for (const item of items) {
      const tr = document.createElement("tr");
      tr.tabIndex = 0;
      const typeCell = document.createElement("td");
      typeCell.append(badge(typeLabels[item.type] || item.type, item.type));

      tr.append(cell(item.title), typeCell, cell(fmtNumber.format(item.score), "num"));
      if (showRelevance) tr.append(cell(fmtNumber.format(item.relevance ?? 0), "num"));
      tr.append(cell(item.provider, "hide-sm"), cell(fmtDate.format(new Date(item.published_at)), "hide-sm"));

      tr.addEventListener("click", () => openDetail(item.id));
      tr.addEventListener("keydown", (e) => { if (e.key === "Enter") openDetail(item.id); });
      rows.append(tr);
    }

    state.totalPages = meta.total_pages;
    $("page-info").textContent = meta.total
      ? `Sayfa ${meta.page} / ${meta.total_pages} · ${meta.total} sonuç · Sıralama: ${sortLabels[meta.sort] || meta.sort}`
      : "";
    $("prev").disabled = meta.page <= 1;
    $("next").disabled = meta.page >= meta.total_pages;
  }

  async function loadContents() {
    const params = new URLSearchParams();
    for (const key of ["q", "type", "provider", "sort", "per_page"]) {
      const value = $(key).value.trim();
      if (value) params.set(key, value);
    }
    params.set("page", state.page);

    try {
      const { data } = await api(`/api/v1/contents?${params}`);
      showError($("error"), "");
      renderContents(data.data, data.meta);
    } catch (err) {
      if (session) showError($("error"), err.message);
    }
  }

  async function loadProviderOptions() {
    try {
      const { data } = await api("/api/v1/providers");
      const select = $("provider");
      const current = select.value;
      select.replaceChildren(new Option("Tümü", ""));
      for (const p of data.data) select.append(new Option(p.name, p.name));
      select.value = current;
    } catch {
      /* the filter stays limited to "all" */
    }
  }

  async function openDetail(id) {
    try {
      const { data } = await api(`/api/v1/contents/${id}`);
      const c = data.data;
      const b = c.score_breakdown;
      $("detail-title").textContent = c.title;
      const entries = [
        ["Tür", typeLabels[c.type] || c.type],
        ["Provider", `${c.provider} (${c.external_id})`],
        ["Etiketler", c.tags.join(", ") || "-"],
        ...Object.entries(c.metrics).map(([k, v]) => [k, fmtNumber.format(v)]),
        ["Temel puan", fmtNumber.format(b.base)],
        ["Tür katsayısı", fmtNumber.format(b.type_coefficient)],
        ["Güncellik puanı", fmtNumber.format(b.recency)],
        ["Etkileşim puanı", fmtNumber.format(b.engagement)],
        ["Final skor", fmtNumber.format(b.final)],
      ];
      const dl = $("detail-body");
      dl.replaceChildren();
      for (const [k, v] of entries) {
        const dt = document.createElement("dt");
        dt.textContent = k;
        const dd = document.createElement("dd");
        dd.textContent = v;
        dl.append(dt, dd);
      }
      $("detail").showModal();
    } catch (err) {
      showError($("error"), err.message);
    }
  }

  let debounce;
  $("q").addEventListener("input", () => {
    clearTimeout(debounce);
    debounce = setTimeout(() => { state.page = 1; loadContents(); }, 300);
  });
  for (const id of ["type", "provider", "sort", "per_page"]) {
    $(id).addEventListener("change", () => { state.page = 1; loadContents(); });
  }
  $("filters").addEventListener("submit", (e) => { e.preventDefault(); state.page = 1; loadContents(); });
  $("prev").addEventListener("click", () => { if (state.page > 1) { state.page--; loadContents(); } });
  $("next").addEventListener("click", () => { if (state.page < state.totalPages) { state.page++; loadContents(); } });

  // ---------------------------------------------------------------- providers (admin)
  async function loadProviders() {
    try {
      const [{ data: providers }, { data: runs }] = await Promise.all([
        api("/api/v1/providers"),
        api("/api/v1/sync/runs"),
      ]);
      const lastRun = Object.fromEntries(runs.data.map((r) => [r.provider, r]));
      renderProviders(providers.data, lastRun);
    } catch (err) {
      showError($("error"), err.message);
    }
  }

  function renderProviders(providers, lastRun) {
    const rows = $("provider-rows");
    rows.replaceChildren();
    for (const p of providers) {
      const tr = document.createElement("tr");
      const status = document.createElement("td");
      status.append(badge(p.enabled ? "Etkin" : "Devre dışı", p.enabled ? "on" : "off"));

      const run = lastRun[p.name];
      const runText = run
        ? `${run.status === "success" ? "✓" : "✗"} ${fmtDateTime.format(new Date(run.finished_at))} · ${run.upserted} kayıt`
        : "Henüz yok";
      const runCell = cell(runText, "hide-sm");
      if (run?.error) runCell.title = run.error;

      const actions = document.createElement("td");
      actions.className = "actions";
      const syncBtn = document.createElement("button");
      syncBtn.type = "button";
      syncBtn.className = "secondary small";
      syncBtn.textContent = "Senkronize et";
      syncBtn.disabled = !p.enabled;
      syncBtn.addEventListener("click", () => runSync(`/api/v1/providers/${p.id}/sync`, syncBtn));
      const toggleBtn = document.createElement("button");
      toggleBtn.type = "button";
      toggleBtn.className = "secondary small";
      toggleBtn.textContent = p.enabled ? "Devre dışı bırak" : "Etkinleştir";
      toggleBtn.addEventListener("click", () => toggleProvider(p, toggleBtn));
      actions.append(syncBtn, toggleBtn);

      tr.append(cell(p.name), cell(p.format.toUpperCase()), cell(p.url, "hide-sm url"), status, runCell, actions);
      rows.append(tr);
    }
  }

  async function runSync(path, button) {
    button.disabled = true;
    try {
      const { data } = await api(path, { method: "POST" });
      const failed = data.data.runs.filter((r) => r.status !== "success");
      toast(failed.length
        ? `Senkronizasyon tamamlandı, ${failed.length} provider başarısız oldu.`
        : `Senkronizasyon tamamlandı (${data.data.runs.length} provider).`);
      await loadProviders();
      loadContents();
    } catch (err) {
      showError($("error"), err.message);
    } finally {
      button.disabled = false;
    }
  }

  async function toggleProvider(p, button) {
    button.disabled = true;
    try {
      await api(`/api/v1/providers/${p.id}`, { method: "PATCH", body: { enabled: !p.enabled } });
      await loadProviders();
    } catch (err) {
      showError($("error"), err.message);
      button.disabled = false;
    }
  }

  $("sync-all-btn").addEventListener("click", (e) => runSync("/api/v1/sync", e.currentTarget));

  $("provider-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    const form = e.target;
    const f = new FormData(form);
    const button = form.querySelector("button[type=submit]");
    button.disabled = true;
    try {
      const { data } = await api("/api/v1/providers", {
        method: "POST",
        body: { name: f.get("name"), url: f.get("url"), format: f.get("format"), enabled: f.get("enabled") === "on" },
      });
      form.reset();
      toast(`"${data.data.name}" eklendi. İçerikleri için senkronize edin.`);
      await loadProviders();
      loadProviderOptions();
    } catch (err) {
      showError($("error"), err.message);
    } finally {
      button.disabled = false;
    }
  });

  // ---------------------------------------------------------------- start
  async function start() {
    loadSession();
    if (!session) {
      showAuth();
      return;
    }
    try {
      // Validates the stored session (refreshing it if needed) and picks up role changes.
      const { data } = await api("/api/v1/auth/me");
      session.user = data.data;
      showApp();
    } catch {
      if (session) signOut();
    }
  }

  start();
})();
