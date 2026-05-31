package config

import (
	"path/filepath"
	"testing"
)

func TestDefaults(t *testing.T) {
	c := &Config{}
	if c.Title() != "Ink" {
		t.Errorf("default Title = %q, want Ink", c.Title())
	}
	if c.Header() != "ink" {
		t.Errorf("default Header = %q, want ink", c.Header())
	}
	if c.FontKey() != Fonts[0].Key {
		t.Errorf("default FontKey = %q, want %q", c.FontKey(), Fonts[0].Key)
	}
	if c.FontStack() != Fonts[0].Stack {
		t.Errorf("default FontStack = %q, want %q", c.FontStack(), Fonts[0].Stack)
	}
	if c.HasFavicon() {
		t.Error("HasFavicon = true on empty config")
	}
}

func TestNormalizeFont(t *testing.T) {
	if got := NormalizeFont("lora"); got != "lora" {
		t.Errorf("NormalizeFont(lora) = %q", got)
	}
	if got := NormalizeFont("does-not-exist"); got != Fonts[0].Key {
		t.Errorf("NormalizeFont(bogus) = %q, want default %q", got, Fonts[0].Key)
	}
	if got := NormalizeFont(""); got != Fonts[0].Key {
		t.Errorf("NormalizeFont(empty) = %q, want default", got)
	}
}

func TestCustomValues(t *testing.T) {
	c := &Config{SiteTitle: "My Site", HeaderTitle: "ms", Font: "lora", FaviconExt: ".png"}
	if c.Title() != "My Site" || c.Header() != "ms" || c.FontKey() != "lora" {
		t.Errorf("custom values not returned: %+v", c)
	}
	if !c.HasFavicon() {
		t.Error("HasFavicon = false with FaviconExt set")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	orig := &Config{SiteTitle: "S", HeaderTitle: "h", Font: "bitter", FaviconExt: ".gif", SecureCookies: true}
	if err := orig.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.SiteTitle != "S" || got.HeaderTitle != "h" || got.Font != "bitter" || got.FaviconExt != ".gif" || !got.SecureCookies {
		t.Errorf("round trip mismatch: %+v", got)
	}
}

func TestLoadMissingFileIsZeroValue(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("Load missing = %v, want nil", err)
	}
	if got.HasPassword() {
		t.Error("missing config should have no password")
	}
}

func TestEnsureSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	c := &Config{}
	created, err := c.EnsureSecret(path)
	if err != nil || !created {
		t.Fatalf("EnsureSecret first call: created=%v err=%v", created, err)
	}
	if len(c.Secret()) == 0 {
		t.Error("Secret empty after EnsureSecret")
	}
	created2, err := c.EnsureSecret(path)
	if err != nil || created2 {
		t.Errorf("EnsureSecret second call: created=%v err=%v, want false,nil", created2, err)
	}
}

func TestPasswordHashing(t *testing.T) {
	c := &Config{}
	if c.HasPassword() || c.CheckPassword("anything") {
		t.Error("empty config should reject all passwords")
	}
	if err := c.SetPassword("hunter2"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	if !c.HasPassword() {
		t.Error("HasPassword = false after SetPassword")
	}
	if !c.CheckPassword("hunter2") {
		t.Error("CheckPassword rejected the correct password")
	}
	if c.CheckPassword("wrong") {
		t.Error("CheckPassword accepted a wrong password")
	}
}
