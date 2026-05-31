package content

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidSlug(t *testing.T) {
	valid := []string{"", "about", "projects/ink", "a/b/c", "notes/2026/trip", "a-b/c-d"}
	for _, s := range valid {
		if !ValidSlug(s) {
			t.Errorf("ValidSlug(%q) = false, want true", s)
		}
	}
	invalid := []string{
		"..", "../etc", "a/..", "a/../b", ".", "a/./b",
		"/leading", "trailing/", "double//slash", "Upper", "has space",
		"under_score", "a/", "/", "dot.ext", "café",
	}
	for _, s := range invalid {
		if ValidSlug(s) {
			t.Errorf("ValidSlug(%q) = true, want false", s)
		}
	}
}

func TestSaveGetRoundTrip(t *testing.T) {
	s := newTestStore(t)
	doc := &Document{Slug: "about", Title: "About", Date: "2026-05-30", Summary: "hi", Body: "Hello **world**"}
	if err := s.Save(doc); err != nil {
		t.Fatalf("Save: %v", err)
	}
	p, err := s.Get("about")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if p.Title != "About" || p.Summary != "hi" || strings.TrimSpace(p.Body) != "Hello **world**" {
		t.Errorf("round trip mismatch: %+v", p)
	}
	if p.Date.Format("2006-01-02") != "2026-05-30" {
		t.Errorf("date = %v", p.Date)
	}
}

func TestSaveNestedCreatesDirs(t *testing.T) {
	s := newTestStore(t)
	if err := s.Save(&Document{Slug: "projects/2026/ink", Title: "Ink"}); err != nil {
		t.Fatalf("Save nested: %v", err)
	}
	want := filepath.Join(s.pagesDir(), "projects", "2026", "ink.md")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("expected file at %s: %v", want, err)
	}
	if _, err := s.Get("projects/2026/ink"); err != nil {
		t.Errorf("Get nested: %v", err)
	}
}

func TestHomePageIsIndex(t *testing.T) {
	s := newTestStore(t)
	if err := s.Save(&Document{Slug: "", Title: "Home"}); err != nil {
		t.Fatalf("Save home: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.pagesDir(), "index.md")); err != nil {
		t.Fatalf("home should be index.md: %v", err)
	}
	p, err := s.Get("")
	if err != nil {
		t.Fatalf("Get home: %v", err)
	}
	if p.URL() != "/" || !p.IsHome() {
		t.Errorf("home URL/IsHome wrong: %q %v", p.URL(), p.IsHome())
	}
}

func TestReservedAndInvalidSlugs(t *testing.T) {
	s := newTestStore(t)
	if err := s.Save(&Document{Slug: "index", Title: "x"}); err != ErrReserved {
		t.Errorf("Save index = %v, want ErrReserved", err)
	}
	if err := s.Save(&Document{Slug: "../escape", Title: "x"}); err != ErrInvalidSlug {
		t.Errorf("Save traversal = %v, want ErrInvalidSlug", err)
	}
	if _, err := s.Get("../../etc/passwd"); err != ErrInvalidSlug {
		t.Errorf("Get traversal = %v, want ErrInvalidSlug", err)
	}
	if _, err := s.Get("missing"); err != ErrNotFound {
		t.Errorf("Get missing = %v, want ErrNotFound", err)
	}
}

func TestListSortsHomeFirst(t *testing.T) {
	s := newTestStore(t)
	for _, slug := range []string{"zebra", "", "alpha", "projects/ink"} {
		if err := s.Save(&Document{Slug: slug, Title: slug}); err != nil {
			t.Fatalf("Save %q: %v", slug, err)
		}
	}
	pages, err := s.List(false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(pages) != 4 {
		t.Fatalf("got %d pages, want 4", len(pages))
	}
	if !pages[0].IsHome() {
		t.Errorf("first page should be home, got %q", pages[0].Slug)
	}
	got := []string{pages[1].Slug, pages[2].Slug, pages[3].Slug}
	want := []string{"alpha", "projects/ink", "zebra"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sort order = %v, want home + %v", got, want)
			break
		}
	}
}

func TestListPublishedOnly(t *testing.T) {
	s := newTestStore(t)
	_ = s.Save(&Document{Slug: "public", Title: "p"})
	_ = s.Save(&Document{Slug: "secret", Title: "s", Draft: true})
	pages, err := s.List(true)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(pages) != 1 || pages[0].Slug != "public" {
		t.Errorf("publishedOnly returned %d pages: %+v", len(pages), pages)
	}
}

func TestDeletePrunesEmptyDirs(t *testing.T) {
	s := newTestStore(t)
	_ = s.Save(&Document{Slug: "a/b/c", Title: "c"})
	if err := s.Delete("a/b/c"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.pagesDir(), "a")); !os.IsNotExist(err) {
		t.Errorf("empty parent dirs should be pruned")
	}
	// Deleting a missing page is not an error.
	if err := s.Delete("a/b/c"); err != nil {
		t.Errorf("Delete missing = %v, want nil", err)
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}
