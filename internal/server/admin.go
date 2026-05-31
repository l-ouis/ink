package server

import (
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"ink/internal/config"
	"ink/internal/content"
	"ink/internal/media"
	"ink/internal/render"
)

// maxUploadBytes caps the size of a single uploaded image.
const maxUploadBytes = 10 << 20 // 10 MiB

func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	if s.auth.Authed(r) {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
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
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
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

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	pages, err := s.store.List(false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Build font options with a CSS-typed stack so each <option> can preview in
	// its own typeface (a plain string is rejected by the style-attr sanitizer).
	type fontOption struct {
		Key, Name string
		Stack     template.CSS
	}
	fonts := make([]fontOption, len(config.Fonts))
	for i, f := range config.Fonts {
		fonts[i] = fontOption{Key: f.Key, Name: f.Name, Stack: template.CSS(f.Stack)}
	}
	s.render(w, r, "admin", map[string]any{
		"Pages":       pages,
		"HeaderTitle": s.cfg.HeaderTitle,
		"Fonts":       fonts,
		"FontKey":     s.cfg.FontKey(),
		"HasFavicon":  s.cfg.HasFavicon(),
		"CSRF":        s.auth.CSRFToken(r),
	})
}

func (s *Server) handleEdit(w http.ResponseWriter, r *http.Request) {
	slug := r.URL.Query().Get("slug")

	var doc content.Document
	isNew := true
	// An explicit ?edit=1 lets the owner open the home page (empty slug).
	if slug != "" || r.URL.Query().Get("edit") != "" {
		p, err := s.store.Get(slug)
		if err != nil {
			s.notFound(w, r)
			return
		}
		isNew = false
		date := ""
		if !p.Date.IsZero() {
			date = p.Date.Format("2006-01-02")
		}
		doc = content.Document{
			Slug: p.Slug, Title: p.Title,
			Date: date, Draft: p.Draft, Summary: p.Summary, Body: p.Body,
		}
	} else {
		doc = content.Document{Date: time.Now().Format("2006-01-02"), Draft: true}
	}
	s.renderEdit(w, r, &doc, isNew, "")
}

func (s *Server) renderEdit(w http.ResponseWriter, r *http.Request, doc *content.Document, isNew bool, errMsg string) {
	status := http.StatusOK
	if errMsg != "" {
		status = http.StatusBadRequest
	}
	s.renderStatus(w, r, status, "edit", map[string]any{
		"Doc":   doc,
		"IsNew": isNew,
		"Error": errMsg,
		"Saved": errMsg == "" && r.URL.Query().Get("saved") != "",
		"CSRF":  s.auth.CSRFToken(r),
	})
}

func (s *Server) handleSave(w http.ResponseWriter, r *http.Request) {
	if !s.auth.CheckCSRF(r) {
		http.Error(w, "invalid csrf token", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	origSlug := r.FormValue("orig_slug")
	doc := &content.Document{
		Slug:    strings.Trim(strings.TrimSpace(r.FormValue("slug")), "/"),
		Title:   strings.TrimSpace(r.FormValue("title")),
		Date:    strings.TrimSpace(r.FormValue("date")),
		Draft:   r.FormValue("draft") == "on",
		Summary: strings.TrimSpace(r.FormValue("summary")),
		Body:    r.FormValue("body"),
	}

	// isNew is carried across the request via the hidden was_new field, since an
	// empty orig_slug is also the (existing) home page.
	isNew := r.FormValue("was_new") == "1"
	if !content.ValidSlug(doc.Slug) {
		s.renderEdit(w, r, doc, isNew, "Each path segment must be lowercase letters, numbers and single hyphens, e.g. projects/ink.")
		return
	}
	if err := s.store.Save(doc); err != nil {
		s.renderEdit(w, r, doc, isNew, "Could not save: "+err.Error())
		return
	}
	// Handle rename: remove the old file once the new one is written.
	if !isNew && origSlug != doc.Slug && content.ValidSlug(origSlug) {
		_ = s.store.Delete(origSlug)
	}
	// Stay in the editor after saving (Post/Redirect/Get avoids resubmit on refresh).
	http.Redirect(w, r, "/admin/edit?edit=1&saved=1&slug="+url.QueryEscape(doc.Slug), http.StatusSeeOther)
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	if !s.auth.CheckCSRF(r) {
		http.Error(w, "invalid csrf token", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := s.store.Delete(strings.Trim(r.FormValue("slug"), "/")); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
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
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleGallery(w http.ResponseWriter, r *http.Request) {
	images, err := s.media.List()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, r, "gallery", map[string]any{
		"Images": images,
		"CSRF":   s.auth.CSRFToken(r),
	})
}

// handleUpload accepts a single multipart image. With a "redirect" form field
// (the no-JS gallery form) it redirects back; otherwise it returns JSON
// {"url","name"} for the editor's fetch-based upload.
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

	if redirect := r.FormValue("redirect"); isLocalRedirect(redirect) {
		http.Redirect(w, r, redirect, http.StatusSeeOther)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"url": img.URL, "name": img.Name})
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
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
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
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleDeleteUpload(w http.ResponseWriter, r *http.Request) {
	if !s.auth.CheckCSRF(r) {
		http.Error(w, "invalid csrf token", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := s.media.Delete(r.FormValue("name")); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/admin/gallery", http.StatusSeeOther)
}

// isLocalRedirect reports whether dst is a safe same-site redirect target,
// guarding against open redirects (e.g. "//evil.com").
func isLocalRedirect(dst string) bool {
	return strings.HasPrefix(dst, "/") && !strings.HasPrefix(dst, "//")
}

func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	out, err := render.Render(".md", []byte(r.FormValue("body")))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(out))
}
