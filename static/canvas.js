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

  // The owner can flip into "preview": the canvas exactly as a visitor sees it,
  // with all editing chrome and interactions suppressed. canEdit() gates every
  // owner-only interaction so a single flag turns them all off at once.
  let previewing = false;
  function canEdit() { return admin && !previewing; }

  // Zoom is clamped so the owner can't zoom in so far that rendering thrashes,
  // nor so far out that everything vanishes.
  const MIN_SCALE = 0.2;
  const MAX_SCALE = 2.5;
  const VIEW_KEY = "ink-view-v1";

  const view = { x: 0, y: 0, scale: 1 };

  function clampScale(s) { return Math.min(MAX_SCALE, Math.max(MIN_SCALE, s)); }

  let idleRaster = null;
  function applyView() {
    world.style.transform = `translate(${view.x}px, ${view.y}px) scale(${view.scale})`;
    // Promote to a GPU layer for smooth gestures, but drop the promotion shortly
    // after movement stops so the browser re-rasterizes text/SVG sharply at the
    // current zoom instead of stretching a cached bitmap.
    world.style.willChange = "transform";
    clearTimeout(idleRaster);
    idleRaster = setTimeout(() => { world.style.willChange = "auto"; }, 200);
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

  // ---- Beacons ----
  // A beacon is a named item location. A Markdown link whose target matches an
  // item's data-beacon — [text](id) — flies the canvas to centre that item
  // instead of navigating. This works for every visitor, not just the owner.
  // The reserved targets "origin"/"home" always fly to (0,0), with no beacon.

  world.addEventListener("click", (e) => {
    const a = e.target.closest(".item-body a");
    if (!a) return;
    const id = (a.getAttribute("href") || "").replace(/^#/, "").trim();
    if (!id) return;
    if (id === "origin" || id === "home") { e.preventDefault(); goOrigin(); return; }
    const el = world.querySelector(`.item[data-beacon="${CSS.escape(id)}"]`);
    if (!el) return; // not a beacon: let the link behave normally
    e.preventDefault();
    flyTo(el);
  });

  // goOrigin flies back to the canvas centre (0,0) at 1:1 zoom — the spawn view.
  function goOrigin() { animateView(window.innerWidth / 2, window.innerHeight / 2, 1); }

  // flyTo smoothly pans (keeping the current zoom) so el sits in the middle of
  // the screen.
  function flyTo(el) {
    const cx = num(el, "x") + el.offsetWidth / 2;
    const cy = num(el, "y") + el.offsetHeight / 2;
    animateView(
      window.innerWidth / 2 - cx * view.scale,
      window.innerHeight / 2 - cy * view.scale
    );
  }

  let animRAF = null;
  function animateView(tx, ty, ts) {
    if (animRAF) cancelAnimationFrame(animRAF);
    const sx = view.x, sy = view.y, ss = view.scale;
    const tscale = (ts == null) ? ss : ts; // keep the current zoom unless told otherwise
    const dur = 450;
    let t0 = null;
    const step = (now) => {
      if (t0 === null) t0 = now;
      const k = Math.min(1, (now - t0) / dur);
      // easeInOutQuad
      const e = k < 0.5 ? 2 * k * k : 1 - Math.pow(-2 * k + 2, 2) / 2;
      view.x = sx + (tx - sx) * e;
      view.y = sy + (ty - sy) * e;
      view.scale = ss + (tscale - ss) * e;
      applyView();
      if (k < 1) { animRAF = requestAnimationFrame(step); }
      else { animRAF = null; refreshAdaptive(); }
    };
    animRAF = requestAnimationFrame(step);
  }

  // Clicking the corner label flies back to the origin (0,0) at 1:1 zoom rather
  // than reloading the page.
  const brand = document.querySelector(".brand");
  if (brand) brand.addEventListener("click", (e) => {
    e.preventDefault();
    goOrigin();
  });

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

    const itemEl = canEdit() ? e.target.closest(".item") : null;
    if (canEdit() && e.target.closest(".item-del-confirm")) return; // handled on click
    if (canEdit() && e.target.classList.contains("item-del")) return; // handled on click
    if (canEdit() && e.target.classList.contains("item-resize")) {
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
      beacon: el.dataset.beacon || "",
      original: el.dataset.original || "",
      crop: el.dataset.crop || "",
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
    if (el.dataset.type === "beacon") return 8000000; // landmarks ride above all content, just below the origin star
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

  // The beacon marker: the same curved-diamond sparkle as the origin star, in
  // indigo. Kept in sync with the server-rendered SVG in templates/canvas.html.
  const BEACON_SVG =
    `<svg class="beacon-star" viewBox="0 0 24 24" aria-hidden="true">` +
    `<path d="M12 0 Q13.6 10.4 24 12 Q13.6 13.6 12 24 Q10.4 13.6 0 12 Q10.4 10.4 12 0Z"/></svg>`;

  function makeItem({ id, type, x, y, w, h, z, src, html, layer, adaptive, beacon }) {
    const el = document.createElement("div");
    el.className = `item item-${type}` + (type === "text" && adaptive ? " adaptive" : "");
    el.dataset.id = id; el.dataset.type = type;
    el.dataset.x = x; el.dataset.y = y; el.dataset.w = w; el.dataset.h = h || 0;
    el.dataset.md = src || "";
    el.dataset.layer = type === "image" ? (layer || "under") : "";
    el.dataset.adaptive = adaptive ? "1" : "";
    el.dataset.beacon = type === "beacon" ? (beacon || "") : "";
    el.dataset.original = ""; el.dataset.crop = "";
    if (type === "beacon") el.title = beacon ? `Beacon: ${beacon}` : "Beacon";
    const bodyHtml = type === "image"
      ? `<img alt="" draggable="false">`
      : type === "beacon"
      ? BEACON_SVG
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
    // View toggle: preview the canvas as a visitor sees it (chrome hidden,
    // editing off) without signing out. The choice persists across reloads.
    const PREVIEW_KEY = "ink-preview-v1";
    const viewToggle = document.getElementById("view-toggle");
    function setPreview(on) {
      previewing = on;
      document.body.classList.toggle("preview", on);
      viewToggle.textContent = on ? "✏ Edit" : "👁 Preview";
      viewToggle.title = on ? "Back to editing" : "Preview as a visitor";
      try { localStorage.setItem(PREVIEW_KEY, on ? "1" : ""); } catch (_) {}
    }
    viewToggle.addEventListener("click", () => setPreview(!previewing));
    try { if (localStorage.getItem(PREVIEW_KEY) === "1") setPreview(true); } catch (_) {}

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

    // Add a beacon at the screen centre, then open its dialog to name it.
    const beaconDialog = document.getElementById("beacon-opts");
    const beaconForm = document.getElementById("beacon-form");
    const beaconIdInput = document.getElementById("beacon-id");
    let beaconEditing = null;

    document.getElementById("add-beacon").addEventListener("click", async () => {
      const c = centerWorld();
      const x = Math.round(c.x - 11), y = Math.round(c.y - 11); // centre the 22px marker
      try {
        const res = await postForm("/admin/item/add", { type: "beacon", x, y, w: 0, h: 0, beacon: "" });
        const { id, z } = await res.json();
        const el = makeItem({ id, type: "beacon", x, y, w: 0, z, beacon: "" });
        openBeacon(el);
      } catch (err) { hint("Couldn't add beacon"); }
    });

    function openBeacon(el) {
      beaconEditing = el;
      beaconIdInput.value = el.dataset.beacon || "";
      beaconDialog.showModal();
      beaconIdInput.focus();
      beaconIdInput.select();
    }
    function closeBeacon() { beaconEditing = null; beaconDialog.close(); }
    beaconForm.addEventListener("submit", async (e) => {
      e.preventDefault();
      if (!beaconEditing) return;
      const el = beaconEditing;
      el.dataset.beacon = beaconIdInput.value.trim();
      el.title = el.dataset.beacon ? `Beacon: ${el.dataset.beacon}` : "Beacon";
      try { await postForm("/admin/item/update", payload(el)); }
      catch (err) { hint("Couldn't save beacon"); }
      closeBeacon();
    });
    document.getElementById("beacon-cancel").addEventListener("click", closeBeacon);
    beaconDialog.addEventListener("cancel", (e) => { e.preventDefault(); closeBeacon(); });
    document.getElementById("beacon-delete").addEventListener("click", async () => {
      if (!beaconEditing) return;
      const el = beaconEditing;
      try { await postForm("/admin/item/delete", { id: el.dataset.id }); el.remove(); }
      catch (err) { hint("Couldn't delete"); }
      closeBeacon();
    });

    // Settings dialog.
    const settings = document.getElementById("settings");
    document.getElementById("open-settings").addEventListener("click", () => settings.showModal());
    document.getElementById("close-settings").addEventListener("click", () => settings.close());

    // Delete via the per-item button: clicking × swaps it for an inline ✓ / ✗
    // confirm in the same corner, instead of a native browser dialog.
    world.addEventListener("click", (e) => {
      if (!canEdit()) return;
      const del = e.target.closest(".item-del");
      if (del) { askDelete(del.closest(".item"), del); }
    });

    let delConfirm = null; // the currently open confirm widget, if any
    function dismissDelete() {
      if (!delConfirm) return;
      delConfirm.del.style.display = "";
      delConfirm.box.remove();
      document.removeEventListener("pointerdown", onOutside, true);
      delConfirm = null;
    }
    function onOutside(e) {
      if (delConfirm && !e.target.closest(".item-del-confirm")) dismissDelete();
    }
    async function doDelete(el) {
      try {
        await postForm("/admin/item/delete", { id: el.dataset.id });
        el.remove();
        refreshAdaptive();
      } catch (err) { hint("Couldn't delete"); }
    }
    function askDelete(el, del) {
      dismissDelete(); // only one open at a time
      del.style.display = "none";
      const box = document.createElement("div");
      box.className = "item-del-confirm";
      box.innerHTML =
        `<button type="button" class="yes" title="Delete">✓</button>` +
        `<button type="button" class="no" title="Keep">✗</button>`;
      box.querySelector(".yes").addEventListener("click", () => { const t = el; dismissDelete(); doDelete(t); });
      box.querySelector(".no").addEventListener("click", dismissDelete);
      el.appendChild(box);
      delConfirm = { box, del };
      // Defer so the click that opened it doesn't immediately dismiss it.
      setTimeout(() => document.addEventListener("pointerdown", onOutside, true), 0);
    }

    // Double-click a text box to edit its Markdown, or an image to set options.
    world.addEventListener("dblclick", (e) => {
      if (!canEdit()) return;
      const txt = e.target.closest(".item-text");
      if (txt) { e.preventDefault(); openEditor(txt); return; }
      const bea = e.target.closest(".item-beacon");
      if (bea) { e.preventDefault(); openBeacon(bea); return; }
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

    // ---- Image cropper ----
    // Non-destructive: cropping renders the chosen region of the *original* to a
    // canvas, uploads it as a new image used for display, and remembers the
    // original URL + rect so the owner can re-crop (or reset) later.
    const cropDialog = document.getElementById("crop-dialog");
    const cropStage = document.getElementById("crop-stage");
    const cropImg = document.getElementById("crop-img");
    const cropBox = document.getElementById("crop-box");
    let cropItem = null;                  // image item being cropped
    let cropDim = { w: 0, h: 0 };         // fitted display size of the original, px
    let cropRect = { x: 0, y: 0, w: 0, h: 0 }; // selection within the stage, px
    let cropLock = null;                  // null = free, 1 = square (pixel) aspect

    const clamp = (v, lo, hi) => Math.min(hi, Math.max(lo, v));
    function srcOf(el) { return el.dataset.original || el.querySelector("img").getAttribute("src") || ""; }
    function parseCrop(s) {
      if (!s) return null;
      const p = s.split(",").map(Number);
      return p.length === 4 && p.every((n) => !isNaN(n)) ? { x: p[0], y: p[1], w: p[2], h: p[3] } : null;
    }
    function drawCropBox() {
      cropBox.style.left = cropRect.x + "px";
      cropBox.style.top = cropRect.y + "px";
      cropBox.style.width = cropRect.w + "px";
      cropBox.style.height = cropRect.h + "px";
    }

    document.getElementById("image-crop").addEventListener("click", () => {
      if (!optsTarget) return;
      const el = optsTarget;
      optsDialog.close(); optsTarget = null;
      openCrop(el);
    });

    function openCrop(el) {
      cropItem = el;
      cropLock = null;
      const src = srcOf(el);
      const probe = new Image();
      probe.onload = () => {
        const maxW = Math.min(window.innerWidth * 0.8, 900);
        const maxH = window.innerHeight * 0.65;
        const scale = Math.min(maxW / probe.naturalWidth, maxH / probe.naturalHeight, 1);
        cropDim = { w: Math.round(probe.naturalWidth * scale), h: Math.round(probe.naturalHeight * scale) };
        cropStage.style.width = cropDim.w + "px";
        cropStage.style.height = cropDim.h + "px";
        cropImg.src = src;
        const c = parseCrop(el.dataset.crop);
        cropRect = c
          ? { x: c.x * cropDim.w, y: c.y * cropDim.h, w: c.w * cropDim.w, h: c.h * cropDim.h }
          : { x: 0, y: 0, w: cropDim.w, h: cropDim.h };
        drawCropBox();
        cropDialog.showModal();
      };
      probe.onerror = () => hint("Couldn't load image");
      probe.src = src;
    }

    // Drag the box to move it; drag a corner handle to resize. All maths is in
    // stage pixels and clamped to the image bounds.
    let cropMode = null, cropStart = null, cropCorner = null;
    cropBox.addEventListener("pointerdown", (e) => {
      e.preventDefault();
      const handle = e.target.closest(".crop-h");
      cropMode = handle ? "resize" : "move";
      cropCorner = handle ? handle.dataset.c : null;
      cropStart = { px: e.clientX, py: e.clientY, rect: { ...cropRect } };
      cropBox.setPointerCapture(e.pointerId);
    });
    cropBox.addEventListener("pointermove", (e) => {
      if (!cropMode) return;
      const dx = e.clientX - cropStart.px, dy = e.clientY - cropStart.py;
      if (cropMode === "move") {
        cropRect.x = clamp(cropStart.rect.x + dx, 0, cropDim.w - cropRect.w);
        cropRect.y = clamp(cropStart.rect.y + dy, 0, cropDim.h - cropRect.h);
      } else {
        resizeCrop(dx, dy);
      }
      drawCropBox();
    });
    const endCrop = () => { cropMode = null; };
    cropBox.addEventListener("pointerup", endCrop);
    cropBox.addEventListener("pointercancel", endCrop);

    function resizeCrop(dx, dy) {
      const r = cropStart.rect, MIN = 24;
      let left = r.x, top = r.y, right = r.x + r.w, bottom = r.y + r.h;
      if (cropCorner.includes("l")) left = clamp(r.x + dx, 0, right - MIN);
      if (cropCorner.includes("r")) right = clamp(r.x + r.w + dx, left + MIN, cropDim.w);
      if (cropCorner.includes("t")) top = clamp(r.y + dy, 0, bottom - MIN);
      if (cropCorner.includes("b")) bottom = clamp(r.y + r.h + dy, top + MIN, cropDim.h);
      let w = right - left, h = bottom - top;
      if (cropLock) {
        const side = Math.min(w, h); // keep a pixel-square selection
        if (cropCorner.includes("l")) left = right - side; else right = left + side;
        if (cropCorner.includes("t")) top = bottom - side; else bottom = top + side;
        left = clamp(left, 0, cropDim.w - side); top = clamp(top, 0, cropDim.h - side);
        w = h = side;
      }
      cropRect = { x: left, y: top, w, h };
    }

    document.getElementById("crop-square").addEventListener("click", () => {
      cropLock = 1;
      const side = Math.min(cropRect.w, cropRect.h);
      const cx = cropRect.x + cropRect.w / 2, cy = cropRect.y + cropRect.h / 2;
      cropRect = {
        x: clamp(cx - side / 2, 0, cropDim.w - side),
        y: clamp(cy - side / 2, 0, cropDim.h - side),
        w: side, h: side,
      };
      drawCropBox();
    });
    document.getElementById("crop-free").addEventListener("click", () => { cropLock = null; });

    function closeCrop() { cropDialog.close(); cropItem = null; }
    document.getElementById("crop-cancel").addEventListener("click", closeCrop);
    cropDialog.addEventListener("cancel", (e) => { e.preventDefault(); closeCrop(); });

    document.getElementById("crop-reset").addEventListener("click", async () => {
      if (!cropItem) return;
      const el = cropItem;
      const orig = el.dataset.original;
      if (orig) {
        const img = el.querySelector("img");
        el.dataset.md = orig;
        el.dataset.original = ""; el.dataset.crop = "";
        img.addEventListener("load", refreshAdaptive, { once: true });
        img.src = orig;
        try { await postForm("/admin/item/update", payload(el)); }
        catch (err) { hint("Couldn't reset"); }
        refreshAdaptive();
      }
      closeCrop();
    });

    document.getElementById("crop-apply").addEventListener("click", async () => {
      if (!cropItem) return;
      const el = cropItem;
      const src = srcOf(el);
      const fr = {
        x: cropRect.x / cropDim.w, y: cropRect.y / cropDim.h,
        w: cropRect.w / cropDim.w, h: cropRect.h / cropDim.h,
      };
      hint("Cropping…");
      try {
        const { blob, name } = await cropToBlob(src, fr);
        const fd = new FormData();
        fd.set("csrf", CSRF);
        fd.set("file", blob, name);
        const up = await fetch("/admin/upload", { method: "POST", body: fd });
        if (!up.ok) throw new Error(await up.text());
        const { url } = await up.json();
        const img = el.querySelector("img");
        if (!el.dataset.original) el.dataset.original = img.getAttribute("src");
        el.dataset.md = url;
        el.dataset.crop = `${fr.x},${fr.y},${fr.w},${fr.h}`;
        img.addEventListener("load", refreshAdaptive, { once: true });
        img.src = url;
        await postForm("/admin/item/update", payload(el));
        hideHint();
        refreshAdaptive();
      } catch (err) { hint("Crop failed"); }
      closeCrop();
    });

    // cropToBlob draws the fractional rect of the source image to a canvas at the
    // source's own resolution. Formats with transparency stay PNG; others become
    // JPEG so large photo crops keep well under the upload limit.
    function cropToBlob(src, fr) {
      return new Promise((resolve, reject) => {
        const img = new Image();
        img.onload = () => {
          const nW = img.naturalWidth, nH = img.naturalHeight;
          const sx = Math.round(fr.x * nW), sy = Math.round(fr.y * nH);
          const sw = Math.max(1, Math.round(fr.w * nW)), sh = Math.max(1, Math.round(fr.h * nH));
          const cv = document.createElement("canvas");
          cv.width = sw; cv.height = sh;
          cv.getContext("2d").drawImage(img, sx, sy, sw, sh, 0, 0, sw, sh);
          const lower = src.toLowerCase();
          const png = lower.endsWith(".png") || lower.endsWith(".gif") || lower.endsWith(".webp");
          cv.toBlob(
            (b) => b ? resolve({ blob: b, name: png ? "crop.png" : "crop.jpg" }) : reject(new Error("toBlob failed")),
            png ? "image/png" : "image/jpeg",
            png ? undefined : 0.92
          );
        };
        img.onerror = () => reject(new Error("load failed"));
        img.src = src;
      });
    }

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
  //
  // Over light backgrounds the text goes near-black: lDarkMax caps its lightness
  // low and sDarkMax trims its saturation, so it reads as a tinted black rather
  // than a mid-tone "normal" colour. (Over dark backgrounds it still goes light.)
  const AC = { hueRotate: 180, fallbackHue: 210, sFloor: 0.5, sBoost: 0.22, lShift: 0.42, lLightMax: 0.95, lDarkMax: 0.2, lDarkMin: 0.08, sDarkMax: 0.55, shadow: "0 1px 2px rgba(0,0,0,0.6)" };

  // adaptiveColor picks a readable colour that contrasts background bg.
  function adaptiveColor(bg) {
    const { h, s, l } = rgbToHsl(bg.r, bg.g, bg.b);
    // A coloured background gets its complement; a hueless one gets a single
    // fixed hue (so neutral areas don't cycle into a rainbow across the text).
    const hue = s > 0.12 ? (h + AC.hueRotate) % 360 : AC.fallbackHue;
    let sat = Math.max(AC.sFloor, Math.min(0.95, s + AC.sBoost));
    // Decide direction by HSL lightness (how light it *looks*), not perceived
    // luminance, so light-blue/light-yellow backgrounds don't fool the shift and
    // wash the text out.
    let light;
    if (l < 0.5) {
      light = Math.min(AC.lLightMax, l + AC.lShift); // over dark: go light
    } else {
      // Over light: go to a dark, only-lightly-tinted near-black.
      light = Math.max(AC.lDarkMin, Math.min(AC.lDarkMax, l - AC.lShift));
      sat = Math.min(sat, AC.sDarkMax);
    }
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
