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
  const fmtNumber = new Intl.NumberFormat("tr-TR", { maximumFractionDigits: 2 });
  const fmtDate = new Intl.DateTimeFormat("tr-TR", { dateStyle: "medium" });
  const fmtDateTime = new Intl.DateTimeFormat("tr-TR", { dateStyle: "short", timeStyle: "short" });
  const fmtCompact = new Intl.NumberFormat("tr-TR", { notation: "compact", maximumFractionDigits: 1 });
  // Turkish compact notation writes thousands as "B" (bin), which reads like "billion" in an
  // international product, so counts are written in full below a million ("1,2 Mn" above).
  const fmtCount = (v) => (v >= 1e6 ? fmtCompact : fmtNumber).format(v);
  const fmtPercent = new Intl.NumberFormat("tr-TR", { style: "percent", maximumFractionDigits: 1 });
  const fmtRelevance = new Intl.NumberFormat("tr-TR", { maximumFractionDigits: 4 });

  // Each type shows four metric columns in the same positions, so rows can be compared at a
  // glance. The fourth is the engagement ratio the score formula uses.
  const metricColumns = {
    video: [
      { icon: "eye", label: "Görüntülenme", value: (m) => fmtCount(m.views ?? 0), title: (m) => fmtNumber.format(m.views ?? 0) },
      { icon: "heart", label: "Beğeni", value: (m) => fmtCount(m.likes ?? 0), title: (m) => fmtNumber.format(m.likes ?? 0) },
      { icon: "clock", label: "Süre", value: (m) => formatDuration(m.duration_sec ?? 0) },
      { icon: "trend", label: "Beğeni oranı", engagement: true,
        value: (m) => (m.views ? fmtPercent.format((m.likes ?? 0) / m.views) : "-") },
    ],
    text: [
      { icon: "book", label: "Okuma süresi", value: (m) => `${fmtNumber.format(m.reading_time ?? 0)} dk` },
      { icon: "spark", label: "Reaksiyon", value: (m) => fmtCount(m.reactions ?? 0), title: (m) => fmtNumber.format(m.reactions ?? 0) },
      { icon: "message", label: "Yorum", value: (m) => fmtCount(m.comments ?? 0), title: (m) => fmtNumber.format(m.comments ?? 0) },
      { icon: "trend", label: "Reaksiyon / dk", engagement: true,
        value: (m) => (m.reading_time ? fmtNumber.format((m.reactions ?? 0) / m.reading_time) : "-") },
    ],
  };

  // Lucide icons (ISC licence), inlined so the dashboard stays dependency-free.
  const icons = {
    sync: '<path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/><path d="M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16"/><path d="M8 16H3v5"/>',
    edit: '<path d="M21.174 6.812a1 1 0 0 0-3.986-3.987L3.842 16.174a2 2 0 0 0-.5.83l-1.321 4.352a.5.5 0 0 0 .623.622l4.353-1.32a2 2 0 0 0 .83-.497z"/><path d="m15 5 4 4"/>',
    disable: '<circle cx="12" cy="12" r="10"/><path d="m4.9 4.9 14.2 14.2"/>',
    enable: '<path d="M12 2v10"/><path d="M18.4 6.6a9 9 0 1 1-12.77.04"/>',
    delete: '<path d="M3 6h18"/><path d="M19 6v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6"/><path d="M8 6V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2"/><path d="M10 11v6"/><path d="M14 11v6"/>',
    eye: '<path d="M2.06 12.35a1 1 0 0 1 0-.7 10.75 10.75 0 0 1 19.88 0 1 1 0 0 1 0 .7 10.75 10.75 0 0 1-19.88 0"/><circle cx="12" cy="12" r="3"/>',
    heart: '<path d="M19 14c1.49-1.46 3-3.21 3-5.5A5.5 5.5 0 0 0 16.5 3c-1.76 0-3 .5-4.5 2-1.5-1.5-2.74-2-4.5-2A5.5 5.5 0 0 0 2 8.5c0 2.3 1.5 4.05 3 5.5l7 7Z"/>',
    clock: '<circle cx="12" cy="12" r="10"/><path d="M12 6v6l4 2"/>',
    book: '<path d="M12 7v14"/><path d="M3 18a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1h5a4 4 0 0 1 4 4 4 4 0 0 1 4-4h5a1 1 0 0 1 1 1v13a1 1 0 0 1-1 1h-6a3 3 0 0 0-3 3 3 3 0 0 0-3-3z"/>',
    spark: '<path d="M9.94 15.5A2 2 0 0 0 8.5 14.06l-6.14-1.58a.5.5 0 0 1 0-.96L8.5 9.94A2 2 0 0 0 9.94 8.5l1.58-6.14a.5.5 0 0 1 .96 0L14.06 8.5A2 2 0 0 0 15.5 9.94l6.14 1.58a.5.5 0 0 1 0 .96L15.5 14.06a2 2 0 0 0-1.44 1.44l-1.58 6.14a.5.5 0 0 1-.96 0z"/>',
    message: '<path d="M7.9 20A9 9 0 1 0 4 16.1L2 22Z"/>',
    trend: '<path d="M22 7 13.5 15.5 8.5 10.5 2 17"/><path d="M16 7h6v6"/>',
    video: '<path d="m16 13 5.22 3.48a.5.5 0 0 0 .78-.42V7.87a.5.5 0 0 0-.75-.43L16 10.5"/><rect x="2" y="6" width="14" height="12" rx="2"/>',
    text: '<path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z"/><path d="M14 2v4a2 2 0 0 0 2 2h4"/><path d="M10 9H8"/><path d="M16 13H8"/><path d="M16 17H8"/>',
  };

  function svgIcon(name, size = 16) {
    return `<svg viewBox="0 0 24 24" width="${size}" height="${size}" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${icons[name]}</svg>`;
  }

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
    upstream_unavailable: "Servis geçici olarak devre dışı, kısa süre sonra tekrar deneyin.",
    format_not_detected: "Format algılanamadı: URL'den gelen veri desteklenen JSON veya XML şemasına uymuyor.",
    provider_unreachable: "Provider URL'sine ulaşılamadı; adresi kontrol edin.",
    conflict: "Bu isimde bir provider zaten var.",
  };

  // Rate limits and open circuits say when to come back (Retry-After, in seconds).
  const retryMessages = {
    rate_limited: (s) => `Çok fazla istek gönderildi, ${s} saniye sonra tekrar deneyin.`,
    upstream_unavailable: (s) => `Servis geçici olarak devre dışı, ${s} saniye sonra tekrar deneyin.`,
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
      const retryAfter = Number.parseInt(res.headers.get("Retry-After"), 10);
      const message = (retryAfter > 0 && retryMessages[code]?.(retryAfter))
        || errorMessages[code] || data?.error?.message || `İstek başarısız (${res.status})`;
      throw new ApiError(res.status, code, message);
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
    $("app-tabs").hidden = true;
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
    $("user-avatar").textContent = session.user.username.charAt(0);
    // Viewers only have the contents panel, so they need no tabs.
    $("app-tabs").hidden = !isAdmin();
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
    setType("");
    setTag("");
    $("published_to").min = "";
    $("published_from").max = "";
    $("provider").replaceChildren(new Option("Tümü", ""));
    state.page = 1;
    state.totalPages = 0;
    state.ranked = null;
    $("results").replaceChildren();
    $("pager").replaceChildren();
    $("summary").textContent = "";
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

  // ---------------------------------------------------------------- theme
  // The page starts with the stored theme, or the system's when none was chosen (see the script
  // in index.html). A choice made with the button is stored; until then the page follows
  // system changes.
  const THEME_KEY = "theme";
  const systemDark = matchMedia("(prefers-color-scheme: dark)");

  function applyTheme(theme) {
    document.documentElement.dataset.theme = theme;
    const label = theme === "dark" ? "Aydınlık temaya geç" : "Karanlık temaya geç";
    $("theme-toggle").setAttribute("aria-label", label);
    $("theme-toggle").title = label;
  }

  function storedTheme() {
    try { return localStorage.getItem(THEME_KEY); } catch { return null; }
  }

  applyTheme(document.documentElement.dataset.theme === "dark" ? "dark" : "light");

  $("theme-toggle").addEventListener("click", () => {
    const theme = document.documentElement.dataset.theme === "dark" ? "light" : "dark";
    applyTheme(theme);
    try { localStorage.setItem(THEME_KEY, theme); } catch { /* storage unavailable */ }
  });

  systemDark.addEventListener("change", (e) => {
    if (!storedTheme()) applyTheme(e.matches ? "dark" : "light");
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
  // ranked holds the last relevance-sorted search, so the detail dialog can explain the match.
  const state = { page: 1, totalPages: 0, ranked: null };

  function el(tag, className, text) {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined) node.textContent = text;
    return node;
  }

  function metricCell(column, metrics, extraClass = "") {
    const box = el("div", `metric ${column.engagement ? "engagement" : ""} ${extraClass}`.trim());
    const value = el("span", "metric-value");
    value.innerHTML = svgIcon(column.icon, 14);
    value.append(column.value(metrics));
    if (column.title) box.title = `${column.label}: ${column.title(metrics)}`;
    box.append(value, el("span", "metric-label", column.label));
    return box;
  }

  function typeIcon(type, size = 20) {
    const box = el("div", `type-icon ${type}`);
    box.innerHTML = svgIcon(type === "video" ? "video" : "text", size);
    box.title = typeLabels[type] || type;
    return box;
  }

  // Match levels for the relevance view; the best result of a search is always 100%.
  const matchLevel = (pct) =>
    pct >= 90 ? { label: "Güçlü eşleşme", tone: "strong" }
      : pct >= 60 ? { label: "Orta eşleşme", tone: "medium" }
        : { label: "Zayıf eşleşme", tone: "weak" };

  // The card's right side shows what the list is sorted by: the score for popularity, a match
  // gauge for relevance (with the score kept as a small secondary figure). The two numbers never
  // share a style, so they are not mistaken for each other.
  function rankingBox(item, ranked) {
    const box = el("div", "score");
    if (!ranked) {
      box.title = "Popülerlik skoru: etkileşim, güncellik ve içerik türünden hesaplanır.";
      box.append(el("span", "score-value", fmtNumber.format(item.score)), el("span", "score-label", "Skor"));
      return box;
    }
    const pct = item.match ?? 0;
    const level = matchLevel(pct);
    box.classList.add("match", level.tone);
    box.title = "Eşleşme, bu aramanın en iyi sonucuna göre yüzdedir: kelimenin başlıkta ve etiketlerde geçmesine ve başlığın aramaya yakınlığına göre hesaplanır.";
    const gauge = el("div", "gauge");
    const r = 15;
    const c = 2 * Math.PI * r;
    gauge.innerHTML = `<svg viewBox="0 0 36 36" width="40" height="40" aria-hidden="true">
      <circle cx="18" cy="18" r="${r}" class="gauge-track"/>
      <circle cx="18" cy="18" r="${r}" class="gauge-fill" stroke-dasharray="${(c * pct) / 100} ${c}"/></svg>`;
    const text = el("div", "match-text");
    text.append(el("span", "match-value", `%${pct}`), el("span", "match-label", level.label));
    const head = el("div", "match-head");
    head.append(gauge, text);
    box.append(head, el("span", "score-chip", `Skor ${fmtNumber.format(item.score)}`));
    return box;
  }

  function renderItem(item, rank, ranked) {
    const li = el("li", "item");
    li.tabIndex = 0;
    li.setAttribute("aria-label", `${item.title}, skor ${fmtNumber.format(item.score)}`);

    const main = el("div", "item-main");
    const meta = el("div", "item-meta");
    meta.append(el("span", "", typeLabels[item.type] || item.type), el("span", "dot", item.provider),
      el("span", "dot", fmtDate.format(new Date(item.published_at))));
    for (const tag of item.tags.slice(0, 3)) meta.append(tagChip(tag));
    main.append(el("h3", "item-title", item.title), meta);

    const metrics = el("div", "metrics");
    for (const column of metricColumns[item.type] || []) metrics.append(metricCell(column, item.metrics));

    const score = rankingBox(item, ranked);

    li.append(el("span", "rank", `#${rank}`), typeIcon(item.type), main, metrics, score);
    li.addEventListener("click", () => openDetail(item.id));
    li.addEventListener("keydown", (e) => { if (e.key === "Enter") openDetail(item.id); });
    return li;
  }

  // tagChip filters the list by its tag; clicking the active tag clears the filter.
  function tagChip(tag, { closeDialog = false } = {}) {
    const chip = el("button", `chip ${tag === $("tag").value ? "active" : ""}`.trim(), tag);
    chip.type = "button";
    chip.title = `"${tag}" etiketine göre filtrele`;
    chip.addEventListener("click", (e) => {
      e.preventDefault();
      e.stopPropagation(); // the card behind the chip opens the detail dialog
      if (closeDialog) $("detail").close();
      applyFilter(() => setTag(tag === $("tag").value ? "" : tag));
    });
    chip.addEventListener("keydown", (e) => e.stopPropagation());
    return chip;
  }

  function renderSkeleton(count) {
    const list = $("results");
    list.replaceChildren();
    for (let i = 0; i < Math.min(count, 6); i++) {
      const li = el("li", "item skeleton");
      li.append(el("span"), el("div", "bone"), el("div", "bone"), el("div", "bone"), el("div", "bone"));
      list.append(li);
    }
  }

  function renderContents(items, meta) {
    const list = $("results");
    list.replaceChildren();
    const ranked = meta.sort === "relevance";
    state.ranked = ranked ? { q: $("q").value.trim(), best: meta.best_relevance } : null;
    // The API falls back to popularity when relevance is requested without a keyword; say so.
    $("sort-hint").hidden = !($("sort").value === "relevance" && !ranked);

    const summary = $("summary");
    summary.replaceChildren();
    if (meta.total) {
      summary.append(el("strong", "", fmtNumber.format(meta.total)), ` sonuç · ${sortLabels[meta.sort] || meta.sort} sıralaması`);
      if (ranked) summary.append(el("span", "summary-note", " · eşleşme, en iyi sonuca göre yüzde"));
    }

    if (items.length === 0) {
      const empty = el("li", "empty-state");
      empty.append(el("strong", "", "Sonuç bulunamadı"), "Farklı bir anahtar kelime deneyin ya da filtreleri gevşetin.");
      list.append(empty);
    }

    const offset = (meta.page - 1) * meta.per_page;
    items.forEach((item, i) => list.append(renderItem(item, offset + i + 1, ranked)));

    state.totalPages = meta.total_pages;
    renderPager(meta.page, meta.total_pages);
  }

  // renderPager shows the first, last and nearby pages, with gaps in between.
  function renderPager(page, total) {
    const pager = $("pager");
    pager.replaceChildren();
    if (total <= 1) return;
    const goTo = (p) => { state.page = p; loadContents(); };
    const button = (label, p, { current = false, disabled = false, aria } = {}) => {
      const b = el("button", "secondary", label);
      b.type = "button";
      b.disabled = disabled;
      if (current) b.setAttribute("aria-current", "page");
      if (aria) b.setAttribute("aria-label", aria);
      b.addEventListener("click", () => goTo(p));
      return b;
    };
    pager.append(button("←", page - 1, { disabled: page <= 1, aria: "Önceki sayfa" }));
    let last = 0;
    for (let p = 1; p <= total; p++) {
      if (p !== 1 && p !== total && Math.abs(p - page) > 1) continue;
      if (p - last > 1) pager.append(el("span", "gap", "…"));
      pager.append(button(String(p), p, { current: p === page }));
      last = p;
    }
    pager.append(button("→", page + 1, { disabled: page >= total, aria: "Sonraki sayfa" }));
  }

  async function loadContents() {
    const epoch = authEpoch;
    const params = new URLSearchParams();
    for (const key of ["q", "type", "provider", "tag", "published_from", "published_to", "sort", "per_page"]) {
      const value = $(key).value.trim();
      if (value) params.set(key, value);
    }
    params.set("page", state.page);

    renderSkeleton(Number($("per_page").value));
    try {
      const { data } = await api(`/api/v1/contents?${params}`);
      if (isStale(epoch)) return;
      showError($("error"), "");
      renderContents(data.data, data.meta);
    } catch (err) {
      if (isStale(epoch)) return;
      $("results").replaceChildren();
      showError($("error"), err.message);
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

  // Score components in the order of the formula: base × coefficient + recency + engagement.
  const scoreColors = { weighted: "#6366f1", recency: "#06b6d4", engagement: "#10b981" };
  const metricNames = {
    views: "Görüntülenme", likes: "Beğeni", reading_time: "Okuma süresi", reactions: "Reaksiyon", comments: "Yorum",
  };
  const n = (v) => fmtNumber.format(v);

  // calcRow shows label, the calculation and its result; a string value is shown as is.
  function calcRow(label, expr, value, className = "") {
    const row = el("div", `calc-row ${className}`.trim());
    const shown = typeof value === "string" ? value : n(value);
    row.append(el("span", "calc-label", label), el("span", "calc-expr", expr), el("span", "calc-val", shown));
    return row;
  }

  function explainBlock(color, title, value) {
    const block = el("section", "explain-block");
    const head = el("div", "explain-head");
    const swatch = el("span", "swatch");
    swatch.style.background = color;
    head.append(swatch, el("h4", "", title), el("strong", "", n(value)));
    block.append(head);
    return block;
  }

  const fieldNames = { title: "başlık", tags: "etiket" };
  const relevanceColor = "#8b5cf6";

  // renderRelevanceExplanation shows how the item matched the search it was opened from: where
  // each word was found, how that became the relevance and how the relevance became a percentage
  // of the best result. Like the score, the steps come from the API that ranks the results.
  function renderRelevanceExplanation(r) {
    const card = $("detail-relevance");
    card.hidden = !r;
    if (!r) return;
    const body = $("detail-relevance-body");
    // Relevance values are small; four decimals keep the arithmetic readable (1,2 ÷ 1,6449 = 0,7295).
    const r4 = (v) => fmtRelevance.format(v);
    body.replaceChildren();

    const best = state.ranked?.best;
    const pct = best ? Math.round((r.relevance / best) * 100) : null;
    const level = pct === null ? null : matchLevel(pct);
    card.style.setProperty("--tone", level?.tone === "strong" ? "var(--good)" : level?.tone === "weak" ? "#d97706" : "var(--accent)");
    $("detail-match").textContent = pct === null ? r4(r.relevance) : `%${pct}`;

    if (!r.matches) {
      body.append(el("p", "calc-note", `Bu içerik "${r.query}" aramasıyla eşleşmiyor.`));
      return;
    }

    // Where the searched words are.
    const where = explainBlock(relevanceColor, `"${r.query}" nerede geçiyor?`, r.relevance);
    where.querySelector("strong").remove();
    for (const t of r.terms) {
      const row = el("div", "term-row");
      row.append(el("span", "term-word", t.term),
        el("span", `where ${t.in_title ? "hit" : ""}`.trim(), t.in_title ? `Başlıkta ${t.in_title} kez` : "Başlıkta yok"),
        el("span", `where ${t.in_tags ? "hit" : ""}`.trim(), t.in_tags ? `Etikette ${t.in_tags} kez` : "Etikette yok"));
      where.append(row);
    }
    if (r.kind === "partial") {
      where.append(el("p", "calc-note", "Kelimenin tamamı geçmiyor; yalnızca bir parçası başlıkta bulundu."));
    }

    // How that became the relevance.
    const points = explainBlock(relevanceColor, "Alakalılık puanı", r.relevance);
    points.querySelector("strong").textContent = r4(r.relevance);
    if (r.kind === "full_text" && r.rank_steps.length) {
      for (const st of r.rank_steps) {
        points.append(calcRow(`${st.position}. eşleşme · ${fieldNames[st.field]}`, `${n(st.weight)} ÷ ${st.position}²`, r4(st.value)));
      }
      const sum = r.rank_steps.reduce((a, st) => a + st.value, 0);
      points.append(calcRow("Toplam ÷ π²/6", `${r4(sum)} ÷ ${r4(r.rank_normalizer)}`, r4(r.rank), "sum"));
      points.append(el("p", "calc-note",
        `Başlıktaki kelime ${n(r.title_weight)}, etiketteki ${n(r.tag_weight)} ağırlık alır. Aynı kelimenin her ek geçişi azalarak katkı yapar (1, ¼, ⅑…).`));
    } else if (r.kind === "full_text") {
      points.append(calcRow("Tam kelime eşleşmesi (ts_rank)", `başlık ${n(r.title_weight)} · etiket ${n(r.tag_weight)}`, r4(r.rank), "sum"));
      points.append(el("p", "calc-note", "Birden fazla kelimede PostgreSQL kelimelerin birbirine yakınlığını da hesaba katar, bu yüzden adım adım gösterilmez."));
    } else {
      points.append(calcRow("Kelime benzerliği", "başlıktaki en yakın kısım", r4(r.word_similarity)));
      points.append(calcRow("Kısmi eşleşme çarpanı", `${r4(r.word_similarity)} × ${n(r.partial_factor)}`, r4(r.relevance), "sum"));
      points.append(el("p", "calc-note", "Kısmi eşleşmeler her zaman tam kelime eşleşmelerinin altında kalacak şekilde ölçeklenir."));
    }
    body.append(where, points);

    // How the relevance became a percentage.
    if (pct !== null) {
      const share = explainBlock(relevanceColor, "Eşleşme yüzdesi", r.relevance);
      share.querySelector("strong").textContent = `%${pct}`;
      share.append(calcRow("Bu içerik ÷ aramanın en iyi sonucu", `${r4(r.relevance)} ÷ ${r4(best)}`, `%${pct}`, "sum"));
      share.append(el("p", "calc-note", `${level.label}. En iyi sonuç her zaman %100'dür.`));
      body.append(share);
    }
  }

  // renderScoreExplanation shows how every component was derived. The steps come from the
  // API, which builds them with the same code that computes the score.
  function renderScoreExplanation(c) {
    const b = c.score_breakdown;
    const x = b.explanation;
    const weighted = b.base * b.type_coefficient;
    const parts = [
      ["weighted", weighted, "Temel puan × tür katsayısı"],
      ["recency", b.recency, "Güncellik"],
      ["engagement", b.engagement, "Etkileşim"],
    ];

    const bar = $("detail-bar");
    bar.replaceChildren();
    for (const [key, value, label] of parts) {
      if (b.final <= 0 || value <= 0) continue;
      const seg = el("span");
      seg.style.width = `${(value / b.final) * 100}%`;
      seg.style.background = scoreColors[key];
      seg.title = `${label}: ${n(value)}`;
      bar.append(seg);
    }

    // Base: each metric divided by its constant, summed, then weighted by the type coefficient.
    const base = explainBlock(scoreColors.weighted, "Temel puan × tür katsayısı", weighted);
    for (const t of x.base_terms) {
      const name = metricNames[t.metric] || t.metric;
      base.append(calcRow(name, t.divisor === 1 ? n(t.value) : `${n(t.value)} ÷ ${n(t.divisor)}`, t.result));
    }
    base.append(calcRow("Temel puan", x.base_terms.map((t) => n(t.result)).join(" + "), b.base, "sum"));
    base.append(calcRow(`Tür katsayısı (${typeLabels[c.type] || c.type})`, `${n(b.base)} × ${n(b.type_coefficient)}`, weighted, "sum"));

    // Recency: which age tier the content falls into.
    const r = x.recency;
    const recency = explainBlock(scoreColors.recency, "Güncellik", b.recency);
    const tiers = el("div", "tiers");
    r.tiers.forEach((t, i) => {
      const tier = el("span", `tier ${i === r.matched_tier ? "matched" : ""}`.trim(), `≤ ${n(t.max_age_days)} gün: +${n(t.points)}`);
      tiers.append(tier);
    });
    tiers.append(el("span", `tier ${r.matched_tier < 0 ? "matched" : ""}`.trim(), "Daha eski: 0"));
    recency.append(tiers);
    const ageText = r.age_days === 0 ? "bugün" : `${n(r.age_days)} gün önce`;
    recency.append(calcRow("İçerik yaşı", `${fmtDate.format(new Date(c.published_at))} · ${ageText}`, b.recency, "sum"));

    // Engagement: a ratio of two metrics times a constant.
    const e = x.engagement;
    const engagement = explainBlock(scoreColors.engagement, "Etkileşim", b.engagement);
    const num = metricNames[e.numerator] || e.numerator;
    const den = metricNames[e.denominator] || e.denominator;
    if (e.denominator_value > 0) {
      const ratio = e.numerator_value / e.denominator_value;
      engagement.append(calcRow(`${num} ÷ ${den}`, `${n(e.numerator_value)} ÷ ${n(e.denominator_value)}`, ratio));
      engagement.append(calcRow("Çarpan", `${n(ratio)} × ${n(e.multiplier)}`, e.result, "sum"));
    } else {
      engagement.append(calcRow(`${num} ÷ ${den}`, `${n(e.numerator_value)} ÷ 0`, 0, "sum"));
      engagement.append(el("p", "calc-note", `${den} 0 olduğu için etkileşim puanı 0 sayılır.`));
    }

    $("detail-breakdown").replaceChildren(base, recency, engagement);

    const total = $("detail-total");
    total.replaceChildren(
      el("span", "calc-expr", `${n(weighted)} + ${n(b.recency)} + ${n(b.engagement)}`),
      el("strong", "", `= ${n(b.final)}`),
    );
  }

  async function openDetail(id) {
    const epoch = authEpoch;
    try {
      // From a relevance-sorted list, also ask how this item matched the search.
      const query = state.ranked ? `?q=${encodeURIComponent(state.ranked.q)}` : "";
      const { data } = await api(`/api/v1/contents/${id}${query}`);
      if (isStale(epoch)) return;
      const c = data.data;
      const b = c.score_breakdown;

      const kicker = $("detail-kicker");
      kicker.replaceChildren(badge(typeLabels[c.type] || c.type, c.type));
      $("detail-title").textContent = c.title;
      $("detail-meta").textContent = `${c.provider} · #${c.external_id} · ${fmtDate.format(new Date(c.published_at))}`;

      const tiles = $("detail-metrics");
      tiles.replaceChildren();
      for (const column of metricColumns[c.type] || []) {
        const exact = column.title ? { ...column, value: column.title } : column;
        tiles.append(metricCell(exact, c.metrics));
      }

      $("detail-final").textContent = fmtNumber.format(b.final);
      renderScoreExplanation(c);
      renderRelevanceExplanation(c.relevance_breakdown);

      const tags = $("detail-tags");
      tags.replaceChildren(...c.tags.map((t) => tagChip(t, { closeDialog: true })));
      $("detail").showModal();
    } catch (err) {
      if (!isStale(epoch)) showError($("error"), err.message);
    }
  }

  // applyFilter changes a filter and reloads from the first page.
  function applyFilter(change) {
    change();
    state.page = 1;
    loadContents();
  }

  // The tag filter is set from the tag chips and shown as a removable pill.
  function setTag(tag) {
    $("tag").value = tag;
    renderActiveFilters();
  }

  function renderActiveFilters() {
    const box = $("active-filters");
    box.replaceChildren();
    const tag = $("tag").value;
    if (!tag) return;
    const pill = el("span", "filter-pill", `Etiket: ${tag}`);
    const remove = el("button");
    remove.type = "button";
    remove.setAttribute("aria-label", "Etiket filtresini kaldır");
    remove.innerHTML = '<svg viewBox="0 0 24 24" width="12" height="12" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" aria-hidden="true"><path d="M18 6 6 18M6 6l12 12"/></svg>';
    remove.addEventListener("click", () => applyFilter(() => setTag("")));
    pill.append(remove);
    box.append(pill);
  }

  // Each date bounds the other, so the browser's picker cannot produce a reversed range.
  for (const id of ["published_from", "published_to"]) {
    $(id).addEventListener("change", () => {
      $("published_to").min = $("published_from").value;
      $("published_from").max = $("published_to").value;
      applyFilter(() => {});
    });
  }

  // The type filter is a segmented control backed by the hidden #type input.
  function setType(value) {
    $("type").value = value;
    for (const b of document.querySelectorAll("#type-seg [data-type]")) {
      b.setAttribute("aria-checked", String(b.dataset.type === value));
    }
  }
  for (const b of document.querySelectorAll("#type-seg [data-type]")) {
    b.addEventListener("click", () => {
      if ($("type").value === b.dataset.type) return;
      setType(b.dataset.type);
      state.page = 1;
      loadContents();
    });
  }

  let debounce;
  $("q").addEventListener("input", () => {
    clearTimeout(debounce);
    debounce = setTimeout(() => { state.page = 1; loadContents(); }, 300);
  });
  for (const id of ["provider", "sort", "per_page"]) {
    $(id).addEventListener("change", () => { state.page = 1; loadContents(); });
  }
  $("filters").addEventListener("submit", (e) => { e.preventDefault(); state.page = 1; loadContents(); });

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

  // iconButton builds a compact action button; the label doubles as tooltip and accessible name.
  function iconButton(icon, label, extraClass = "") {
    const button = document.createElement("button");
    button.type = "button";
    button.className = `secondary icon-btn ${extraClass}`.trim();
    button.title = label;
    button.setAttribute("aria-label", label);
    button.innerHTML = svgIcon(icon);
    return button;
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
        ? `${run.status === "success" ? "✓" : "✗"} ${fmtDateTime.format(new Date(run.finished_at))} · ${run.fetched} kayıt`
          + (run.status !== "success" ? "" : run.upserted ? ` · ${run.upserted} değişiklik` : " · değişiklik yok")
          + (run.removed ? ` · ${run.removed} kaldırıldı` : "")
        : "Henüz yok";
      const runCell = cell(runText, "hide-sm");
      if (run?.error) runCell.title = run.error;

      const actionsCell = document.createElement("td");
      const actions = document.createElement("div");
      actions.className = "actions";
      actionsCell.append(actions);
      const syncBtn = iconButton("sync", "Senkronize et");
      syncBtn.disabled = !p.enabled;
      syncBtn.addEventListener("click", () => runSync(`/api/v1/providers/${p.id}/sync`, syncBtn));
      const editBtn = iconButton("edit", "Düzenle");
      editBtn.addEventListener("click", () => openProviderEdit(p));
      const toggleBtn = p.enabled ? iconButton("disable", "Devre dışı bırak") : iconButton("enable", "Etkinleştir");
      toggleBtn.addEventListener("click", () => toggleProvider(p, toggleBtn));
      const deleteBtn = iconButton("delete", "Sil", "danger");
      deleteBtn.addEventListener("click", () => deleteProvider(p, deleteBtn));
      actions.append(syncBtn, editBtn, toggleBtn, deleteBtn);

      tr.append(cell(p.name), cell(p.format.toUpperCase(), "hide-sm"), cell(p.url, "hide-sm url"), status, runCell, actionsCell);
      rows.append(tr);
    }
  }

  async function runSync(path, button) {
    const epoch = authEpoch;
    button.disabled = true;
    button.classList.add("busy");
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
      button.classList.remove("busy");
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
