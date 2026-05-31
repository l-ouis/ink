// canvas.js drives the infinite canvas: pan, zoom, and (for the signed-in
// owner) adding, moving, resizing and editing text and image items. Geometry
// lives in each item's data-* attributes; the server is the source of truth and
// is updated after every change.
(() => {
  "use strict";

  const viewport = document.getElementById("viewport");
  const world = document.getElementById("world");
  const hintEl = document.getElementById("hint");
  const admin = document.body.dataset.authed === "1";
  const CSRF = document.body.dataset.csrf || "";

  // Zoom is clamped so the owner can't zoom in so far that rendering thrashes,
  // nor so far out that everything vanishes.
  const MIN_SCALE = 0.2;
  const MAX_SCALE = 2.5;
  const VIEW_KEY = "ink-view-v1";

  const view = { x: 0, y: 0, scale: 1 };

  function clampScale(s) { return Math.min(MAX_SCALE, Math.max(MIN_SCALE, s)); }

  function applyView() {
    world.style.transform = `translate(${view.x}px, ${view.y}px) scale(${view.scale})`;
    try { localStorage.setItem(VIEW_KEY, JSON.stringify(view)); } catch (_) {}
  }

  // screenToWorld converts a viewport (client) point into world coordinates.
  function screenToWorld(sx, sy) {
    return { x: (sx - view.x) / view.scale, y: (sy - view.y) / view.scale };
  }

  // ---- Item geometry ----

  function num(el, key) { return parseFloat(el.dataset[key]) || 0; }

  function layout(el) {
    el.style.left = num(el, "x") + "px";
    el.style.top = num(el, "y") + "px";
    const w = num(el, "w");
    if (w > 0) el.style.width = w + "px";
  }

  function layoutAll() { world.querySelectorAll(".item").forEach(layout); }

  // centerOrigin puts the world origin (0,0) — the canvas centre and the spawn
  // point new visitors land on — at the middle of the screen, at 1:1 zoom.
  function centerOrigin() {
    view.x = window.innerWidth / 2;
    view.y = window.innerHeight / 2;
    view.scale = 1;
    applyView();
  }

  // ---- Pan / zoom / drag input ----

  const pointers = new Map(); // pointerId -> {x, y}
  let mode = null;            // 'pan' | 'drag' | 'resize' | 'pinch'
  let start = null;           // mode-specific starting state
  let target = null;          // item element being dragged/resized
  let moved = false;
  let pinch = null;

  viewport.addEventListener("pointerdown", (e) => {
    pointers.set(e.pointerId, { x: e.clientX, y: e.clientY });

    if (pointers.size === 2) { beginPinch(); return; }
    if (pointers.size > 2 || mode === "pinch") return;

    const itemEl = admin ? e.target.closest(".item") : null;
    if (admin && e.target.classList.contains("item-del")) return; // handled on click
    if (admin && e.target.classList.contains("item-resize")) {
      target = e.target.closest(".item");
      mode = "resize";
      start = { px: e.clientX, py: e.clientY, w: num(target, "w") || target.offsetWidth };
      raise(target);
    } else if (itemEl) {
      target = itemEl;
      mode = "drag";
      start = { px: e.clientX, py: e.clientY, x: num(itemEl, "x"), y: num(itemEl, "y") };
      raise(itemEl);
    } else {
      mode = "pan";
      start = { px: e.clientX, py: e.clientY, vx: view.x, vy: view.y };
      viewport.classList.add("panning");
    }
    moved = false;
  });

  window.addEventListener("pointermove", (e) => {
    if (!pointers.has(e.pointerId)) return;
    pointers.set(e.pointerId, { x: e.clientX, y: e.clientY });

    if (mode === "pinch") { movePinch(); return; }
    if (!mode) return;

    const dx = e.clientX - start.px, dy = e.clientY - start.py;
    if (Math.abs(dx) > 2 || Math.abs(dy) > 2) moved = true;

    if (mode === "pan") {
      view.x = start.vx + dx; view.y = start.vy + dy; applyView();
    } else if (mode === "drag") {
      target.dataset.x = start.x + dx / view.scale;
      target.dataset.y = start.y + dy / view.scale;
      layout(target);
    } else if (mode === "resize") {
      target.dataset.w = Math.max(40, start.w + dx / view.scale);
      layout(target);
    }
  });

  function endPointer(e) {
    pointers.delete(e.pointerId);
    if (mode === "pinch") {
      if (pointers.size < 2) { mode = null; pinch = null; }
      return;
    }
    if ((mode === "drag" || mode === "resize") && moved && target) saveGeometry(target);
    if (pointers.size === 0) {
      mode = null; target = null; start = null;
      viewport.classList.remove("panning");
    }
  }
  window.addEventListener("pointerup", endPointer);
  window.addEventListener("pointercancel", endPointer);

  // Wheel zoom, anchored on the cursor.
  viewport.addEventListener("wheel", (e) => {
    e.preventDefault();
    const factor = Math.pow(1.0015, -e.deltaY);
    zoomAt(e.clientX, e.clientY, clampScale(view.scale * factor));
  }, { passive: false });

  function zoomAt(cx, cy, newScale) {
    const w = screenToWorld(cx, cy);
    view.scale = newScale;
    view.x = cx - w.x * newScale;
    view.y = cy - w.y * newScale;
    applyView();
  }

  function beginPinch() {
    const pts = [...pointers.values()];
    const mid = { x: (pts[0].x + pts[1].x) / 2, y: (pts[0].y + pts[1].y) / 2 };
    pinch = {
      dist: Math.hypot(pts[0].x - pts[1].x, pts[0].y - pts[1].y),
      scale: view.scale,
      world: screenToWorld(mid.x, mid.y),
    };
    mode = "pinch";
    viewport.classList.remove("panning");
  }

  function movePinch() {
    const pts = [...pointers.values()];
    if (pts.length < 2) return;
    const mid = { x: (pts[0].x + pts[1].x) / 2, y: (pts[0].y + pts[1].y) / 2 };
    const dist = Math.hypot(pts[0].x - pts[1].x, pts[0].y - pts[1].y);
    view.scale = clampScale(pinch.scale * (dist / pinch.dist));
    view.x = mid.x - pinch.world.x * view.scale;
    view.y = mid.y - pinch.world.y * view.scale;
    applyView();
  }

  // ---- Server sync ----

  async function postForm(url, data) {
    const body = new URLSearchParams();
    body.set("csrf", CSRF);
    for (const k in data) body.set(k, data[k]);
    const res = await fetch(url, {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body,
    });
    if (!res.ok) throw new Error(await res.text());
    return res;
  }

  // payload gathers every writable field of an item for an update request, so
  // moving a box never clobbers its content or options.
  function payload(el) {
    return {
      id: el.dataset.id,
      x: num(el, "x"), y: num(el, "y"), w: num(el, "w"), h: num(el, "h"),
      z: num(el, "z"),
      content: el.dataset.md || "",
      layer: el.dataset.layer || "",
      adaptive: el.dataset.adaptive === "1" ? "1" : "",
    };
  }

  async function saveGeometry(el) {
    try {
      await postForm("/admin/item/update", payload(el));
    } catch (err) { hint("Couldn't save position"); }
    refreshAdaptive();
  }

  // Stacking bands keep all "under" images below text and all "over" images
  // above it, regardless of creation order. Within a band, the item's own z
  // gives relative order.
  function bandBase(el) {
    if (el.dataset.type === "image") return el.dataset.layer === "over" ? 2000000 : 0;
    return 1000000;
  }
  function effZ(el) { return bandBase(el) + num(el, "z"); }
  function applyZ(el) { el.style.zIndex = String(effZ(el)); }

  // raise brings a text box to the front on interaction. Images are left alone
  // so their explicit arrange order (set in the image dialog) isn't disturbed by
  // dragging. The new z persists with the next geometry save.
  function raise(el) {
    if (el.dataset.type === "image") return;
    el.dataset.z = ++topZ;
    applyZ(el);
  }

  let topZ = 0;
  world.querySelectorAll(".item").forEach((el) => { topZ = Math.max(topZ, num(el, "z")); applyZ(el); });

  // ---- Item construction ----

  function makeItem({ id, type, x, y, w, h, z, src, html, layer, adaptive }) {
    const el = document.createElement("div");
    el.className = `item item-${type}` + (type === "text" && adaptive ? " adaptive" : "");
    el.dataset.id = id; el.dataset.type = type;
    el.dataset.x = x; el.dataset.y = y; el.dataset.w = w; el.dataset.h = h || 0;
    el.dataset.md = src || "";
    el.dataset.layer = type === "image" ? (layer || "under") : "";
    el.dataset.adaptive = adaptive ? "1" : "";
    const bodyHtml = type === "image"
      ? `<img alt="" draggable="false">`
      : (html || "");
    el.innerHTML =
      `<div class="item-body">${bodyHtml}</div>` +
      `<button class="item-del" type="button" title="Delete" aria-label="Delete">×</button>` +
      `<span class="item-resize" title="Resize" aria-hidden="true"></span>`;
    if (type === "image") {
      const img = el.querySelector("img");
      img.addEventListener("load", refreshAdaptive);
      img.src = src;
    } else if (adaptive) {
      wrapLetters(el.querySelector(".item-body"));
    }
    world.appendChild(el);
    layout(el);
    el.dataset.z = (z == null) ? ++topZ : z;
    topZ = Math.max(topZ, num(el, "z"));
    applyZ(el);
    refreshAdaptive();
    return el;
  }

  // viewportCenterWorld returns world coords at the center of the screen.
  function centerWorld() { return screenToWorld(window.innerWidth / 2, window.innerHeight / 2); }

  // ---- Admin actions ----

  if (admin) setupAdmin();

  function setupAdmin() {
    const editorDialog = document.getElementById("editor");
    const editorText = document.getElementById("editor-text");
    const editorForm = document.getElementById("editor-form");
    let editing = null; // item element currently being edited

    document.getElementById("add-text").addEventListener("click", async () => {
      const c = centerWorld();
      const w = 320;
      try {
        const defaultMd = "# New note\n\nDouble-click to edit.";
        const res = await postForm("/admin/item/add", {
          type: "text", x: Math.round(c.x - w / 2), y: Math.round(c.y - 40), w, h: 0,
          content: defaultMd, adaptive: "1",
        });
        const data = await res.json();
        const el = makeItem({ id: data.id, type: "text", x: Math.round(c.x - w / 2), y: Math.round(c.y - 40), w, z: data.z, src: defaultMd, html: data.html, adaptive: true });
        openEditor(el);
      } catch (err) { hint("Couldn't add text box"); }
    });

    const imageInput = document.getElementById("image-input");
    document.getElementById("add-image").addEventListener("click", () => imageInput.click());
    imageInput.addEventListener("change", async () => {
      const file = imageInput.files[0];
      imageInput.value = "";
      if (!file) return;
      hint("Uploading…");
      try {
        const fd = new FormData();
        fd.set("csrf", CSRF);
        fd.set("file", file);
        const up = await fetch("/admin/upload", { method: "POST", body: fd });
        if (!up.ok) throw new Error(await up.text());
        const { url } = await up.json();
        const w = await naturalWidth(url);
        const c = centerWorld();
        const x = Math.round(c.x - w / 2), y = Math.round(c.y - w / 3);
        const res = await postForm("/admin/item/add", { type: "image", x, y, w, h: 0, content: url, layer: "under" });
        const { id, z } = await res.json();
        makeItem({ id, type: "image", x, y, w, z, src: url, layer: "under" });
        hideHint();
      } catch (err) { hint("Upload failed"); }
    });

    // Settings dialog.
    const settings = document.getElementById("settings");
    document.getElementById("open-settings").addEventListener("click", () => settings.showModal());
    document.getElementById("close-settings").addEventListener("click", () => settings.close());

    // Delete via the per-item button.
    world.addEventListener("click", async (e) => {
      const del = e.target.closest(".item-del");
      if (!del) return;
      const el = del.closest(".item");
      if (!confirm("Delete this item?")) return;
      try {
        await postForm("/admin/item/delete", { id: el.dataset.id });
        el.remove();
        refreshAdaptive();
      } catch (err) { hint("Couldn't delete"); }
    });

    // Double-click a text box to edit its Markdown, or an image to set options.
    world.addEventListener("dblclick", (e) => {
      const txt = e.target.closest(".item-text");
      if (txt) { e.preventDefault(); openEditor(txt); return; }
      const img = e.target.closest(".item-image");
      if (img) { e.preventDefault(); openImageOpts(img); }
    });

    const editorAdaptive = document.getElementById("editor-adaptive");
    function openEditor(el) {
      editing = el;
      el.classList.add("editing");
      editorText.value = el.dataset.md || "";
      editorAdaptive.checked = el.dataset.adaptive === "1";
      editorDialog.showModal();
      editorText.focus();
    }
    function closeEditor() {
      if (editing) editing.classList.remove("editing");
      editing = null;
      editorDialog.close();
    }
    document.getElementById("editor-cancel").addEventListener("click", closeEditor);
    editorDialog.addEventListener("cancel", (e) => { e.preventDefault(); closeEditor(); });

    editorForm.addEventListener("submit", async (e) => {
      e.preventDefault();
      if (!editing) return;
      const el = editing;
      el.dataset.md = editorText.value;
      el.dataset.adaptive = editorAdaptive.checked ? "1" : "";
      el.classList.toggle("adaptive", editorAdaptive.checked);
      try {
        const res = await postForm("/admin/item/update", payload(el));
        const { html } = await res.json();
        const body = el.querySelector(".item-body");
        body.innerHTML = html;
        if (editorAdaptive.checked) wrapLetters(body);
        else body.style.color = "";
      } catch (err) { hint("Couldn't save"); }
      closeEditor();
      refreshAdaptive();
    });

    editorText.addEventListener("keydown", (e) => {
      if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
        e.preventDefault();
        editorForm.requestSubmit();
      }
    });

    // Image options dialog: layer (under/over text) and stacking order. Changes
    // apply live.
    const optsDialog = document.getElementById("image-opts");
    const layerRadios = [...optsDialog.querySelectorAll('input[name="layer"]')];
    let optsTarget = null;

    function openImageOpts(el) {
      optsTarget = el;
      const layer = el.dataset.layer === "over" ? "over" : "under";
      layerRadios.forEach((r) => { r.checked = r.value === layer; });
      optsDialog.showModal();
    }
    layerRadios.forEach((r) => r.addEventListener("change", async () => {
      if (!optsTarget || !r.checked) return;
      optsTarget.dataset.layer = r.value === "over" ? "over" : "under";
      applyZ(optsTarget);
      try { await postForm("/admin/item/update", payload(optsTarget)); }
      catch (err) { hint("Couldn't save"); }
      refreshAdaptive();
    }));
    optsDialog.querySelectorAll("[data-z]").forEach((b) =>
      b.addEventListener("click", () => { if (optsTarget) reorderImage(optsTarget, b.dataset.z); }));
    document.getElementById("image-opts-close").addEventListener("click", () => { optsDialog.close(); optsTarget = null; });
    optsDialog.addEventListener("cancel", () => { optsTarget = null; });

    // reorderImage restacks el among the images sharing its layer, then persists
    // every changed item's new z. front/back jump to an end; forward/backward
    // swap with the neighbour. z is compacted to 0..n-1 so order stays clean.
    function reorderImage(el, op) {
      const layer = el.dataset.layer === "over" ? "over" : "under";
      const peers = [...world.querySelectorAll(".item-image")]
        .filter((im) => (im.dataset.layer === "over" ? "over" : "under") === layer)
        .sort((a, b) => num(a, "z") - num(b, "z"));
      const i = peers.indexOf(el);
      if (i < 0) return;
      const order = peers.slice();
      if (op === "front") { order.splice(i, 1); order.push(el); }
      else if (op === "back") { order.splice(i, 1); order.unshift(el); }
      else if (op === "forward" && i < order.length - 1) { order[i] = order[i + 1]; order[i + 1] = el; }
      else if (op === "backward" && i > 0) { order[i] = order[i - 1]; order[i - 1] = el; }
      else return;
      const changed = [];
      order.forEach((im, idx) => { if (num(im, "z") !== idx) { im.dataset.z = idx; applyZ(im); changed.push(im); } });
      if (!changed.length) return;
      topZ = Math.max(topZ, ...[...world.querySelectorAll(".item")].map((x) => num(x, "z")));
      Promise.all(changed.map((im) => postForm("/admin/item/update", payload(im))))
        .then(refreshAdaptive)
        .catch(() => hint("Couldn't reorder"));
    }
  }

  // naturalWidth resolves an image's intrinsic width (capped), for a sensible
  // default placement size.
  function naturalWidth(url) {
    return new Promise((resolve) => {
      const img = new Image();
      img.onload = () => resolve(Math.min(img.naturalWidth || 360, 480));
      img.onerror = () => resolve(360);
      img.src = url;
    });
  }

  // ---- Adaptive text contrast ----
  // Each glyph of an "adaptive" text box is wrapped in a span; the brightness
  // of the image behind that individual glyph decides whether it is painted
  // black (on light) or white (on dark), so a single box can mix both.
  const scratch = document.createElement("canvas");
  scratch.width = scratch.height = 16;
  const sctx = scratch.getContext("2d", { willReadFrequently: true });

  function worldRect(el) {
    return { x: num(el, "x"), y: num(el, "y"), w: el.offsetWidth, h: el.offsetHeight };
  }

  // wrapLetters replaces every visible character in body with a <span class=ac>,
  // leaving whitespace as plain text and skipping code blocks (which carry
  // their own colours). Idempotent: it no-ops if already wrapped.
  function wrapLetters(body) {
    if (body.querySelector(".ac")) return;
    const walker = document.createTreeWalker(body, NodeFilter.SHOW_TEXT, {
      acceptNode(n) {
        if (!n.nodeValue.trim()) return NodeFilter.FILTER_REJECT;
        return n.parentElement.closest("pre, code") ? NodeFilter.FILTER_REJECT : NodeFilter.FILTER_ACCEPT;
      },
    });
    const nodes = [];
    while (walker.nextNode()) nodes.push(walker.currentNode);
    for (const node of nodes) {
      const frag = document.createDocumentFragment();
      for (const ch of node.nodeValue) {
        if (ch === " " || ch === "\t" || ch === "\n") { frag.appendChild(document.createTextNode(ch)); continue; }
        const span = document.createElement("span");
        span.className = "ac";
        span.textContent = ch;
        frag.appendChild(span);
      }
      node.parentNode.replaceChild(frag, node);
    }
  }

  // regionColor returns the mean colour {r,g,b} (0–255) of the source rectangle
  // of img, or null if it can't be read.
  function regionColor(img, sx, sy, sw, sh) {
    sw = Math.max(1, sw); sh = Math.max(1, sh);
    try {
      sctx.clearRect(0, 0, 16, 16);
      sctx.drawImage(img, sx, sy, sw, sh, 0, 0, 16, 16);
      const d = sctx.getImageData(0, 0, 16, 16).data;
      let r = 0, g = 0, b = 0, n = 0;
      for (let i = 0; i < d.length; i += 4) {
        if (d[i + 3] < 8) continue; // skip transparent pixels
        r += d[i]; g += d[i + 1]; b += d[i + 2]; n++;
      }
      return n ? { r: r / n, g: g / n, b: b / n } : null;
    } catch (_) { return null; }
  }

  function rgbToHsl(r, g, b) {
    r /= 255; g /= 255; b /= 255;
    const max = Math.max(r, g, b), min = Math.min(r, g, b), d = max - min;
    let h = 0;
    const l = (max + min) / 2;
    const s = d === 0 ? 0 : d / (1 - Math.abs(2 * l - 1));
    if (d !== 0) {
      if (max === r) h = ((g - b) / d) % 6;
      else if (max === g) h = (b - r) / d + 2;
      else h = (r - g) / d + 4;
      h *= 60;
      if (h < 0) h += 360;
    }
    return { h, s, l };
  }

  // Adaptive-colour tuning. The text takes the background's *complementary* hue
  // (opposite on the wheel) so it contrasts rather than echoes, forces enough
  // saturation that it never reads as a plain grey, and shifts lightness far
  // enough to stay legible. fallbackHue is used only when the background is
  // achromatic (grey/black/white) and so has no hue to contrast.
  const AC = { hueRotate: 180, fallbackHue: 210, sFloor: 0.5, sBoost: 0.22, lShift: 0.42, lLightMax: 0.95, lDarkMin: 0.08, shadow: "0 1px 2px rgba(0,0,0,0.6)" };

  // adaptiveColor picks a readable, non-grey colour that contrasts background bg.
  function adaptiveColor(bg) {
    const { h, s, l } = rgbToHsl(bg.r, bg.g, bg.b);
    // A coloured background gets its complement; a hueless one gets a single
    // fixed hue (so neutral areas don't cycle into a rainbow across the text).
    const hue = s > 0.12 ? (h + AC.hueRotate) % 360 : AC.fallbackHue;
    const sat = Math.max(AC.sFloor, Math.min(0.95, s + AC.sBoost));
    // Decide direction by HSL lightness (how light it *looks*), not perceived
    // luminance, so light-blue/light-yellow backgrounds don't fool the shift and
    // wash the text out. This guarantees a fixed lightness gap from the bg.
    const light = l < 0.5
      ? Math.min(AC.lLightMax, l + AC.lShift)
      : Math.max(AC.lDarkMin, l - AC.lShift);
    return `hsl(${hue.toFixed(0)} ${(sat * 100).toFixed(0)}% ${(light * 100).toFixed(0)}%)`;
  }

  function clearGlyph(s) { s.style.color = ""; s.style.textShadow = ""; }

  // imageBehind returns the front-most image stacked under z that contains the
  // world point (wx, wy), or null.
  function imageBehind(images, z, wx, wy) {
    let best = null, bestZ = -Infinity;
    for (const im of images) {
      const iz = effZ(im);
      if (iz >= z || iz <= bestZ) continue;
      const ir = worldRect(im);
      if (wx >= ir.x && wx <= ir.x + ir.w && wy >= ir.y && wy <= ir.y + ir.h) { bestZ = iz; best = { im, ir }; }
    }
    return best;
  }

  function refreshAdaptive() {
    const texts = world.querySelectorAll(".item-text.adaptive");
    if (!texts.length) return;
    const images = [...world.querySelectorAll(".item-image")];
    texts.forEach((t) => {
      const tz = effZ(t);
      const spans = t.querySelectorAll(".ac");
      // Read every glyph rect up front; colour writes are paint-only and won't
      // invalidate the following reads.
      const jobs = [];
      spans.forEach((s) => jobs.push({ s, r: s.getBoundingClientRect() }));
      for (const { s, r } of jobs) {
        const c = screenToWorld(r.left + r.width / 2, r.top + r.height / 2);
        const best = images.length ? imageBehind(images, tz, c.x, c.y) : null;
        if (!best) { clearGlyph(s); continue; } // no image behind: plain text, no shadow
        const img = best.im.querySelector("img");
        if (!img || !img.complete || !img.naturalWidth) continue; // retried on img load
        const scale = img.naturalWidth / best.ir.w; // world units -> source px
        const lw = r.width / view.scale, lh = r.height / view.scale;
        const bg = regionColor(img, (c.x - lw / 2 - best.ir.x) * scale, (c.y - lh / 2 - best.ir.y) * scale, lw * scale, lh * scale);
        if (!bg) { clearGlyph(s); continue; }
        s.style.color = adaptiveColor(bg);
        s.style.textShadow = AC.shadow; // only over an image
      }
    });
  }

  // ---- Hint toast ----
  let hintTimer = null;
  function hint(msg) {
    hintEl.textContent = msg;
    hintEl.classList.add("show");
    clearTimeout(hintTimer);
    hintTimer = setTimeout(hideHint, 2200);
  }
  function hideHint() { hintEl.classList.remove("show"); }

  // ---- Boot ----
  layoutAll();
  let restored = false;
  try {
    const saved = JSON.parse(localStorage.getItem(VIEW_KEY));
    if (saved && typeof saved.x === "number") {
      view.x = saved.x; view.y = saved.y; view.scale = clampScale(saved.scale);
      restored = true;
    }
  } catch (_) {}
  if (restored) applyView(); else centerOrigin();
  world.querySelectorAll(".item-text.adaptive").forEach((t) => wrapLetters(t.querySelector(".item-body")));
  refreshAdaptive();
  window.addEventListener("load", refreshAdaptive);
})();
