package media

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Minimal byte headers that http.DetectContentType recognises.
var (
	pngBytes  = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	gifBytes  = []byte("GIF89a\x01\x00\x01\x00")
	jpegBytes = []byte("\xff\xd8\xff\xe0\x00\x10JFIF")
)

func TestValidName(t *testing.T) {
	valid := []string{"cat.png", "a.jpg", "my-photo-1.webp", "x.gif"}
	for _, n := range valid {
		if !ValidName(n) {
			t.Errorf("ValidName(%q) = false, want true", n)
		}
	}
	invalid := []string{"../etc.png", "a/b.png", "UPPER.png", "no-ext", "space .png", ".png", "a.svg", "a.exe"}
	for _, n := range invalid {
		if ValidName(n) {
			t.Errorf("ValidName(%q) = true, want false", n)
		}
	}
}

func TestDetectImageExt(t *testing.T) {
	cases := map[string][]byte{".png": pngBytes, ".gif": gifBytes, ".jpg": jpegBytes}
	for want, data := range cases {
		got, ok := DetectImageExt(data)
		if !ok || got != want {
			t.Errorf("DetectImageExt(%s) = %q,%v; want %q,true", want, got, ok, want)
		}
	}
	if _, ok := DetectImageExt([]byte("not an image at all")); ok {
		t.Error("DetectImageExt(text) = true, want false")
	}
}

func TestSaveSanitizesAndDedups(t *testing.T) {
	s := newStore(t)
	img, err := s.Save("My Photo!.JPG", pngBytes) // name messy, content is PNG
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	// Extension comes from sniffed content (.png), name is slugified.
	if img.Name != "my-photo.png" {
		t.Errorf("name = %q, want my-photo.png", img.Name)
	}
	if img.URL != "/uploads/my-photo.png" {
		t.Errorf("url = %q", img.URL)
	}
	img2, _ := s.Save("my photo.png", pngBytes)
	if img2.Name != "my-photo-1.png" {
		t.Errorf("dedup name = %q, want my-photo-1.png", img2.Name)
	}
}

func TestSaveRejectsNonImage(t *testing.T) {
	s := newStore(t)
	if _, err := s.Save("evil.png", []byte("<html>not an image</html>")); err != ErrUnsupported {
		t.Errorf("Save non-image = %v, want ErrUnsupported", err)
	}
}

func TestListNewestFirst(t *testing.T) {
	s := newStore(t)
	if imgs, _ := s.List(); len(imgs) != 0 {
		t.Errorf("empty store List = %d, want 0", len(imgs))
	}
	a, _ := s.Save("a.png", pngBytes)
	b, _ := s.Save("b.png", gifBytes)
	// Set explicit mtimes so ordering is deterministic regardless of how close
	// in time the two writes happened.
	now := time.Now()
	_ = os.Chtimes(filepath.Join(s.Dir(), a.Name), now.Add(-time.Hour), now.Add(-time.Hour))
	_ = os.Chtimes(filepath.Join(s.Dir(), b.Name), now, now)
	imgs, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(imgs) != 2 {
		t.Fatalf("List = %d, want 2", len(imgs))
	}
	if imgs[0].Name != b.Name {
		t.Errorf("expected newest (%s) first, got %q", b.Name, imgs[0].Name)
	}
}

func TestDeleteValidatesName(t *testing.T) {
	s := newStore(t)
	if err := s.Delete("../config.json"); err != ErrInvalidName {
		t.Errorf("Delete traversal = %v, want ErrInvalidName", err)
	}
	img, _ := s.Save("gone.png", pngBytes)
	if err := s.Delete(img.Name); err != nil {
		t.Errorf("Delete = %v", err)
	}
	if err := s.Delete(img.Name); err != nil { // missing is not an error
		t.Errorf("Delete missing = %v, want nil", err)
	}
}

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}
