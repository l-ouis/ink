// Live Markdown preview: debounce edits and render server-side so the preview
// matches exactly what the published page will look like.
(function () {
  const body = document.getElementById("body");
  const preview = document.getElementById("preview");
  if (!body || !preview) return;

  let timer = null;
  let inFlight = false;
  let pending = false;

  async function render() {
    if (inFlight) {
      pending = true;
      return;
    }
    inFlight = true;
    try {
      const res = await fetch("/admin/preview", {
        method: "POST",
        headers: { "Content-Type": "application/x-www-form-urlencoded" },
        body: new URLSearchParams({ body: body.value }),
      });
      if (res.ok) {
        preview.innerHTML = await res.text();
        enhanceImages();
      }
    } catch (e) {
      /* network hiccup: keep the last good preview */
    } finally {
      inFlight = false;
      if (pending) {
        pending = false;
        render();
      }
    }
  }

  body.addEventListener("input", function () {
    clearTimeout(timer);
    timer = setTimeout(render, 250);
  });

  render();

  // --- Image uploads: button, paste and drag-and-drop. ---

  const form = body.closest("form");
  const csrf = form ? (form.querySelector('input[name="csrf"]') || {}).value : "";
  const fileInput = document.getElementById("image-input");
  const imageBtn = document.getElementById("image-btn");

  function insertAtCursor(text) {
    const start = body.selectionStart;
    const end = body.selectionEnd;
    body.value = body.value.slice(0, start) + text + body.value.slice(end);
    const pos = start + text.length;
    body.selectionStart = body.selectionEnd = pos;
    body.focus();
    body.dispatchEvent(new Event("input")); // refresh the preview
  }

  function replaceFirst(find, replaceWith) {
    const i = body.value.indexOf(find);
    if (i < 0) return;
    body.value = body.value.slice(0, i) + replaceWith + body.value.slice(i + find.length);
    body.dispatchEvent(new Event("input"));
  }

  async function uploadOne(file) {
    const label = file.name || "image";
    const placeholder = "![uploading " + label + "…]()";
    insertAtCursor(placeholder + "\n");
    try {
      const fd = new FormData();
      fd.append("csrf", csrf);
      fd.append("file", file, file.name || "pasted.png");
      const res = await fetch("/admin/upload", { method: "POST", body: fd });
      if (!res.ok) throw new Error((await res.text()).trim() || res.statusText);
      const { url } = await res.json();
      replaceFirst(placeholder, "![](" + url + ")");
    } catch (e) {
      replaceFirst(placeholder, "");
      alert("Image upload failed: " + e.message);
    }
  }

  // Upload sequentially so placeholders never collide.
  async function uploadAll(files) {
    for (const f of files) {
      if (f && f.type && f.type.indexOf("image/") === 0) await uploadOne(f);
    }
  }

  if (imageBtn && fileInput) {
    imageBtn.addEventListener("click", () => fileInput.click());
    fileInput.addEventListener("change", () => {
      uploadAll(Array.from(fileInput.files));
      fileInput.value = "";
    });
  }

  body.addEventListener("paste", (e) => {
    const items = e.clipboardData && e.clipboardData.files;
    if (items && items.length) {
      const imgs = Array.from(items).filter((f) => f.type.indexOf("image/") === 0);
      if (imgs.length) {
        e.preventDefault();
        uploadAll(imgs);
      }
    }
  });

  body.addEventListener("dragover", (e) => {
    if (e.dataTransfer && Array.from(e.dataTransfer.items || []).some((i) => i.kind === "file")) {
      e.preventDefault();
    }
  });
  body.addEventListener("drop", (e) => {
    const files = e.dataTransfer && e.dataTransfer.files;
    if (files && files.length) {
      e.preventDefault();
      uploadAll(Array.from(files));
    }
  });

  // --- Image resizing: drag an image's corner in the preview to set the width
  // it displays at. The size is written back into the source as an <img width=N>
  // (markdown ![](url) is upgraded to HTML only when first resized). ---

  // attrOf reads an HTML attribute value from a raw <img …> tag string.
  function attrOf(tag, name) {
    const m = tag.match(new RegExp(name + '\\s*=\\s*"([^"]*)"|' + name + "\\s*=\\s*'([^']*)'", "i"));
    return m ? (m[1] !== undefined ? m[1] : m[2]) : "";
  }

  // tokenizeImages finds every inline image in the source (markdown or <img>),
  // in document order, with its byte span, URL and alt text.
  const IMG_TOKEN = /!\[([^\]]*)\]\(\s*([^)\s]+)(?:\s+(?:"[^"]*"|'[^']*'))?\s*\)|<img\b[^>]*>/gi;
  function tokenizeImages(srcText) {
    const tokens = [];
    let m;
    IMG_TOKEN.lastIndex = 0;
    while ((m = IMG_TOKEN.exec(srcText))) {
      const raw = m[0];
      let url, alt;
      if (raw[0] === "!") {
        // Markdown alt is plain text; make it attribute-ready.
        alt = escAttr(m[1] || "");
        url = m[2];
      } else {
        // <img> alt is already attribute-escaped in the source; keep verbatim.
        url = attrOf(raw, "src");
        alt = attrOf(raw, "alt");
      }
      tokens.push({ start: m.index, end: m.index + raw.length, url: url, alt: alt });
    }
    return tokens;
  }

  function escAttr(s) {
    return String(s).replace(/&/g, "&amp;").replace(/"/g, "&quot;");
  }

  // setImageWidth rewrites the source token matching previewImg to carry width.
  // The preview img is paired to a source token by URL plus its occurrence index
  // among same-URL images, so unrelated images don't throw off the mapping.
  function setImageWidth(previewImg, width) {
    const src = previewImg.getAttribute("src");
    const imgs = Array.from(preview.querySelectorAll("img"));
    let occurrence = 0;
    for (const im of imgs) {
      if (im === previewImg) break;
      if (im.getAttribute("src") === src) occurrence++;
    }
    const token = tokenizeImages(body.value).filter((t) => t.url === src)[occurrence];
    if (!token) return;
    const replacement = '<img src="' + escAttr(src) + '" alt="' + token.alt + '" width="' + width + '">';
    body.value = body.value.slice(0, token.start) + replacement + body.value.slice(token.end);
    body.dispatchEvent(new Event("input"));
  }

  function startResize(e, img, wrap) {
    e.preventDefault();
    const startX = e.clientX;
    const startW = img.getBoundingClientRect().width;
    const maxW = preview.clientWidth - 8;
    const badge = document.createElement("span");
    badge.className = "imgsize";
    wrap.appendChild(badge);
    document.body.style.userSelect = "none";

    function move(ev) {
      let w = Math.round(startW + (ev.clientX - startX));
      w = Math.max(40, Math.min(w, maxW));
      img.style.width = w + "px";
      img.style.height = "auto";
      badge.textContent = w + " px";
    }
    function up() {
      document.removeEventListener("mousemove", move);
      document.removeEventListener("mouseup", up);
      document.body.style.userSelect = "";
      const w = parseInt(img.style.width, 10) || Math.round(img.getBoundingClientRect().width);
      setImageWidth(img, w);
    }
    document.addEventListener("mousemove", move);
    document.addEventListener("mouseup", up);
  }

  // enhanceImages wraps each preview image and attaches a resize handle. The
  // preview is fully replaced on every render, so images are always fresh here.
  function enhanceImages() {
    Array.from(preview.querySelectorAll("img")).forEach((img) => {
      const wrap = document.createElement("span");
      wrap.className = "imgwrap";
      img.parentNode.insertBefore(wrap, img);
      wrap.appendChild(img);
      const handle = document.createElement("span");
      handle.className = "imghandle";
      handle.title = "Drag to resize";
      wrap.appendChild(handle);
      handle.addEventListener("mousedown", (e) => startResize(e, img, wrap));
    });
  }
})();
