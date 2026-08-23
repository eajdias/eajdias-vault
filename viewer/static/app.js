"use strict";

const grid = document.getElementById("grid");
const sentinel = document.getElementById("sentinel");
const emptyBox = document.getElementById("empty");
const countEl = document.getElementById("count");
const sortSel = document.getElementById("sort");
const probeNote = document.getElementById("probenote");
const lb = document.getElementById("lightbox");
const lbContent = document.getElementById("lb-content");
const lbCounter = document.getElementById("lb-counter");
const lbFilesize = document.getElementById("lb-filesize");

// ---- abas + ordenação ------------------------------------------------------

const SORTS = {
  all: [
    ["recent", "Recentes"],
    ["oldest", "Antigos"],
    ["size_desc", "Maiores arquivos"],
    ["size_asc", "Menores arquivos"],
    ["name", "Nome A-Z"],
  ],
  image: [
    ["size_asc", "Menores primeiro (carrega rápido)"],
    ["size_desc", "Maiores primeiro"],
    ["recent", "Recentes"],
    ["oldest", "Antigos"],
    ["name", "Nome A-Z"],
  ],
  gif: [
    ["size_asc", "Menores primeiro (carrega rápido)"],
    ["size_desc", "Maiores primeiro"],
    ["recent", "Recentes"],
    ["oldest", "Antigos"],
    ["name", "Nome A-Z"],
  ],
  video: [
    ["duration_asc", "Duração curta → longa"],
    ["duration_desc", "Duração longa → curta"],
    ["size_asc", "Menores arquivos"],
    ["size_desc", "Maiores arquivos"],
    ["recent", "Recentes"],
    ["name", "Nome A-Z"],
  ],
};
const DEFAULT_SORT = { all: "recent", image: "size_asc", gif: "size_asc", video: "duration_asc" };

function sortFor(tab) {
  const saved = localStorage.getItem(`sv-sort-${tab}`);
  return SORTS[tab].some(([k]) => k === saved) ? saved : DEFAULT_SORT[tab];
}

function renderSortOptions() {
  sortSel.replaceChildren();
  for (const [k, label] of SORTS[state.tab]) {
    const o = document.createElement("option");
    o.value = k;
    o.textContent = label;
    if (k === state.sort) o.selected = true;
    sortSel.appendChild(o);
  }
}

function syncUrl() {
  const p = new URLSearchParams();
  if (state.tab !== "all") p.set("tab", state.tab);
  if (state.sort !== DEFAULT_SORT[state.tab]) p.set("sort", state.sort);
  if (state.q) p.set("q", state.q);
  history.replaceState(null, "", p.size ? `?${p}` : location.pathname);
}

function setTab(tab) {
  if (!SORTS[tab]) tab = "all";
  state.tab = tab;
  state.sort = sortFor(tab);
  document.querySelectorAll(".tab").forEach((b) => { b.classList.toggle("active", b.dataset.cat === tab); });
  renderSortOptions();
  syncUrl();
  reset();
}

// ---- estado / fetch --------------------------------------------------------

const state = { offset: 0, total: null, q: "", tab: "all", sort: "recent", loading: false, items: [] };
let abortCtl = null;

async function api(url, signal) {
  const r = await fetch(url, { signal });
  if (r.status === 401) { location.href = "/login"; throw new Error("unauth"); }
  if (!r.ok) throw new Error(r.status);
  return r.json();
}

function fmtSize(n) {
  if (n >= 1 << 30) return (n / (1 << 30)).toFixed(1) + " GB";
  if (n >= 1 << 20) return (n / (1 << 20)).toFixed(1) + " MB";
  if (n >= 1024) return Math.round(n / 1024) + " KB";
  return n + " B";
}
function fmtDur(s) {
  s = Math.round(s);
  const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), sec = s % 60;
  const pad = (x) => String(x).padStart(2, "0");
  return h ? `${h}:${pad(m)}:${pad(sec)}` : `${m}:${pad(sec)}`;
}

function chip(text, cls) {
  const c = document.createElement("span");
  c.className = `chip ${cls}`;
  c.textContent = text;
  return c;
}

