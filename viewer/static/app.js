"use strict";

const grid = document.getElementById("grid");
const sentinel = document.getElementById("sentinel");
const emptyBox = document.getElementById("empty");
const countEl = document.getElementById("count");
const sortSel = document.getElementById("sort");
const randBtn = document.getElementById("randbtn");
const probeNote = document.getElementById("probenote");
const lb = document.getElementById("lightbox");
const lbContent = document.getElementById("lb-content");
const lbCounter = document.getElementById("lb-counter");
const lbFilesize = document.getElementById("lb-filesize");
const lbFavBtn = document.getElementById("lb-fav");
const tagInput = document.getElementById("taginput");
const chipsBox = document.getElementById("chips");
const suggestBox = document.getElementById("suggest");
const tagbarEl = document.getElementById("tagbar");

// ---- abas + ordenação ------------------------------------------------------

const SORTS = {
  all: [
    ["recent", "Recentes"],
    ["oldest", "Antigos"],
    ["size_desc", "Maiores arquivos"],
    ["size_asc", "Menores arquivos"],
    ["name", "Nome A-Z"],
  ],
  fav: [
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
const DEFAULT_SORT = { all: "recent", fav: "recent", image: "size_asc", gif: "size_asc", video: "duration_asc" };

function sortFor(tab) {
  const saved = localStorage.getItem(`sv-sort-${tab}`);
  return SORTS[tab].some(([k]) => k === saved) ? saved : DEFAULT_SORT[tab];
}

// ---- modo aleatório (apresentação padrão; toggle desliga) -------------------
// seed determinístico => paginação estável entre páginas/scroll infinito.

function renderSortOptions() {
  sortSel.replaceChildren();
  for (const [k, label] of SORTS[state.tab]) {
    const o = document.createElement("option");
    o.value = k;
    o.textContent = label;
    if (k === state.sort) o.selected = true;
    sortSel.appendChild(o);
  }
  sortSel.disabled = state.random;
}

function applyRandomUI() {
  randBtn.classList.toggle("active", state.random);
  randBtn.setAttribute("aria-pressed", String(state.random));
  sortSel.disabled = state.random;
}

function setRandom(on) {
  state.random = on;
  localStorage.setItem("sv-random", on ? "1" : "0");
  state.seed = null; // novo sorteio
  applyRandomUI();
  syncUrl();
  reset();
}

function syncUrl() {
  const p = new URLSearchParams();
  if (state.tab !== "all") p.set("tab", state.tab);
  if (state.tags.length) p.set("tags", state.tags.join(","));
  if (state.random) {
    p.set("rand", "1");
    if (state.seed) p.set("seed", state.seed);
  } else if (state.sort !== DEFAULT_SORT[state.tab]) {
    p.set("sort", state.sort);
  }
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

const state = {
  offset: 0, total: null, tab: "all", sort: "recent", loading: false, items: [],
  tags: [],
  random: localStorage.getItem("sv-random") !== "0", // ON por padrão
  seed: null,
};
// rede celular: thumbs menores e páginas curtas (economia de dados no 4G)
const net = navigator.connection || {};
const thumbW = net.saveData || String(net.effectiveType || "").includes("2g") ? 320 : (devicePixelRatio > 2 ? 800 : 400);
const pageLimit = net.saveData ? 30 : 60;
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

// ---- filtro por tags (pesquisa = filtro AND) ----------------------------------
// Normalização espelha o servidor (normTag): minúsculo, espaços→"_".
function normTagUI(s) {
  return s.trim().toLowerCase().replace(/\s+/g, "_");
}

function renderChips() {
  chipsBox.replaceChildren();
  for (const t of state.tags) {
    const c = document.createElement("span");
    c.className = "tchip";
    c.textContent = t;
    const x = document.createElement("button");
    x.type = "button";
    x.textContent = "×";
    x.setAttribute("aria-label", `Remover ${t}`);
    x.addEventListener("click", () => removeTag(t));
    c.appendChild(x);
    chipsBox.appendChild(c);
  }
}

function addTag(t) {
  t = normTagUI(t);
  if (!t || state.tags.includes(t)) return;
  state.tags.push(t);
  renderChips();
  syncUrl();
  reset();
}

function removeTag(t) {
  state.tags = state.tags.filter((x) => x !== t);
  renderChips();
  syncUrl();
  reset();
}

let sugTimer = null;
tagInput.addEventListener("input", () => {
  clearTimeout(sugTimer);
  const v = tagInput.value.trim();
  if (!v) { suggestBox.hidden = true; return; }
  sugTimer = setTimeout(async () => {
    try {
      const d = await api(`/api/tags?prefix=${encodeURIComponent(v)}&limit=8`);
      suggestBox.replaceChildren();
      if (!d.tags.length) { suggestBox.hidden = true; return; }
      for (const { tag, count } of d.tags) {
        const li = document.createElement("li");
        const b = document.createElement("button");
        b.type = "button";
        b.dataset.tag = tag;
        b.textContent = `${tag} (${count})`;
        b.addEventListener("click", () => {
          addTag(tag);
          tagInput.value = "";
          suggestBox.hidden = true;
          tagInput.focus();
        });
        li.appendChild(b);
        suggestBox.appendChild(li);
      }
      suggestBox.hidden = false;
    } catch { suggestBox.hidden = true; }
  }, 150);
});
tagInput.addEventListener("keydown", (e) => {
  if (e.key === "Enter") {
    e.preventDefault();
    const first = suggestBox.querySelector("li button");
    const v = tagInput.value.trim();
    if (!v && first) addTag(first.dataset.tag);
    else if (v) addTag(v);
    tagInput.value = "";
    suggestBox.hidden = true;
  } else if (e.key === "Escape") {
    suggestBox.hidden = true;
    tagInput.blur();
  }
});
document.addEventListener("click", (e) => {
  if (!tagbarEl.contains(e.target)) suggestBox.hidden = true;
});

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
  im.src = `/thumb/${encodeURIComponent(it.name)}?w=${thumbW}`;
  im.alt = it.name;
  div.appendChild(im);
  if (it.kind === "video") div.appendChild(chip("▶", "play"));
  if (it.duration > 0) div.appendChild(chip(fmtDur(it.duration), "dur"));
  div.appendChild(chip(fmtSize(it.size), "size"));
  const sb = document.createElement("button");
  sb.type = "button";
  sb.className = "starbtn" + (it.fav ? " active" : "");
  sb.title = "Favorito";
  sb.setAttribute("aria-label", "Favoritar");
  sb.innerHTML = STAR_SVG;
  // loadMore faz push antes de chamar card(), então o índice deste item é length-1
  const idx = state.items.length - 1;
  sb.addEventListener("click", (e) => { e.stopPropagation(); toggleFav(it, idx, sb); });
  div.appendChild(sb);
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
  thumbUnload.observe(div);
  return div;
}

// ---- favoritos --------------------------------------------------------------

const STAR_SVG = `<svg aria-hidden="true" viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2"></polygon></svg>`;

async function toggleFav(it, idx, btn) {
  try {
    const r = await fetch(`/api/fav/${encodeURIComponent(it.name)}`, { method: "POST" });
    if (!r.ok) return;
    const d = await r.json();
    it.fav = d.fav;
    if (btn) btn.classList.toggle("active", d.fav);
    // estrela do lightbox acompanha quando é o mesmo item
    if (lbIdx === idx) lbFavBtn.classList.toggle("active", d.fav);
    refreshStats();
    // na aba Favoritos, remover a estrela tira o item da lista
    if (state.tab === "fav" && !d.fav) reset();
  } catch { /* silencioso */ }
}

lbFavBtn.addEventListener("click", (e) => {
  e.stopPropagation();
  const it = state.items[lbIdx];
  if (!it) return;
  const cardBtn = grid.children[lbIdx]?.querySelector(".starbtn");
  toggleFav(it, lbIdx, cardBtn);
});

// ---- descarte de thumbs fora da tela ----------------------------------------
// Com 3000+ mídias o DOM acumula muitos <img>; soltar o src de cards distantes
// libera os bitmaps da RAM do celular. O aspect-ratio já foi fixado no load,
// então não há layout shift ao esvaziar/restaurar.
const thumbUnload = new IntersectionObserver((entries) => {
  for (const e of entries) {
    const im = e.target.querySelector("img");
    if (!im || e.target.classList.contains("err")) continue;
    if (e.isIntersecting) {
      if (im.dataset.full) { im.src = im.dataset.full; delete im.dataset.full; }
    } else if (im.src && !im.dataset.full && !e.target.querySelector("video.preview")) {
      im.dataset.full = im.src;
      im.removeAttribute("src");
    }
  }
}, { rootMargin: matchMedia("(pointer: coarse)").matches ? "800px" : "1500px" });

async function loadMore() {
  if (state.loading) return;
  if (state.total !== null && state.offset >= state.total) return;
  state.loading = true;
  try {
    abortCtl?.abort();
    abortCtl = new AbortController();
    const p = new URLSearchParams({
      offset: state.offset, limit: pageLimit,
      category: state.tab, sort: state.random ? "random" : state.sort,
      tags: state.tags.join(","),
    });
    if (state.random && state.seed) p.set("seed", state.seed);
    const d = await api(`/api/items?${p}`, abortCtl.signal);
    if (state.random && d.seed) {
      state.seed = d.seed; // fixa o seed sorteado pelo servidor
      syncUrl();
    }
    for (const it of d.items) {
      state.items.push(it);
      card(it);
    }
    state.offset += d.items.length;
    state.total = d.total;
    countEl.textContent = `${d.total} mídias`;
    emptyBox.hidden = d.total !== 0;
    if (lbWantMore && !lb.hidden && lbIdx < state.items.length - 1) {
      lbWantMore = false;
      lbIdx++;
      renderLightbox();
    }
  } catch (e) {
    if (e.name !== "AbortError") { /* silencioso */ }
  }
  state.loading = false;
}

function reset() {
  abortCtl?.abort();
  lbWantMore = false;
  thumbUnload.disconnect(); // solta observações de cards descartados
  state.offset = 0; state.total = null; state.items.length = 0;
  grid.replaceChildren();
  emptyBox.hidden = true;
  loadMore();
}

// ---- lightbox ----
let lbIdx = -1;
let lbWantMore = false; // ArrowRight no fim do carregado: avança quando a próxima página chegar
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

// lbStep navega no lightbox além da página carregada: perto do fim já
// pré-busca a próxima; no limite, busca e avança quando ela chegar.
function lbStep(d) {
  const n = lbIdx + d;
  if (n < 0) return;
  if (state.total !== null && n >= state.total) return;
  if (n < state.items.length) {
    lbIdx = n;
    renderLightbox();
    if (lbIdx >= state.items.length - 3) loadMore(); // prefetch
    return;
  }
  lbWantMore = true;
  loadMore();
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
  lbTags.hidden = true;
  lbContent.innerHTML = "";
  zimg = null;
  ptrs.clear(); pinch = null; lastPan = null;
  resetZoom();
  unlockScroll();
}
// variante de exibição no celular: thumb 1600px (fração do original);
// gif mantém o original (thumb seria JPEG estático, mataria a animação)
// ponytail: originais >1600px perdem detalhe no zoom 4x — trocar p/ /media no zoom se importar
function displaySrc(it) {
  return it.kind === "image" && it.mime !== "image/gif"
    ? `/thumb/${encodeURIComponent(it.name)}?w=1600`
    : `/media/${encodeURIComponent(it.name)}`;
}
function renderLightbox() {
  const it = state.items[lbIdx];
  if (!it) return closeLightbox();
  stopBoost();
  const prev = lbContent.querySelector("video");
  if (prev) { prev.pause(); prev.removeAttribute("src"); prev.load(); } // libera a banda do vídeo anterior
  lb.classList.remove("chrome-hidden");
  lbCounter.textContent = `${lbIdx + 1} / ${state.total ?? state.items.length}`;
  lbFilesize.textContent = fmtSize(it.size || 0);
  lbContent.innerHTML = "";
  const src = `/media/${encodeURIComponent(it.name)}`;
  zimg = null;
  resetZoom();
  let el;
  if (it.kind === "video") {
    el = document.createElement("video");
    el.controls = true; el.autoplay = true; el.playsInline = true; el.preload = "metadata";
    el.poster = `/thumb/${encodeURIComponent(it.name)}?w=400`; // thumb já em cache: pintura instantânea
    el.src = src;
    el.loop = true; // reprodução em loop infinito por padrão
    el.controlsList = "nodownload";
    el.disablePictureInPicture = true;
    el.setAttribute("disableremoteplayback", "");
    el.draggable = false;
    wireVideoGestures(el);
  } else if (it.kind === "image") {
    el = document.createElement("img");
    el.src = displaySrc(it); el.alt = it.name;
    el.draggable = false;
    el.setAttribute("selectstart", "return false");
    zimg = el;
  } else {
    el = document.createElement("p");
    el.className = "unsupported";
    el.textContent = `${it.name} — ${it.mime} (sem exibição no navegador)`;
  }
  lbContent.appendChild(el);
  lbFavBtn.classList.toggle("active", !!it.fav);
  renderLbTags();
  // pré-carrega a próxima imagem estática (swipe sem tela preta no 4G)
  const nx = state.items[lbIdx + 1];
  if (nx && nx.kind === "image" && nx.mime !== "image/gif") { const p = new Image(); p.src = displaySrc(nx); }
}

// ---- tags no lightbox: atrás de toggle, customs com × para remover --------
const lbTags = document.getElementById("lb-tags");
const lbTagsToggle = document.getElementById("lb-tags-toggle");
const lbTagEnv = document.getElementById("lb-tagenv");
const lbTagList = document.getElementById("lb-taglist");
const lbCustom = document.getElementById("lb-custom");
let lbTagsOpen = localStorage.getItem("sv-show-tags") === "1";

function syncTagsToggle(n) {
  lbTagsToggle.textContent = `🏷 ${n ?? 0}`;
  lbTagsToggle.setAttribute("aria-expanded", String(lbTagsOpen));
  lbTagEnv.hidden = !lbTagsOpen;
}
lbTagsToggle.addEventListener("click", () => {
  lbTagsOpen = !lbTagsOpen;
  localStorage.setItem("sv-show-tags", lbTagsOpen ? "1" : "0");
  syncTagsToggle((state.items[lbIdx]?.tags?.length || 0) + (state.items[lbIdx]?.custom?.length || 0));
});

function renderLbTags() {
  const it = state.items[lbIdx];
  if (!it) { lbTags.hidden = true; return; }
  lbTags.hidden = false;
  syncTagsToggle((it.tags || []).length + (it.custom || []).length);
  lbTagList.replaceChildren();
  for (const t of it.tags || []) {
    const b = document.createElement("button");
    b.type = "button";
    b.className = "ttag";
    b.textContent = t;
    b.title = `filtrar por ${t}`;
    b.addEventListener("click", () => addTag(t));
    lbTagList.appendChild(b);
  }
  for (const t of it.custom || []) {
    const s = document.createElement("span");
    s.className = "tcustom";
    s.textContent = `${t} `;
    const x = document.createElement("button");
    x.type = "button";
    x.textContent = "×";
    x.title = "remover";
    x.setAttribute("aria-label", `Remover ${t}`);
    x.addEventListener("click", async () => {
      const r = await fetch(`/api/ctag/${encodeURIComponent(it.name)}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ del: [t] }),
      });
      if (!r.ok) return;
      const d = await r.json();
      it.custom = d.custom || [];
      renderLbTags();
    });
    s.appendChild(x);
    lbTagList.appendChild(s);
  }
}

lbCustom.addEventListener("keydown", async (e) => {
  if (e.key !== "Enter") return;
  e.preventDefault();
  const it = state.items[lbIdx];
  const v = lbCustom.value.trim();
  if (!it || !v) return;
  const r = await fetch(`/api/ctag/${encodeURIComponent(it.name)}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ add: [v] }),
  });
  if (!r.ok) return;
  const d = await r.json();
  it.custom = d.custom || [];
  lbCustom.value = "";
  renderLbTags();
});

// ---- zoom de imagens no lightbox --------------------------------------------
// Pinch com 2 dedos (1x–4x) + pan arrastando quando ampliado; duplo toque /
// duplo clique alterna 1x <-> 2x ancorado no ponto tocado; roda do mouse
// também dá zoom. Com zoom > 1x, swipe não navega (arrasta a imagem).
const zoom = { scale: 1, x: 0, y: 0 };
let zimg = null;

function applyZoom() {
  if (!zimg) return;
  zimg.style.transform = `translate(${zoom.x}px, ${zoom.y}px) scale(${zoom.scale})`;
  zimg.classList.toggle("zoomed", zoom.scale > 1);
}
function resetZoom() {
  zoom.scale = 1; zoom.x = 0; zoom.y = 0;
  applyZoom();
}
// mantém o ponto da tela fixo enquanto muda a escala
function zoomAtPoint(px, py, newScale) {
  if (!zimg) return;
  newScale = Math.min(4, Math.max(1, newScale));
  const cx = innerWidth / 2, cy = innerHeight / 2;
  const rx = (px - cx - zoom.x) / zoom.scale;
  const ry = (py - cy - zoom.y) / zoom.scale;
  zoom.x = (px - cx) - newScale * rx;
  zoom.y = (py - cy) - newScale * ry;
  zoom.scale = newScale;
  if (newScale === 1) { zoom.x = 0; zoom.y = 0; }
  applyZoom();
}

const ptrs = new Map(); // pointerId -> {x,y}
let pinch = null;       // estado do gesto ativo
function pinchDist() {
  const [a, b] = [...ptrs.values()];
  return Math.hypot(a.x - b.x, a.y - b.y);
}
function pinchCenter() {
  const [a, b] = [...ptrs.values()];
  return { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 };
}

lbContent.addEventListener("pointerdown", (e) => {
  if (!(e.target instanceof HTMLImageElement)) return;
  ptrs.set(e.pointerId, { x: e.clientX, y: e.clientY });
  // base do pan: o próximo move calcula o delta a partir daqui
  if (ptrs.size === 1 && zoom.scale > 1) lastPan = { x: e.clientX, y: e.clientY };
  try { e.target.setPointerCapture(e.pointerId); } catch { /* pointer sintético/teste */ }
  if (ptrs.size === 2 && zimg) {
    pinch = { dist: pinchDist(), scale: zoom.scale, c: pinchCenter(), zx: zoom.x, zy: zoom.y };
  }
});
lbContent.addEventListener("pointermove", (e) => {
  if (!ptrs.has(e.pointerId)) return;
  ptrs.set(e.pointerId, { x: e.clientX, y: e.clientY });
  if (!zimg) return;
  if (pinch && ptrs.size >= 2) {
    const f = pinchDist() / pinch.dist;
    const ns = Math.min(4, Math.max(1, pinch.scale * f));
    const c = pinchCenter();
    // escala ancorada no centro inicial do gesto + segue o movimento dos dedos
    zoomAtPoint(pinch.c.x, pinch.c.y, ns);
    zoom.x += c.x - pinch.c.x;
    zoom.y += c.y - pinch.c.y;
    if (ns === 1) { zoom.x = 0; zoom.y = 0; }
    applyZoom();
  } else if (ptrs.size === 1 && zoom.scale > 1) {
    const prev = lastPan || { x: e.clientX, y: e.clientY };
    zoom.x += e.clientX - prev.x;
    zoom.y += e.clientY - prev.y;
    applyZoom();
    lastPan = { x: e.clientX, y: e.clientY };
  }
});
let lastPan = null;
function ptrEnd(e) {
  ptrs.delete(e.pointerId);
  if (ptrs.size === 1) {
    const [p] = [...ptrs.values()]; // dedo restante do pinch vira pan contínuo
    lastPan = { x: p.x, y: p.y };
  } else {
    lastPan = null;
  }
  if (ptrs.size < 2) pinch = null;
}
lbContent.addEventListener("pointerup", ptrEnd);
lbContent.addEventListener("pointercancel", ptrEnd);

// duplo toque/clique na IMAGEM alterna 1x <-> 2x no ponto; um único tap
// alterna a barra superior/navs (modo imersivo) — tudo num listener só
// para a ordem duplo-tap vs toggle ficar determinística
let lastTapT = 0;
lbContent.addEventListener("click", (e) => {
  if (lbSuppressTap) { lbSuppressTap = false; return; }
  const isImg = e.target instanceof HTMLImageElement;
  const t = e.target;
  if (t !== lbContent && !isImg && !(t instanceof HTMLVideoElement)) return;
  const now = Date.now();
  if (isImg && now - lastTapT < 300) {
    lastTapT = 0;
    if (zoom.scale > 1) resetZoom();
    else zoomAtPoint(e.clientX, e.clientY, 2);
    return; // duplo toque não alterna a barra
  }
  lastTapT = isImg ? now : 0;
  lb.classList.toggle("chrome-hidden");
});
lbContent.addEventListener("wheel", (e) => {
  if (!(e.target instanceof HTMLImageElement)) return;
  e.preventDefault();
  const f = e.deltaY < 0 ? 1.15 : 1 / 1.15;
  zoomAtPoint(e.clientX, e.clientY, zoom.scale * f);
}, { passive: false });
document.getElementById("lb-close").addEventListener("click", closeLightbox);
lb.addEventListener("click", (e) => { if (e.target === lb) closeLightbox(); });
document.getElementById("lb-prev").addEventListener("click", () => lbStep(-1));
document.getElementById("lb-next").addEventListener("click", () => lbStep(1));
document.addEventListener("keydown", (e) => {
  if (lb.hidden) return;
  if (e.key === "Escape") closeLightbox();
  if (e.key === "ArrowLeft") lbStep(-1);
  if (e.key === "ArrowRight") lbStep(1);
});

// swipe no lightbox (mobile): horizontal E vertical navegam entre mídias;
// touch-action:none + preventDefault garantem que o fundo não role junto
function lbSwipe(dx, dy) {
  const adx = Math.abs(dx), ady = Math.abs(dy);
  const MIN = 40; // dedo real curva: limiar um pouco mais permissivo
  if (adx > MIN && ady < adx * 0.9) {
    if (dx < 0) lbStep(1);
    else if (dx > 0) lbStep(-1);
    return;
  }
  if (ady > MIN && adx < ady * 0.9) {
    if (dy < 0) lbStep(1);
    else if (dy > 0) lbStep(-1);
  }
}
let tX = 0, tY = 0;
lb.addEventListener("touchstart", (e) => {
  const t = e.changedTouches[0];
  tX = t.clientX; tY = t.clientY;
}, { passive: true });
lb.addEventListener("touchmove", (e) => e.preventDefault(), { passive: false });
lb.addEventListener("touchend", (e) => {
  if (zoom.scale > 1) { // imagem ampliada: gesto é pan/zoom, não navegação
    tX = e.changedTouches[0].clientX; tY = e.changedTouches[0].clientY;
    return;
  }
  const t = e.changedTouches[0];
  lbSwipe(t.clientX - tX, t.clientY - tY);
}, { passive: true });

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

// ---- controles de aba/ordem ----
document.querySelectorAll(".tab").forEach((b) => { b.addEventListener("click", () => setTab(b.dataset.cat)); });
sortSel.addEventListener("change", () => {
  state.sort = sortSel.value;
  localStorage.setItem(`sv-sort-${state.tab}`, state.sort);
  syncUrl();
  reset();
});
randBtn.addEventListener("click", () => setRandom(!state.random));

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
let dlTimer = null;

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
  if (st.sync_mode) html += dlRow("Modo", st.sync_mode === "incremental" ? "sync rápido" : "completo");
  if (st.active) {
    html += dlRow("Taxa", `${fmtSize(st.rate_bps)}/s`);
    html += dlRow("Sessão", `${st.session_files} arq · ${fmtSize(st.session_bytes)}`);
  }
  if (st.vault_files) html += dlRow("Vault", `${st.vault_files} arq · ${fmtSize(st.vault_bytes)}`);
  if (st.age_s !== undefined && st.updated_at) html += dlRow("Última atividade", st.age_s < 90 ? `há ${st.age_s}s` : new Date(st.updated_at * 1000).toLocaleTimeString());
  dlBody.innerHTML = html;

  if (st.stuck) {
    dlHint.textContent = "nenhum progresso há mais de 3 min — pare pelo terminal (Ctrl+C) e inicie de novo";
  } else {
    dlHint.textContent = "início e parada pelo terminal: docker compose run --rm downloader";
  }
}

