// Package render turns source files into HTML. Renderers are registered by file
// extension, so adding Typst support later is just another Register call.
package render

import (
	"fmt"
	"html/template"
	"sync"
)

// Renderer converts source bytes into trusted HTML.
type Renderer interface {
	Render(source []byte) (template.HTML, error)
}

var (
	mu       sync.RWMutex
	registry = map[string]Renderer{}
)

// Register associates a renderer with a file extension (e.g. ".md").
func Register(ext string, r Renderer) {
	mu.Lock()
	defer mu.Unlock()
	registry[ext] = r
}

// For returns the renderer registered for ext, if any.
func For(ext string) (Renderer, bool) {
	mu.RLock()
	defer mu.RUnlock()
	r, ok := registry[ext]
	return r, ok
}

// Render renders source using the renderer registered for ext.
func Render(ext string, source []byte) (template.HTML, error) {
	r, ok := For(ext)
	if !ok {
		return "", fmt.Errorf("render: no renderer for %q", ext)
	}
	return r.Render(source)
}