// ---- preview no hover (só desktop): vídeo toca trecho mudo, GIF anima ----
// leve por construção: 1 preview ativo por vez, atraso de 350ms e vídeos
// via streaming Range (não baixa o arquivo inteiro); GIF limitado a 48MB
const canHover = matchMedia("(hover: hover) and (pointer: fine)").matches;
let activePreview = null;

function stopPreview() {
  if (!activePreview) return;
  const { div, kind } = activePreview;
  activePreview = null;
  const vid = div.querySelector("video.preview");
  if (vid) { vid.pause(); vid.removeAttribute("src"); vid.load(); vid.remove(); }
  const im = div.querySelector("img");
  if (kind === "gif" && im && div.dataset.thumbSrc) im.src = div.dataset.thumbSrc;
}

function startPreview(div, it) {
  stopPreview();
  if (!div.isConnected) return;
  const src = `/media/${encodeURIComponent(it.name)}`;
  if (it.kind === "gif") {
    if (it.size > 48 * 1024 * 1024) return;
    const im = div.querySelector("img");
    if (!im) return;
    div.dataset.thumbSrc = im.src;
    activePreview = { div, kind: "gif" };
    im.src = src; // gif real substitui o thumb estático e anima
  } else if (it.kind === "video") {
    const v = document.createElement("video");
    v.className = "preview";
    v.muted = true; v.loop = true; v.playsInline = true; v.preload = "metadata";
    v.src = src;
    v.addEventListener("loadeddata", () => {
      v.classList.add("on");
      v.play().catch(() => {});
    }, { once: true });
    activePreview = { div, kind: "video" };
    div.appendChild(v);
  }
}

function card(it) {
  const div = document.createElement("div");
  div.className = "card";
  const im = new Image();
  im.loading = "lazy";
  im.decoding = "async";
  im.addEventListener("load", () => {
    im.classList.add("on");
    if (im.naturalWidth && im.naturalHeight)
      div.style.aspectRatio = im.naturalWidth + "/" + im.naturalHeight;
  });
  im.addEventListener("error", () => div.classList.add("err"));
  im.src = `/thumb/${encodeURIComponent(it.name)}?w=400`;
  im.alt = it.name;
  div.appendChild(im);
  if (it.kind === "video") div.appendChild(chip("▶", "play"));
  if (it.duration > 0) div.appendChild(chip(fmtDur(it.duration), "dur"));
  div.appendChild(chip(fmtSize(it.size), "size"));
  // loadMore faz push antes de chamar card(), então o índice deste item é length-1
  const idx = state.items.length - 1;
  div.addEventListener("click", () => openLightbox(idx));
  if (canHover && (it.kind === "video" || it.kind === "gif")) {
    let hov = null;
    div.addEventListener("mouseenter", () => {
      hov = setTimeout(() => { hov = null; startPreview(div, it); }, 350);
    });
    div.addEventListener("mouseleave", () => {
      if (hov) { clearTimeout(hov); hov = null; }
      stopPreview();
    });
  }
  grid.appendChild(div);
  return div;
}

async function loadMore() {
  if (state.loading) return;
  if (state.total !== null && state.offset >= state.total) return;
  state.loading = true;
  try {
    abortCtl?.abort();
    abortCtl = new AbortController();
    const p = new URLSearchParams({
      offset: state.offset, limit: 60,
      category: state.tab, sort: state.sort,
    });
    if (state.q) p.set("q", state.q);
    const d = await api(`/api/items?${p}`, abortCtl.signal);
    for (const it of d.items) {
      state.items.push(it);
      card(it);
    }
    state.offset += d.items.length;
    state.total = d.total;
    countEl.textContent = `${d.total} mídias`;
    emptyBox.hidden = d.total !== 0;
  } catch (e) {
    if (e.name !== "AbortError") { /* silencioso */ }
  }
  state.loading = false;
}

function reset() {
  abortCtl?.abort();
  state.offset = 0; state.total = null; state.items.length = 0;
  grid.replaceChildren();
  emptyBox.hidden = true;
  loadMore();
}

