// Package canvas is the file-backed store for the single infinite canvas. The
// whole site is one boundless 2-D plane of positioned items (Markdown text
// boxes and images), persisted as a single JSON file. There are no pages.
package canvas

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

var (
	ErrNotFound = errors.New("canvas: item not found")
	ErrType     = errors.New("canvas: invalid item type")
)

// Item types.
const (
	TypeText  = "text"
	TypeImage = "image"
)

// Image layers: whether an image stacks below or above all text.
const (
	LayerUnder = "under"
	LayerOver  = "over"
)

// Item is a single thing placed on the canvas. X/Y are the top-left position in
// world coordinates; W/H are its size in world units. Z is the stacking order
// (higher is on top). Content holds Markdown source for text items and the
// upload URL (e.g. "/uploads/cat.png") for image items.
type Item struct {
	ID      string  `json:"id"`
	Type    string  `json:"type"`
	X       float64 `json:"x"`
	Y       float64 `json:"y"`
	W       float64 `json:"w"`
	H       float64 `json:"h"`
	Z       int     `json:"z"`
	Content string  `json:"content"`
	// Layer applies to images only: "under" (default) or "over" text.
	Layer string `json:"layer,omitempty"`
	// Adaptive applies to text only: flip the text colour between black and
	// white to contrast with the image behind it.
	Adaptive bool `json:"adaptive,omitempty"`
}

// normalize keeps type-specific fields consistent: images carry a valid layer
// and never the text-only Adaptive flag, and vice versa.
func normalize(it *Item) {
	if it.Type == TypeImage {
		if it.Layer != LayerOver {
			it.Layer = LayerUnder
		}
		it.Adaptive = false
	} else {
		it.Layer = ""
	}
}

// Store reads and writes the canvas to a single JSON file, keeping the current
// state in memory behind a mutex so concurrent requests stay consistent.
type Store struct {
	path string
	mu   sync.RWMutex
	items map[string]*Item
}

// New opens (or initialises) the canvas stored at path.
func New(path string) (*Store, error) {
	s := &Store{path: path, items: map[string]*Item{}}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil // empty canvas on first run
	}
	if err != nil {
		return err
	}
	var doc struct {
		Items []*Item `json:"items"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return err
	}
	for _, it := range doc.Items {
		s.items[it.ID] = it
	}
	return nil
}

// save writes the current items to disk atomically. Callers must hold the lock.
func (s *Store) save() error {
	doc := struct {
		Items []*Item `json:"items"`
	}{Items: s.sortedLocked()}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// sortedLocked returns items ordered by Z then ID for a stable layout. Callers
// must hold at least the read lock.
func (s *Store) sortedLocked() []*Item {
	out := make([]*Item, 0, len(s.items))
	for _, it := range s.items {
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Z != out[j].Z {
			return out[i].Z < out[j].Z
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// All returns every item, in render order (back to front).
func (s *Store) All() []*Item {
	s.mu.RLock()
	defer s.mu.RUnlock()
	// Return copies so callers can't mutate the store's state.
	src := s.sortedLocked()
	out := make([]*Item, len(src))
	for i, it := range src {
		cp := *it
		out[i] = &cp
	}
	return out
}

// topZLocked returns one more than the highest Z in use (0 if empty).
func (s *Store) topZLocked() int {
	max := 0
	for _, it := range s.items {
		if it.Z > max {
			max = it.Z
		}
	}
	return max + 1
}

// Add stores a new item, assigning it a fresh ID and the top Z order, and
// returns the stored copy.
func (s *Store) Add(it Item) (*Item, error) {
	if it.Type != TypeText && it.Type != TypeImage {
		return nil, ErrType
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	it.ID = newID()
	it.Z = s.topZLocked()
	normalize(&it)
	stored := it
	s.items[it.ID] = &stored
	if err := s.save(); err != nil {
		delete(s.items, it.ID)
		return nil, err
	}
	cp := stored
	return &cp, nil
}

// Update applies geometry, stacking, content and option changes to an existing
// item. Type and ID are fixed; layer (images) and adaptive (text) are
// normalized to the item's type. Stacking order (z) is client-authoritative.
func (s *Store) Update(id string, x, y, w, h float64, z int, content, layer string, adaptive bool) (*Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.items[id]
	if !ok {
		return nil, ErrNotFound
	}
	prev := *it
	it.X, it.Y, it.W, it.H, it.Z, it.Content = x, y, w, h, z, content
	it.Layer, it.Adaptive = layer, adaptive
	normalize(it)
	if err := s.save(); err != nil {
		*it = prev
		return nil, err
	}
	cp := *it
	return &cp, nil
}

// Delete removes an item. Deleting a missing item is not an error.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.items[id]
	if !ok {
		return nil
	}
	delete(s.items, id)
	if err := s.save(); err != nil {
		s.items[id] = it
		return err
	}
	return nil
}

// newID returns a short random hex identifier.
func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
