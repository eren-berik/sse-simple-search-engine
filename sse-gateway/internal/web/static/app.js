(() => {
  "use strict";

  const $ = (id) => document.getElementById(id);
  const typeLabels = { video: "Video", text: "Metin" };
  const sortLabels = { popularity: "Popülerlik", relevance: "Alakalılık" };
  const roleLabels = { admin: "Admin", viewer: "Viewer" };
  const formatDuration = (sec) => {
    const h = Math.floor(sec / 3600), m = Math.floor((sec % 3600) / 60), s = String(sec % 60).padStart(2, "0");
    return h ? `${h}:${String(m).padStart(2, "0")}:${s}` : `${m}:${s}`;
  };
  // Labels and value formats for the metrics shown in the content detail dialog.
  const metricFields = {
    views: { label: "Görüntülemeler", format: (v) => fmtNumber.format(v) },
    likes: { label: "Beğeniler", format: (v) => fmtNumber.format(v) },
    duration_sec: { label: "Süre", format: formatDuration },
    reading_time: { label: "Okuma Süresi", format: (v) => `${fmtNumber.format(v)} dk` },
    reactions: { label: "Reaksiyonlar", format: (v) => fmtNumber.format(v) },
    comments: { label: "Yorumlar", format: (v) => fmtNumber.format(v) },
  };
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
    format_not_detected: "Format algılanamadı: URL'den gelen veri desteklenen JSON veya XML şemasına uymuyor.",
    provider_unreachable: "Provider URL'sine ulaşılamadı; adresi kontrol edin.",
    conflict: "Bu isimde bir provider zaten var.",
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
    // Always come back to the login form, e.g. after registering and then logging out.
    switchAuthTab("login");
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

  // authEpoch changes whenever the signed-in user changes; responses of requests started under
  // an older epoch are dropped so one user's data never renders in another user's session.
  let authEpoch = 0;
  const isStale = (epoch) => epoch !== authEpoch;

  function signOut(message) {
    authEpoch++;
    clearSession();
    resetAppView();
    showAuth(message);
  }

  // resetAppView clears everything the previous user typed or loaded.
  function resetAppView() {
    clearTimeout(debounce);
    $("filters").reset();
    $("provider").replaceChildren(new Option("Tümü", ""));
    state.page = 1;
    state.totalPages = 0;
    $("rows").replaceChildren();
    $("page-info").textContent = "";
    $("relevance-col").hidden = true;
    $("sort-hint").hidden = true;
    $("provider-rows").replaceChildren();
    $("provider-form").reset();
    if ($("detail").open) $("detail").close();
    if ($("provider-edit").open) $("provider-edit").close();
    $("toast").hidden = true;
    showError($("error"), "");
  }

  // ---------------------------------------------------------------- auth forms
  function switchAuthTab(name) {
    const tab = document.querySelector(`[data-auth-tab="${name}"]`);
    selectTab(tab.parentElement, tab);
    $("login-form").hidden = name !== "login";
    $("register-form").hidden = name !== "register";
    showError($("auth-error"), "");
  }

  for (const tab of document.querySelectorAll("[data-auth-tab]")) {
    tab.addEventListener("click", () => switchAuthTab(tab.dataset.authTab));
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
    const epoch = authEpoch;
    const params = new URLSearchParams();
    for (const key of ["q", "type", "provider", "sort", "per_page"]) {
      const value = $(key).value.trim();
      if (value) params.set(key, value);
    }
    params.set("page", state.page);

    try {
      const { data } = await api(`/api/v1/contents?${params}`);
      if (isStale(epoch)) return;
      showError($("error"), "");
      renderContents(data.data, data.meta);
    } catch (err) {
      if (!isStale(epoch)) showError($("error"), err.message);
    }
  }

  async function loadProviderOptions() {
    const epoch = authEpoch;
    try {
      const { data } = await api("/api/v1/providers");
      if (isStale(epoch)) return;
      const select = $("provider");
      const current = select.value;
      select.replaceChildren(new Option("Tümü", ""));
      // Disabled providers' contents are hidden, so they are not offered as a filter.
      for (const p of data.data.filter((p) => p.enabled)) select.append(new Option(p.name, p.name));
      select.value = [...select.options].some((o) => o.value === current) ? current : "";
    } catch {
      /* the filter stays limited to "all" */
    }
  }

  async function openDetail(id) {
    const epoch = authEpoch;
    try {
      const { data } = await api(`/api/v1/contents/${id}`);
      if (isStale(epoch)) return;
      const c = data.data;
      const b = c.score_breakdown;
      $("detail-title").textContent = c.title;
      const entries = [
        ["Tür", typeLabels[c.type] || c.type],
        ["Provider", `${c.provider} (${c.external_id})`],
        ["Etiketler", c.tags.join(", ") || "-"],
        ...Object.entries(c.metrics).map(([k, v]) => {
          const field = metricFields[k];
          return field ? [field.label, field.format(v)] : [k, fmtNumber.format(v)];
        }),
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
      if (!isStale(epoch)) showError($("error"), err.message);
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
    const epoch = authEpoch;
    try {
      const [{ data: providers }, { data: runs }] = await Promise.all([
        api("/api/v1/providers"),
        api("/api/v1/sync/runs"),
      ]);
      if (isStale(epoch)) return;
      const lastRun = Object.fromEntries(runs.data.map((r) => [r.provider, r]));
      renderProviders(providers.data, lastRun);
    } catch (err) {
      if (!isStale(epoch)) showError($("error"), err.message);
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
      const editBtn = document.createElement("button");
      editBtn.type = "button";
      editBtn.className = "secondary small";
      editBtn.textContent = "Düzenle";
      editBtn.addEventListener("click", () => openProviderEdit(p));
      const toggleBtn = document.createElement("button");
      toggleBtn.type = "button";
      toggleBtn.className = "secondary small";
      toggleBtn.textContent = p.enabled ? "Devre dışı bırak" : "Etkinleştir";
      toggleBtn.addEventListener("click", () => toggleProvider(p, toggleBtn));
      const deleteBtn = document.createElement("button");
      deleteBtn.type = "button";
      deleteBtn.className = "secondary small danger";
      deleteBtn.textContent = "Sil";
      deleteBtn.addEventListener("click", () => deleteProvider(p, deleteBtn));
      actions.append(syncBtn, editBtn, toggleBtn, deleteBtn);

      tr.append(cell(p.name), cell(p.format.toUpperCase()), cell(p.url, "hide-sm url"), status, runCell, actions);
      rows.append(tr);
    }
  }

  async function runSync(path, button) {
    const epoch = authEpoch;
    button.disabled = true;
    try {
      const { data } = await api(path, { method: "POST" });
      if (isStale(epoch)) return;
      const failed = data.data.runs.filter((r) => r.status !== "success");
      toast(failed.length
        ? `Senkronizasyon tamamlandı, ${failed.length} provider başarısız oldu.`
        : `Senkronizasyon tamamlandı (${data.data.runs.length} provider).`);
      await loadProviders();
      loadContents();
    } catch (err) {
      if (!isStale(epoch)) showError($("error"), err.message);
    } finally {
      button.disabled = false;
    }
  }

  async function toggleProvider(p, button) {
    const epoch = authEpoch;
    button.disabled = true;
    try {
      await api(`/api/v1/providers/${p.id}`, { method: "PATCH", body: { enabled: !p.enabled } });
      if (isStale(epoch)) return;
      toast(p.enabled
        ? `"${p.name}" devre dışı bırakıldı; içerikleri aramada gösterilmeyecek.`
        : `"${p.name}" etkinleştirildi.`);
      await loadProviders();
      await loadProviderOptions();
      loadContents();
    } catch (err) {
      if (isStale(epoch)) return;
      showError($("error"), err.message);
      button.disabled = false;
    }
  }

  async function deleteProvider(p, button) {
    if (!confirm(`"${p.name}" ve bu provider'dan gelen tüm içerikler kalıcı olarak silinecek. Emin misiniz?`)) return;
    const epoch = authEpoch;
    button.disabled = true;
    try {
      await api(`/api/v1/providers/${p.id}`, { method: "DELETE" });
      if (isStale(epoch)) return;
      toast(`"${p.name}" silindi.`);
      await loadProviders();
      await loadProviderOptions();
      loadContents();
    } catch (err) {
      if (isStale(epoch)) return;
      showError($("error"), err.message);
      button.disabled = false;
    }
  }

  // ---------------------------------------------------------------- provider edit dialog
  let editing = null;

  function openProviderEdit(p) {
    editing = p;
    const form = $("provider-edit-form");
    form.elements.name.value = p.name;
    form.elements.url.value = p.url;
    form.elements.format.value = "auto";
    showError($("provider-edit-error"), "");
    $("provider-edit").showModal();
  }

  $("provider-edit").querySelector("[data-close]").addEventListener("click", () => $("provider-edit").close());

  $("provider-edit-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    const form = e.target;
    const p = editing;
    const name = form.elements.name.value.trim();
    const url = form.elements.url.value.trim();
    const format = form.elements.format.value;

    // Send only what changed. With "auto", a changed URL lets the server detect the format.
    const body = {};
    if (name !== p.name) body.name = name;
    if (url !== p.url) body.url = url;
    if (format !== "auto" && format !== p.format) body.format = format;
    if (Object.keys(body).length === 0) {
      $("provider-edit").close();
      return;
    }

    const epoch = authEpoch;
    const button = form.querySelector("button[type=submit]");
    button.disabled = true;
    try {
      const { data } = await api(`/api/v1/providers/${p.id}`, { method: "PATCH", body });
      if (isStale(epoch)) return;
      $("provider-edit").close();
      toast(`"${data.data.name}" güncellendi (format: ${data.data.format.toUpperCase()}).`);
      await loadProviders();
      await loadProviderOptions();
      loadContents();
    } catch (err) {
      if (!isStale(epoch)) showError($("provider-edit-error"), err.message);
    } finally {
      button.disabled = false;
    }
  });

  $("sync-all-btn").addEventListener("click", (e) => runSync("/api/v1/sync", e.currentTarget));

  $("provider-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    const epoch = authEpoch;
    const form = e.target;
    const f = new FormData(form);
    const button = form.querySelector("button[type=submit]");
    button.disabled = true;
    try {
      const { data } = await api("/api/v1/providers", {
        method: "POST",
        body: { name: f.get("name"), url: f.get("url"), format: f.get("format"), enabled: f.get("enabled") === "on" },
      });
      if (isStale(epoch)) return;
      form.reset();
      toast(`"${data.data.name}" eklendi (format: ${data.data.format.toUpperCase()}). İçerikleri için senkronize edin.`);
      await loadProviders();
      loadProviderOptions();
    } catch (err) {
      if (!isStale(epoch)) showError($("error"), err.message);
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