// ---- lightbox ----
let lbIdx = -1;
let lbSuppressTap = false; // um hold de 2x não deve alternar a barra
const lbSpeedEl = document.getElementById("lb-speed");
let holdTimer = null;

function stopBoost(v) {
  if (holdTimer) { clearTimeout(holdTimer); holdTimer = null; }
  if (v && v.playbackRate !== 1) v.playbackRate = 1;
  lbSpeedEl.hidden = true;
}

// segurar pressionado sobre o vídeo = 2x; soltar = volta ao normal
function wireVideoGestures(v) {
  v.addEventListener("pointerdown", () => {
    holdTimer = setTimeout(() => {
      holdTimer = null;
      if (!v.isConnected || v.ended) return;
      v.playbackRate = 2;
      lbSpeedEl.hidden = false;
      lbSuppressTap = true;
    }, 450);
  });
  for (const ev of ["pointerup", "pointercancel", "pointerleave"]) {
    v.addEventListener(ev, () => stopBoost(v));
  }
}

function openLightbox(i) {
  stopPreview();
  lbIdx = i;
  renderLightbox();
  lb.hidden = false;
  lockScroll();
}
function closeLightbox() {
  stopBoost(lbContent.querySelector("video") || undefined);
  lb.hidden = true;
  lbContent.innerHTML = "";
  unlockScroll();
}
function renderLightbox() {
  const it = state.items[lbIdx];
  if (!it) return closeLightbox();
  stopBoost();
  lb.classList.remove("chrome-hidden");
  lbCounter.textContent = `${lbIdx + 1} / ${state.total ?? state.items.length}`;
  lbFilesize.textContent = fmtSize(it.size || 0);
  lbContent.innerHTML = "";
  const src = `/media/${encodeURIComponent(it.name)}`;
  let el;
  if (it.kind === "video") {
    el = document.createElement("video");
    el.controls = true; el.autoplay = true; el.playsInline = true; el.src = src;
    el.controlsList = "nodownload";
    el.disablePictureInPicture = true;
    el.setAttribute("disableremoteplayback", "");
    el.draggable = false;
    wireVideoGestures(el);
  } else if (it.kind === "image") {
    el = document.createElement("img");
    el.src = src; el.alt = it.name;
    el.draggable = false;
    el.setAttribute("selectstart", "return false");
  } else {
    el = document.createElement("p");
    el.className = "unsupported";
    el.textContent = `${it.name} — ${it.mime} (sem exibição no navegador)`;
  }
  lbContent.appendChild(el);
}
document.getElementById("lb-close").addEventListener("click", closeLightbox);
lb.addEventListener("click", (e) => { if (e.target === lb) closeLightbox(); });
document.getElementById("lb-prev").addEventListener("click", () => { if (lbIdx > 0) { lbIdx--; renderLightbox(); } });
document.getElementById("lb-next").addEventListener("click", () => { if (lbIdx < state.items.length - 1) { lbIdx++; renderLightbox(); } });
document.addEventListener("keydown", (e) => {
  if (lb.hidden) return;
  if (e.key === "Escape") closeLightbox();
  if (e.key === "ArrowLeft" && lbIdx > 0) { lbIdx--; renderLightbox(); }
  if (e.key === "ArrowRight" && lbIdx < state.items.length - 1) { lbIdx++; renderLightbox(); }
});

// swipe no lightbox (mobile): horizontal E vertical navegam entre mídias;
// touch-action:none + preventDefault garantem que o fundo não role junto
function lbSwipe(dx, dy) {
  const adx = Math.abs(dx), ady = Math.abs(dy);
  const MIN = 40; // dedo real curva: limiar um pouco mais permissivo
  if (adx > MIN && ady < adx * 0.9) {
    if (dx < 0 && lbIdx < state.items.length - 1) { lbIdx++; renderLightbox(); }
    else if (dx > 0 && lbIdx > 0) { lbIdx--; renderLightbox(); }
    return;
  }
  if (ady > MIN && adx < ady * 0.9) {
    if (dy < 0 && lbIdx < state.items.length - 1) { lbIdx++; renderLightbox(); }
    else if (dy > 0 && lbIdx > 0) { lbIdx--; renderLightbox(); }
  }
}
let tX = 0, tY = 0;
lb.addEventListener("touchstart", (e) => {
  const t = e.changedTouches[0];
  tX = t.clientX; tY = t.clientY;
}, { passive: true });
lb.addEventListener("touchmove", (e) => e.preventDefault(), { passive: false });
lb.addEventListener("touchend", (e) => {
  const t = e.changedTouches[0];
  lbSwipe(t.clientX - tX, t.clientY - tY);
}, { passive: true });

