// Package media is the disk-backed store for uploaded images. Unlike templates
// and CSS (which are embedded into the binary at build time), uploads live on
// disk under content/uploads/ and are served at runtime, so new images appear
// without rebuilding.
package media

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var (
	ErrUnsupported = errors.New("media: unsupported image type")
	ErrInvalidName = errors.New("media: invalid name")
)

// extByType maps a sniffed content type to a canonical file extension. Only
// raster formats are allowed; SVG is deliberately excluded because it can carry
// script that would execute on the site's own origin if opened directly.
var extByType = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

// Image is a stored upload.
type Image struct {
	Name    string
	URL     string
	Size    int64
	ModTime time.Time
}

// Store reads and writes images under a single directory.
type Store struct {
	root string
}

// New returns a store rooted at dir, creating it if needed.
func New(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Store{root: dir}, nil
}

// Dir returns the root directory, for wiring a file server.
func (s *Store) Dir() string { return s.root }

// nameRe matches a safe stored filename: it is the only guard for the delete
// path, so it is strict and excludes any path separators.
var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*\.(?:png|jpg|gif|webp)$`)

// ValidName reports whether name is a safe stored-image filename.
func ValidName(name string) bool { return nameRe.MatchString(name) }

// List returns all stored images, newest first.
func (s *Store) List() ([]Image, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Image
	for _, e := range entries {
		if e.IsDir() || !ValidName(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Image{
			Name:    e.Name(),
			URL:     "/uploads/" + e.Name(),
			Size:    info.Size(),
			ModTime: info.ModTime(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModTime.After(out[j].ModTime) })
	return out, nil
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// DetectImageExt sniffs data's content type and returns the canonical file
// extension for it (e.g. ".png"), reporting false for unsupported types.
func DetectImageExt(data []byte) (string, bool) {
	ct := http.DetectContentType(data)
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	ext, ok := extByType[strings.TrimSpace(ct)]
	return ext, ok
}

// Save validates that data is a supported image, writes it under a safe,
// deduplicated filename derived from origName, and returns the stored Image.
// The extension is chosen from the sniffed content type, not the client name.
func (s *Store) Save(origName string, data []byte) (Image, error) {
	ext, ok := DetectImageExt(data)
	if !ok {
		return Image{}, ErrUnsupported
	}

	base := strings.ToLower(strings.TrimSuffix(filepath.Base(origName), filepath.Ext(origName)))
	base = strings.Trim(nonAlnum.ReplaceAllString(base, "-"), "-")
	if base == "" {
		base = "image"
	}

	name := base + ext
	for n := 1; ; n++ {
		if _, err := os.Stat(filepath.Join(s.root, name)); errors.Is(err, os.ErrNotExist) {
			break
		}
		name = fmt.Sprintf("%s-%d%s", base, n, ext)
	}

	path := filepath.Join(s.root, name)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return Image{}, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return Image{}, err
	}
	img := Image{Name: name, URL: "/uploads/" + name, Size: int64(len(data))}
	if info, err := os.Stat(path); err == nil {
		img.ModTime = info.ModTime()
	}
	return img, nil
}

// Delete removes an image. Deleting a missing image is not an error.
func (s *Store) Delete(name string) error {
	if !ValidName(name) {
		return ErrInvalidName
	}
	if err := os.Remove(filepath.Join(s.root, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