async function refreshDl() {
  try {
    const r = await fetch("/api/download/status");
    if (r.ok) renderDl(await r.json());
  } catch { /* silencioso */ }
}

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

// ---- boot: restaura aba/sort/modo aleatório da URL ----
{
  const p = new URLSearchParams(location.search);
  const tab = SORTS[p.get("tab")] ? p.get("tab") : "all";
  state.tab = tab;
  state.sort = sortFor(tab);
  if (SORTS[tab].some(([k]) => k === p.get("sort"))) {
    state.sort = p.get("sort");
    localStorage.setItem(`sv-sort-${tab}`, state.sort);
  }
  // modo aleatório: ?rand=1 força ON, rand=0 força OFF; sem parâmetro,
  // segue a preferência salva (padrão ON). Seed restaura a mesma ordem.
  const rp = p.get("rand");
  if (rp === "1") state.random = true;
  else if (rp === "0") state.random = false;
  const sp = p.get("seed");
  if (state.random && sp) state.seed = Number(sp) || null;
  // filtro de tags via deep-link (?tags=a,b)
  if (p.get("tags")) {
    state.tags = p.get("tags").split(",").map(normTagUI).filter(Boolean);
    renderChips();
  }
  document.querySelectorAll(".tab").forEach((b) => { b.classList.toggle("active", b.dataset.cat === tab); });
  renderSortOptions();
  applyRandomUI();
}
reset();
refreshStats();
setInterval(() => { if (!document.hidden) refreshStats(); }, 60000); // 15s acordava o rádio do celular à toa
document.addEventListener("visibilitychange", () => { if (!document.hidden) refreshStats(); });

// altura do header vira var CSS (as abas sticky se posicionam por ela;
// no mobile o header quebra em 2 linhas e a altura muda)
function syncHeadVar() {
  document.documentElement.style.setProperty(
    "--headH", document.querySelector("header").offsetHeight + "px");
}
syncHeadVar();
addEventListener("resize", syncHeadVar);