// tap na mídia alterna barra superior/navs (modo imersivo; cliques nos
// controles nativos do vídeo não propagam para o elemento)
lbContent.addEventListener("click", (e) => {
  if (lbSuppressTap) { lbSuppressTap = false; return; }
  const t = e.target;
  if (t !== lbContent && !(t instanceof HTMLImageElement) && !(t instanceof HTMLVideoElement)) return;
  lb.classList.toggle("chrome-hidden");
});
// long-press não deve abrir menu de contexto (salvar imagem/vídeo)
lbContent.addEventListener("contextmenu", (e) => {
  if (e.target instanceof HTMLVideoElement || e.target instanceof HTMLImageElement) e.preventDefault();
});

// trava o scroll do fundo com overlays abertos (html E body: alguns
// browsers móveis rolam pelo documentElement mesmo com body overflow hidden)
function lockScroll() {
  document.body.style.overflow = "hidden";
  document.documentElement.style.overflow = "hidden";
}
function unlockScroll() {
  if (lb.hidden && dlpanel.hidden) {
    document.body.style.overflow = "";
    document.documentElement.style.overflow = "";
  }
}

// ---- busca com debounce ----
let deb;
document.getElementById("q").addEventListener("input", (e) => {
  clearTimeout(deb);
  deb = setTimeout(() => {
    state.q = e.target.value.trim();
    syncUrl();
    reset();
  }, 300);
});

// ---- controles de aba/ordem ----
document.querySelectorAll(".tab").forEach((b) => { b.addEventListener("click", () => setTab(b.dataset.cat)); });
sortSel.addEventListener("change", () => {
  state.sort = sortSel.value;
  localStorage.setItem(`sv-sort-${state.tab}`, state.sort);
  syncUrl();
  reset();
});

// ---- badges das abas + progresso da análise de duração ----
async function refreshStats() {
  try {
    const st = await api("/api/stats");
    for (const [cat, n] of Object.entries(st.counts)) {
      const b = document.querySelector(`.tab[data-cat="${cat}"] .tcount`);
      if (b) b.textContent = n;
    }
    const { left, total } = st.probe;
    probeNote.hidden = !(left > 0 && total > 0 && left < total);
    if (!probeNote.hidden) probeNote.textContent = `⏳ analisando vídeos… ${total - left}/${total}`;
    if (left === 0 && probeNote.dataset.wasActive) {
      delete probeNote.dataset.wasActive;
      reset(); // durações recém-chegadas podem mudar a ordem atual
    } else if (!probeNote.hidden) {
      probeNote.dataset.wasActive = "1";
    }
  } catch { /* silencioso */ }
}

// ---- logout ----
document.getElementById("logout").addEventListener("click", async () => {
  await fetch("/logout", { method: "POST" });
  location.href = "/login";
});

// ---- painel do downloader ----
const dlpanel = document.getElementById("dlpanel");
const dlBody = document.getElementById("dl-body");
const dlHint = document.getElementById("dl-hint");
const btnStart = document.getElementById("dl-start");
const btnSync = document.getElementById("dl-sync");
const btnStop = document.getElementById("dl-stop");
let dlTimer = null;
let dlActionBusy = false;

function dlRow(label, value, cls) {
  return `<div class="dlrow"><span>${label}</span><b${cls ? ` class="${cls}"` : ""}>${value}</b></div>`;
}

