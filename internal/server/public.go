package server

import (
	"html/template"
	"net/http"
	"strings"

	"ink/internal/canvas"
	"ink/internal/config"
	"ink/internal/render"
)

// serveUpload serves a stored image. Requests for a directory (a path ending in
// "/") are refused so the uploads folder can't be listed.
func (s *Server) serveUpload(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/") {
		s.notFound(w, r)
		return
	}
	// Uploads get unique, never-overwritten names, so they are immutable: let
	// browsers cache them indefinitely instead of revalidating on every load.
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	s.uploads.ServeHTTP(w, r)
}

// serveFavicon serves the uploaded favicon, or 404s if none is set.
func (s *Server) serveFavicon(w http.ResponseWriter, r *http.Request) {
	path := s.faviconFile()
	if path == "" {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, path)
}

// viewItem is an item prepared for the template: geometry plus the display HTML
// (rendered Markdown for text, an <img> is built client-side for images) and
// the raw source so the editor can reopen it.
type viewItem struct {
	*canvas.Item
	HTML template.HTML
}

// handleCanvas serves the single-page infinite canvas with every item rendered
// in place. The admin toolbar and editing controls are activated client-side
// when the request carries a valid session.
func (s *Server) handleCanvas(w http.ResponseWriter, r *http.Request) {
	items := s.canvas.All()
	views := make([]viewItem, 0, len(items))
	for _, it := range items {
		v := viewItem{Item: it}
		if it.Type == canvas.TypeText {
			html, err := render.Render(".md", []byte(it.Content))
			if err != nil {
				html = template.HTML("")
			}
			v.HTML = html
		}
		views = append(views, v)
	}

	// Settings shown in the admin panel. A CSS-typed font stack lets each
	// <option> preview in its own typeface.
	type fontOption struct {
		Key, Name string
		Stack     template.CSS
	}
	fonts := make([]fontOption, len(config.Fonts))
	for i, f := range config.Fonts {
		fonts[i] = fontOption{Key: f.Key, Name: f.Name, Stack: template.CSS(f.Stack)}
	}

	s.render(w, r, "canvas", map[string]any{
		"Items":       views,
		"OriginViewX": s.cfg.OriginViewX,
		"OriginViewY": s.cfg.OriginViewY,
		"CSRF":        s.auth.CSRFToken(r),
		"HeaderTitle": s.cfg.HeaderTitle,
		"Fonts":       fonts,
		"FontKey":     s.cfg.FontKey(),
		"HasFavicon":  s.cfg.HasFavicon(),
	})
}
