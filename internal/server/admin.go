package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"ink/internal/canvas"
	"ink/internal/config"
	"ink/internal/media"
	"ink/internal/render"
)

// maxUploadBytes caps the size of a single uploaded image.
const maxUploadBytes = 10 << 20 // 10 MiB

func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	if s.auth.Authed(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.render(w, r, "login", map[string]any{"NoPassword": !s.cfg.HasPassword()})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.auth.Throttle() {
		s.renderStatus(w, r, http.StatusTooManyRequests, "login", map[string]any{
			"Error": "Too many attempts. Try again in a few minutes.",
		})
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if s.cfg.CheckPassword(r.FormValue("password")) {
		s.auth.Reset()
		s.auth.Issue(w)
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.auth.Fail()
	s.renderStatus(w, r, http.StatusUnauthorized, "login", map[string]any{
		"Error":      "Incorrect password.",
		"NoPassword": !s.cfg.HasPassword(),
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if !s.auth.CheckCSRF(r) {
		http.Error(w, "invalid csrf token", http.StatusForbidden)
		return
	}
	s.auth.Clear(w)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	if !s.auth.CheckCSRF(r) {
		http.Error(w, "invalid csrf token", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	s.cfg.HeaderTitle = strings.TrimSpace(r.FormValue("header_title"))
	s.cfg.Font = config.NormalizeFont(r.FormValue("font"))
	if err := s.cfg.Save(s.cfgPath); err != nil {
		http.Error(w, "could not save settings: "+err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// ---- Canvas item API -------------------------------------------------------

// handleItemAdd creates a new text or image item and returns it as JSON,
// including the server-rendered HTML for text items and the assigned id/z.
func (s *Server) handleItemAdd(w http.ResponseWriter, r *http.Request) {
	if !s.auth.CheckCSRF(r) {
		http.Error(w, "invalid csrf token", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	it := canvas.Item{
		Type:     r.FormValue("type"),
		X:        formFloat(r, "x", 0),
		Y:        formFloat(r, "y", 0),
		W:        formFloat(r, "w", 320),
		H:        formFloat(r, "h", 0),
		Content:  r.FormValue("content"),
		Layer:    r.FormValue("layer"),
		Adaptive: r.FormValue("adaptive") == "1",
		Beacon:   strings.TrimSpace(r.FormValue("beacon")),
	}
	stored, err := s.canvas.Add(it)
	if errors.Is(err, canvas.ErrType) {
		http.Error(w, "unknown item type", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, "could not save item", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"id":   stored.ID,
		"z":    stored.Z,
		"html": s.renderItem(stored),
	})
}

// handleItemUpdate changes an item's position, size and/or content, returning
// the re-rendered HTML.
func (s *Server) handleItemUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.auth.CheckCSRF(r) {
		http.Error(w, "invalid csrf token", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	stored, err := s.canvas.Update(r.FormValue("id"), canvas.Item{
		X: formFloat(r, "x", 0), Y: formFloat(r, "y", 0),
		W: formFloat(r, "w", 0), H: formFloat(r, "h", 0),
		Z:        int(formFloat(r, "z", 0)),
		Content:  r.FormValue("content"),
		Layer:    r.FormValue("layer"),
		Adaptive: r.FormValue("adaptive") == "1",
		Beacon:   strings.TrimSpace(r.FormValue("beacon")),
		Original: strings.TrimSpace(r.FormValue("original")),
		Crop:     strings.TrimSpace(r.FormValue("crop")),
		ViewDX:   formFloat(r, "viewdx", 0),
		ViewDY:   formFloat(r, "viewdy", 0),
	})
	if errors.Is(err, canvas.ErrNotFound) {
		http.Error(w, "no such item", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "could not save item", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"html": s.renderItem(stored)})
}

// handleOriginView persists the origin's view-point offset (where the corner
// label and [text](origin) links land).
func (s *Server) handleOriginView(w http.ResponseWriter, r *http.Request) {
	if !s.auth.CheckCSRF(r) {
		http.Error(w, "invalid csrf token", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	s.cfg.OriginViewX = formFloat(r, "vx", 0)
	s.cfg.OriginViewY = formFloat(r, "vy", 0)
	if err := s.cfg.Save(s.cfgPath); err != nil {
		http.Error(w, "could not save", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleItemDelete removes an item.
func (s *Server) handleItemDelete(w http.ResponseWriter, r *http.Request) {
	if !s.auth.CheckCSRF(r) {
		http.Error(w, "invalid csrf token", http.StatusForbidden)
		return
	}
	if err := s.canvas.Delete(r.FormValue("id")); err != nil {
		http.Error(w, "could not delete item", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// renderItem returns the display HTML for a text item (rendered Markdown), or
// the empty string for an image (the client builds the <img> from its URL).
func (s *Server) renderItem(it *canvas.Item) string {
	if it.Type != canvas.TypeText {
		return ""
	}
	html, err := render.Render(".md", []byte(it.Content))
	if err != nil {
		return ""
	}
	return string(html)
}

// ---- Uploads ---------------------------------------------------------------

// handleUpload accepts a single multipart image and returns JSON {"url","name"}
// for the canvas editor's fetch-based upload.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		http.Error(w, "Image is too large (max 10 MB).", http.StatusRequestEntityTooLarge)
		return
	}
	if !s.auth.CheckCSRF(r) {
		http.Error(w, "invalid csrf token", http.StatusForbidden)
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "No file received.", http.StatusBadRequest)
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "Could not read upload.", http.StatusBadRequest)
		return
	}
	img, err := s.media.Save(hdr.Filename, data)
	if errors.Is(err, media.ErrUnsupported) {
		http.Error(w, "Only PNG, JPEG, GIF and WebP images are allowed.", http.StatusUnsupportedMediaType)
		return
	}
	if err != nil {
		http.Error(w, "Could not save image.", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"url": img.URL, "name": img.Name})
}

// handleUploadFavicon stores an uploaded image as the site favicon (alongside
// the config file) and records its extension in config.
func (s *Server) handleUploadFavicon(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		http.Error(w, "Image is too large (max 10 MB).", http.StatusRequestEntityTooLarge)
		return
	}
	if !s.auth.CheckCSRF(r) {
		http.Error(w, "invalid csrf token", http.StatusForbidden)
		return
	}
	file, _, err := r.FormFile("favicon")
	if err != nil {
		http.Error(w, "No file received.", http.StatusBadRequest)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "Could not read upload.", http.StatusBadRequest)
		return
	}
	ext, ok := media.DetectImageExt(data)
	if !ok {
		http.Error(w, "Only PNG, JPEG, GIF and WebP images are allowed.", http.StatusUnsupportedMediaType)
		return
	}
	if err := s.writeFavicon(ext, data); err != nil {
		http.Error(w, "Could not save favicon.", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleDeleteFavicon(w http.ResponseWriter, r *http.Request) {
	if !s.auth.CheckCSRF(r) {
		http.Error(w, "invalid csrf token", http.StatusForbidden)
		return
	}
	if err := s.removeFavicon(); err != nil {
		http.Error(w, "Could not remove favicon.", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// ---- helpers ---------------------------------------------------------------

// formFloat reads a float form field, falling back to def when absent or
// unparseable.
func formFloat(r *http.Request, name string, def float64) float64 {
	v := strings.TrimSpace(r.FormValue(name))
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
