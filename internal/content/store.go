// Package content is the file-backed store for pages. Each page is a single
// text file with YAML frontmatter, stored under content/pages/ at a path that
// mirrors its URL. A page at /projects/ink lives in pages/projects/ink.md; the
// home page (/) lives in pages/index.md.
package content

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var (
	ErrNotFound    = errors.New("content: not found")
	ErrInvalidSlug = errors.New("content: invalid slug")
	ErrReserved    = errors.New("content: reserved slug")
)

// segRe matches a single path segment: lowercase, digits and single hyphens.
// A slug is one or more of these joined by "/". This is the only thing standing
// between the editor and path traversal, so it is deliberately strict: no "..",
// no empty segments, no leading/trailing/double slashes, no backslashes.
var segRe = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// ValidSlug reports whether s is a safe, well-formed page slug. The empty slug
// is valid and denotes the home page.
func ValidSlug(s string) bool {
	if s == "" {
		return true
	}
	for _, seg := range strings.Split(s, "/") {
		if !segRe.MatchString(seg) {
			return false
		}
	}
	return true
}

// Page is a single piece of content with parsed metadata. Body holds the raw
// source (after frontmatter); it is rendered by the render package on demand.
type Page struct {
	Slug    string // URL path without leading slash; "" is the home page
	Ext     string // ".md", later ".typ"
	Title   string
	Date    time.Time
	Draft   bool
	Summary string
	Body    string
}

// URL returns the public path for the page.
func (p *Page) URL() string {
	if p.Slug == "" {
		return "/"
	}
	return "/" + p.Slug
}

// IsHome reports whether the page is the site's home page.
func (p *Page) IsHome() bool { return p.Slug == "" }

// Document is the editor's view of a page used when saving.
type Document struct {
	Slug    string
	Title   string
	Date    string
	Draft   bool
	Summary string
	Body    string
}

// Store reads and writes page files under a single root directory.
type Store struct {
	root string
}

// New returns a store rooted at dir, creating the pages/ subdir.
func New(dir string) (*Store, error) {
	s := &Store{root: dir}
	if err := os.MkdirAll(s.pagesDir(), 0o755); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) pagesDir() string { return filepath.Join(s.root, "pages") }

// pathFor maps a slug to its on-disk file path. The slug must already be valid.
func (s *Store) pathFor(slug string) string {
	rel := "index.md"
	if slug != "" {
		rel = filepath.FromSlash(slug) + ".md"
	}
	return filepath.Join(s.pagesDir(), rel)
}

// slugForPath is the inverse of pathFor for files found by List.
func slugForPath(rel string) string {
	slug := filepath.ToSlash(strings.TrimSuffix(rel, ".md"))
	if slug == "index" {
		return ""
	}
	return slug
}

// List returns all pages, walking the tree recursively. When publishedOnly is
// set, drafts are omitted. Pages are sorted with the home page first, then
// alphabetically by slug.
func (s *Store) List(publishedOnly bool) ([]*Page, error) {
	var out []*Page
	err := filepath.WalkDir(s.pagesDir(), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() || filepath.Ext(d.Name()) != ".md" {
			return nil
		}
		rel, err := filepath.Rel(s.pagesDir(), path)
		if err != nil {
			return nil
		}
		p, err := s.load(slugForPath(rel))
		if err != nil {
			return nil
		}
		if publishedOnly && p.Draft {
			return nil
		}
		out = append(out, p)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Slug == "" || out[j].Slug == "" {
			return out[i].Slug == "" && out[j].Slug != ""
		}
		return out[i].Slug < out[j].Slug
	})
	return out, nil
}

// Get loads a single page by slug.
func (s *Store) Get(slug string) (*Page, error) {
	if !ValidSlug(slug) {
		return nil, ErrInvalidSlug
	}
	if _, err := os.Stat(s.pathFor(slug)); err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return s.load(slug)
}

func (s *Store) load(slug string) (*Page, error) {
	raw, err := os.ReadFile(s.pathFor(slug))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	m, body := splitFrontmatter(raw)
	title := m.Title
	if title == "" {
		if slug == "" {
			title = "Home"
		} else {
			title = slug
		}
	}
	return &Page{
		Slug:    slug,
		Ext:     ".md",
		Title:   title,
		Date:    parseDate(m.Date),
		Draft:   m.Draft,
		Summary: m.Summary,
		Body:    body,
	}, nil
}

// Save writes d to disk, creating parent directories as needed. If the slug is
// new it creates a file; otherwise it overwrites. The slug is validated before
// any path is constructed.
func (s *Store) Save(d *Document) error {
	if !ValidSlug(d.Slug) {
		return ErrInvalidSlug
	}
	// "index" is reserved: the home page already owns pages/index.md.
	if d.Slug == "index" {
		return ErrReserved
	}
	if strings.TrimSpace(d.Date) == "" {
		d.Date = time.Now().Format("2006-01-02")
	}
	path := s.pathFor(d.Slug)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buildFile(d), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Delete removes a page. Deleting a missing page is not an error. Empty parent
// directories left behind are pruned.
func (s *Store) Delete(slug string) error {
	if !ValidSlug(slug) {
		return ErrInvalidSlug
	}
	path := s.pathFor(slug)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	s.pruneEmptyDirs(filepath.Dir(path))
	return nil
}

// pruneEmptyDirs removes now-empty directories from dir up to (but not
// including) the pages root. Best effort: it stops at the first non-empty dir.
func (s *Store) pruneEmptyDirs(dir string) {
	root := s.pagesDir()
	for dir != root && strings.HasPrefix(dir, root) {
		if err := os.Remove(dir); err != nil {
			return // not empty, or gone
		}
		dir = filepath.Dir(dir)
	}
}
