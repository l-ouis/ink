// Package config loads and persists the single JSON config file that holds the
// owner's credentials and server secrets.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/crypto/bcrypt"
)

// Config is the on-disk configuration. Unexported fields are not persisted.
type Config struct {
	SiteTitle     string `json:"site_title"`
	HeaderTitle   string `json:"header_title"`
	Font          string `json:"font"`
	FaviconExt    string `json:"favicon_ext"`
	PasswordHash  string `json:"password_hash"`
	SessionSecret string `json:"session_secret"`
	SecureCookies bool   `json:"secure_cookies"`
}

// Font is a selectable serif typeface. Stack is the CSS font-family list; its
// first entry must match a @font-face family defined in static/fonts.css.
type Font struct {
	Key   string
	Name  string
	Stack string
}

// Fonts is the curated list offered in the admin font picker. The first entry
// is the default.
var Fonts = []Font{
	{"noto-serif", "Noto Serif", `"Noto Serif", Georgia, serif`},
	{"lora", "Lora", `"Lora", Georgia, serif`},
	{"source-serif-4", "Source Serif 4", `"Source Serif 4", Georgia, serif`},
	{"eb-garamond", "EB Garamond", `"EB Garamond", Georgia, serif`},
	{"merriweather", "Merriweather", `"Merriweather", Georgia, serif`},
	{"playfair-display", "Playfair Display", `"Playfair Display", "Times New Roman", serif`},
	{"pt-serif", "PT Serif", `"PT Serif", Georgia, serif`},
	{"libre-baskerville", "Libre Baskerville", `"Libre Baskerville", Georgia, serif`},
	{"spectral", "Spectral", `"Spectral", Georgia, serif`},
	{"bitter", "Bitter", `"Bitter", Georgia, serif`},
}

func fontByKey(key string) Font {
	for _, f := range Fonts {
		if f.Key == key {
			return f
		}
	}
	return Fonts[0]
}

// NormalizeFont returns key if it names a known font, else the default key.
func NormalizeFont(key string) string { return fontByKey(key).Key }

// Load reads the config at path. A missing file yields a zero-value config so
// the server can still boot (and the login page can prompt for setup).
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// Save writes the config atomically to path, creating parent dirs as needed.
func (c *Config) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// EnsureSecret generates a session secret on first run and persists it. It
// returns true when a new secret was written.
func (c *Config) EnsureSecret(path string) (bool, error) {
	if c.SessionSecret != "" {
		return false, nil
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return false, err
	}
	c.SessionSecret = hex.EncodeToString(buf)
	return true, c.Save(path)
}

// Secret returns the raw session-signing key.
func (c *Config) Secret() []byte {
	b, err := hex.DecodeString(c.SessionSecret)
	if err != nil {
		return []byte(c.SessionSecret)
	}
	return b
}

// HasPassword reports whether an owner password has been set.
func (c *Config) HasPassword() bool { return c.PasswordHash != "" }

// SetPassword hashes and stores plain as the owner password.
func (c *Config) SetPassword(plain string) error {
	h, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	c.PasswordHash = string(h)
	return nil
}

// CheckPassword reports whether plain matches the stored password hash.
func (c *Config) CheckPassword(plain string) bool {
	if c.PasswordHash == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(c.PasswordHash), []byte(plain)) == nil
}

// Title returns the site title, falling back to a default.
func (c *Config) Title() string {
	if c.SiteTitle == "" {
		return "Ink"
	}
	return c.SiteTitle
}

// Header returns the top-left header title, falling back to a default.
func (c *Config) Header() string {
	if c.HeaderTitle == "" {
		return "ink"
	}
	return c.HeaderTitle
}

// FontKey returns the selected font key, defaulting to the first font.
func (c *Config) FontKey() string { return fontByKey(c.Font).Key }

// FontStack returns the CSS font-family stack for the selected font.
func (c *Config) FontStack() string { return fontByKey(c.Font).Stack }

// HasFavicon reports whether a custom favicon has been uploaded.
func (c *Config) HasFavicon() bool { return c.FaviconExt != "" }
