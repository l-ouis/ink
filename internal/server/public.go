package server

import (
	"net/http"
	"strings"

	"ink/internal/content"
	"ink/internal/render"
)

// serveUpload serves a stored image. Requests for a directory (a path ending in
// "/") are refused so the uploads folder can't be listed.
func (s *Server) serveUpload(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/") {
		s.notFound(w, r)
		return
	}
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

// handlePage serves any page by its path. The path wildcard captures everything
// after the leading slash, so "" is the home page and "projects/ink" is a
// nested page. A trailing slash is tolerated by redirecting to the clean path.
func (s *Server) handlePage(w http.ResponseWriter, r *http.Request) {
	slug := strings.Trim(r.PathValue("path"), "/")
	if cleaned := "/" + slug; cleaned != r.URL.Path && slug != "" {
		http.Redirect(w, r, cleaned, http.StatusMovedPermanently)
		return
	}
	if !content.ValidSlug(slug) {
		s.notFound(w, r)
		return
	}
	p, err := s.store.Get(slug)
	if err != nil {
		s.notFound(w, r)
		return
	}
	if p.Draft && !s.auth.Authed(r) {
		s.notFound(w, r)
		return
	}
	body, err := render.Render(p.Ext, []byte(p.Body))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, r, "page", map[string]any{"Page": p, "Body": body})
}