function renderDl(st) {
  const phaseLabel =
    st.active
      ? st.phase === "iniciando" ? "iniciando…" : "baixando"
      : st.phase === "concluido" ? "parado (última execução concluída)"
      : st.phase === "interrompido" ? "parado (interrompido)"
      : "parado";
  const stateCls = st.active ? (st.stuck ? "warn" : "ok") : "";
  let html = "";
  html += dlRow("Estado", st.stuck ? "⚠ sem progresso" : phaseLabel, stateCls);
  if (st.active) html += dlRow("Iniciado", st.source === "ui" ? "pela UI" : "pelo terminal");
  if (st.sync_mode) html += dlRow("Modo", st.sync_mode === "incremental" ? "sync rápido" : "completo");
  if (st.active) {
    html += dlRow("Taxa", `${fmtSize(st.rate_bps)}/s`);
    html += dlRow("Sessão", `${st.session_files} arq · ${fmtSize(st.session_bytes)}`);
  }
  if (st.vault_files) html += dlRow("Vault", `${st.vault_files} arq · ${fmtSize(st.vault_bytes)}`);
  if (st.age_s !== undefined && st.updated_at) html += dlRow("Última atividade", st.age_s < 90 ? `há ${st.age_s}s` : new Date(st.updated_at * 1000).toLocaleTimeString());
  dlBody.innerHTML = html;

  btnStart.disabled = btnSync.disabled = st.active || !st.docker_ok || dlActionBusy;
  btnStop.disabled = !st.active || !st.docker_ok || dlActionBusy;
  if (!st.docker_ok) {
    dlHint.textContent = "controle indisponível: socket do Docker não montado no viewer";
  } else if (st.stuck) {
    dlHint.textContent = "nenhum progresso há mais de 3 min — considere Parar e iniciar de novo";
  } else if (st.active) {
    dlHint.textContent = "";
  } else {
    dlHint.textContent = "";
  }
}

async function refreshDl() {
  try {
    const r = await fetch("/api/download/status");
    if (r.ok) renderDl(await r.json());
  } catch { /* silencioso */ }
}

async function dlAction(url) {
  if (dlActionBusy) return;
  dlActionBusy = true;
  try {
    await fetch(url, { method: "POST" });
    refreshDl();
  } catch { /* silencioso */ } finally {
    dlActionBusy = false;
  }
}

btnStart.addEventListener("click", () => dlAction("/api/download/start?mode=full"));
btnSync.addEventListener("click", () => dlAction("/api/download/start?mode=incremental"));
btnStop.addEventListener("click", () => dlAction("/api/download/stop"));

function openDl() {
  dlpanel.hidden = false;
  lockScroll();
  refreshDl();
  dlTimer = setInterval(refreshDl, 3000);
}
function closeDl() {
  dlpanel.hidden = true;
  unlockScroll();
  clearInterval(dlTimer);
  dlTimer = null;
}
document.getElementById("gear").addEventListener("click", () => (dlpanel.hidden ? openDl() : closeDl()));
document.getElementById("dl-close").addEventListener("click", closeDl);
dlpanel.addEventListener("click", (e) => { if (e.target === dlpanel) closeDl(); });

// ---- scroll infinito ----
new IntersectionObserver((entries) => {
  if (entries.some((e) => e.isIntersecting)) loadMore();
}, { rootMargin: "600px" }).observe(sentinel);

// ---- boot: restaura aba/sort/busca da URL ----
{
  const p = new URLSearchParams(location.search);
  const tab = SORTS[p.get("tab")] ? p.get("tab") : "all";
  state.tab = tab;
  state.sort = sortFor(tab);
  if (SORTS[tab].some(([k]) => k === p.get("sort"))) {
    state.sort = p.get("sort");
    localStorage.setItem(`sv-sort-${tab}`, state.sort);
  }
  const q0 = p.get("q") || "";
  if (q0) { document.getElementById("q").value = q0; state.q = q0; }
  document.querySelectorAll(".tab").forEach((b) => { b.classList.toggle("active", b.dataset.cat === tab); });
  renderSortOptions();
}
reset();
refreshStats();
setInterval(refreshStats, 15000);

// altura do header vira var CSS (as abas sticky se posicionam por ela;
// no mobile o header quebra em 2 linhas e a altura muda)
function syncHeadVar() {
  document.documentElement.style.setProperty(
    "--headH", document.querySelector("header").offsetHeight + "px");
}
syncHeadVar();
addEventListener("resize", syncHeadVar);
