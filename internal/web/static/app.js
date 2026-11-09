(() => {
  "use strict";

  const $ = (id) => document.getElementById(id);
  const state = { page: 1, totalPages: 0 };
  const typeLabels = { video: "Video", text: "Metin" };
  const sortLabels = { popularity: "Popülerlik", relevance: "Alakalılık" };
  const fmtNumber = new Intl.NumberFormat("tr-TR", { maximumFractionDigits: 2 });
  const fmtDate = new Intl.DateTimeFormat("tr-TR", { dateStyle: "medium" });
  const fmtDateTime = new Intl.DateTimeFormat("tr-TR", { dateStyle: "short", timeStyle: "short" });

  async function api(path, options) {
    const res = await fetch(path, options);
    const body = await res.json().catch(() => null);
    if (!res.ok) {
      throw new Error(body?.error?.message || `İstek başarısız (${res.status})`);
    }
    return { body, headers: res.headers };
  }

  function showError(message) {
    const el = $("error");
    el.textContent = message || "";
    el.hidden = !message;
  }

  function cell(text, className) {
    const td = document.createElement("td");
    if (className) td.className = className;
    td.textContent = text;
    return td;
  }

  function render(items, meta) {
    const rows = $("rows");
    rows.replaceChildren();
    const showRelevance = meta.sort === "relevance";
    $("relevance-col").hidden = !showRelevance;
    // The API falls back to popularity when relevance is requested without a keyword; say so instead of silently ignoring the choice.
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
      const badge = document.createElement("span");
      badge.className = `badge ${item.type}`;
      badge.textContent = typeLabels[item.type] || item.type;
      typeCell.append(badge);

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

  async function load() {
    const params = new URLSearchParams();
    for (const key of ["q", "type", "sort", "per_page"]) {
      const value = $(key).value.trim();
      if (value) params.set(key, value);
    }
    params.set("page", state.page);

    try {
      const { body } = await api(`/api/v1/contents?${params}`);
      showError("");
      render(body.data, body.meta);
    } catch (err) {
      showError(err.message);
    }
  }

  async function openDetail(id) {
    try {
      const { body } = await api(`/api/v1/contents/${id}`);
      const c = body.data;
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
      showError(err.message);
    }
  }

  async function loadSyncStatus() {
    try {
      const { body } = await api("/api/v1/sync/runs");
      if (body.data.length === 0) {
        $("sync-status").textContent = "Henüz senkronizasyon yapılmadı.";
        return;
      }
      $("sync-status").textContent = body.data
        .map((r) => `${r.provider}: ${r.status === "success" ? "✓" : "✗"} ${fmtDateTime.format(new Date(r.finished_at))}`)
        .join(" · ");
    } catch {
      $("sync-status").textContent = "Senkronizasyon durumu alınamadı.";
    }
  }

  async function triggerSync() {
    let key = sessionStorage.getItem("adminKey");
    if (!key) {
      key = prompt("Admin API anahtarı (X-API-Key):");
      if (!key) return;
    }
    const btn = $("sync-btn");
    btn.disabled = true;
    try {
      await api("/api/v1/admin/sync", { method: "POST", headers: { "X-API-Key": key } });
      try { sessionStorage.setItem("adminKey", key); } catch { /* storage unavailable */ }
      showError("");
      await Promise.all([loadSyncStatus(), load()]);
    } catch (err) {
      try { sessionStorage.removeItem("adminKey"); } catch { /* storage unavailable */ }
      showError(err.message);
    } finally {
      btn.disabled = false;
    }
  }

  let debounce;
  $("q").addEventListener("input", () => {
    clearTimeout(debounce);
    debounce = setTimeout(() => { state.page = 1; load(); }, 300);
  });
  for (const id of ["type", "sort", "per_page"]) {
    $(id).addEventListener("change", () => { state.page = 1; load(); });
  }
  $("filters").addEventListener("submit", (e) => { e.preventDefault(); state.page = 1; load(); });
  $("prev").addEventListener("click", () => { if (state.page > 1) { state.page--; load(); } });
  $("next").addEventListener("click", () => { if (state.page < state.totalPages) { state.page++; load(); } });
  $("sync-btn").addEventListener("click", triggerSync);

  load();
  loadSyncStatus();
})();
