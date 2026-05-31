// Package server wires the HTTP routes, templates and handlers together.
package server

import (
	"bytes"
	"html/template"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"ink/internal/auth"
	"ink/internal/config"
	"ink/internal/content"
	"ink/internal/media"
)

// Server holds the dependencies shared by all handlers.
type Server struct {
	cfg     *config.Config
	cfgPath string
	dataDir string // where the favicon is stored, alongside the config file
	store   *content.Store
	media   *media.Store
	auth    *auth.Manager
	tmpl    map[string]*template.Template
	static  http.Handler
	uploads http.Handler
	mux     *http.ServeMux
}

// New builds a Server. cfgPath is where config changes made from the admin UI
// are persisted. tmplFS must contain templates/*.html; staticFS is served at
// /static/ and should be rooted at the static directory. Uploaded images are
// served from the media store's directory on disk.
func New(cfg *config.Config, cfgPath string, store *content.Store, ms *media.Store, am *auth.Manager, tmplFS, staticFS fs.FS) (*Server, error) {
	s := &Server{cfg: cfg, cfgPath: cfgPath, dataDir: filepath.Dir(cfgPath), store: store, media: ms, auth: am}
	if err := s.parseTemplates(tmplFS); err != nil {
		return nil, err
	}
	s.static = http.StripPrefix("/static/", http.FileServer(http.FS(staticFS)))
	s.uploads = http.StripPrefix("/uploads/", http.FileServer(http.Dir(ms.Dir())))
	s.routes()
	return s, nil
}

// Handler returns the configured HTTP handler, wrapped with security headers.
func (s *Server) Handler() http.Handler { return securityHeaders(s.mux) }

// securityHeaders sets conservative defaults on every response. The CSP allows
// inline styles and event handlers (the templates use a few) and same-origin
// resources only, so injected markup can't pull in external scripts.
func securityHeaders(next http.Handler) http.Handler {
	const csp = "default-src 'self'; img-src 'self' data:; " +
		"style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; " +
		"object-src 'none'; base-uri 'self'; frame-ancestors 'self'"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Content-Security-Policy", csp)
		next.ServeHTTP(w, r)
	})
}

// faviconFile returns the on-disk path of the uploaded favicon, or "" if none.
func (s *Server) faviconFile() string {
	if !s.cfg.HasFavicon() {
		return ""
	}
	return filepath.Join(s.dataDir, "favicon"+s.cfg.FaviconExt)
}

// writeFavicon stores data as the favicon with the given extension and persists
// the choice, removing any previously stored favicon of a different type.
func (s *Server) writeFavicon(ext string, data []byte) error {
	if old := s.faviconFile(); old != "" && s.cfg.FaviconExt != ext {
		_ = os.Remove(old)
	}
	path := filepath.Join(s.dataDir, "favicon"+ext)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	s.cfg.FaviconExt = ext
	return s.cfg.Save(s.cfgPath)
}

// removeFavicon deletes the stored favicon and clears the config.
func (s *Server) removeFavicon() error {
	if path := s.faviconFile(); path != "" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	s.cfg.FaviconExt = ""
	return s.cfg.Save(s.cfgPath)
}

var funcs = template.FuncMap{
	"fmtDate": func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.Format("January 2, 2006")
	},
}

// pages are content templates; each is parsed together with the base layout.
var pages = []string{"page", "message", "login", "admin", "edit", "gallery"}

func (s *Server) parseTemplates(tmplFS fs.FS) error {
	s.tmpl = make(map[string]*template.Template, len(pages))
	for _, p := range pages {
		t, err := template.New("base.html").Funcs(funcs).
			ParseFS(tmplFS, "templates/base.html", "templates/"+p+".html")
		if err != nil {
			return err
		}
		s.tmpl[p] = t
	}
	return nil
}

func (s *Server) routes() {
	mux := http.NewServeMux()

	// Public.
	mux.Handle("GET /static/", s.static)
	mux.HandleFunc("GET /uploads/", s.serveUpload)
	mux.HandleFunc("GET /favicon.ico", s.serveFavicon)

	// Admin.
	mux.HandleFunc("GET /admin/login", s.handleLoginForm)
	mux.HandleFunc("POST /admin/login", s.handleLogin)
	mux.HandleFunc("POST /admin/logout", s.auth.Require(s.handleLogout))
	mux.HandleFunc("GET /admin", s.auth.Require(s.handleDashboard))
	mux.HandleFunc("GET /admin/edit", s.auth.Require(s.handleEdit))
	mux.HandleFunc("POST /admin/save", s.auth.Require(s.handleSave))
	mux.HandleFunc("POST /admin/delete", s.auth.Require(s.handleDelete))
	mux.HandleFunc("POST /admin/preview", s.auth.Require(s.handlePreview))
	mux.HandleFunc("POST /admin/settings", s.auth.Require(s.handleSaveSettings))
	mux.HandleFunc("GET /admin/gallery", s.auth.Require(s.handleGallery))
	mux.HandleFunc("POST /admin/upload", s.auth.Require(s.handleUpload))
	mux.HandleFunc("POST /admin/gallery/delete", s.auth.Require(s.handleDeleteUpload))
	mux.HandleFunc("POST /admin/favicon", s.auth.Require(s.handleUploadFavicon))
	mux.HandleFunc("POST /admin/favicon/delete", s.auth.Require(s.handleDeleteFavicon))

	// Catch-all page route at any depth. The home page (/) and nested paths
	// (/projects/ink) all resolve here; the more specific patterns above win.
	mux.HandleFunc("GET /{path...}", s.handlePage)

	s.mux = mux
}

// render writes a 200 HTML response from the named template.
func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, data map[string]any) {
	s.renderStatus(w, r, http.StatusOK, name, data)
}

// renderStatus writes an HTML response with the given status. The template is
// fully rendered into a buffer first so a mid-render error can't emit a partial
// page with a 200 already committed.
func (s *Server) renderStatus(w http.ResponseWriter, r *http.Request, status int, name string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	data["Site"] = s.cfg.Title()
	data["Header"] = s.cfg.Header()
	// FontStack comes from a fixed registry (never user input), so it is safe to
	// emit verbatim into the page's CSS.
	data["FontStack"] = template.CSS(s.cfg.FontStack())
	data["Favicon"] = s.cfg.HasFavicon()
	data["Authed"] = s.auth.Authed(r)

	t, ok := s.tmpl[name]
	if !ok {
		http.Error(w, "template not found: "+name, http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "base.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	s.renderStatus(w, r, http.StatusNotFound, "message", map[string]any{
		"Title":   "Not found",
		"Heading": "Not found",
		"Text":    "There's nothing at this address.",
	})
}
