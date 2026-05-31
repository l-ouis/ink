package content

import (
	"bytes"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// meta is the YAML frontmatter block at the top of a content file.
type meta struct {
	Title   string `yaml:"title"`
	Date    string `yaml:"date"`
	Draft   bool   `yaml:"draft"`
	Summary string `yaml:"summary"`
}

var dateLayouts = []string{"2006-01-02", time.RFC3339, "2006-01-02 15:04", "2006-01-02T15:04"}

func parseDate(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, l := range dateLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// splitFrontmatter separates a leading `---`-delimited YAML block from the body.
// Files without frontmatter return empty meta and the whole input as body.
func splitFrontmatter(raw []byte) (m meta, body string) {
	if !bytes.HasPrefix(raw, []byte("---\n")) && !bytes.HasPrefix(raw, []byte("---\r\n")) {
		return meta{}, string(raw)
	}
	// Find the closing delimiter on its own line.
	rest := raw[3:]
	rest = bytes.TrimLeft(rest, "\r")
	rest = bytes.TrimPrefix(rest, []byte("\n"))
	idx := bytes.Index(rest, []byte("\n---"))
	if idx < 0 {
		return meta{}, string(raw)
	}
	yamlPart := rest[:idx]
	after := rest[idx+1:] // starts at "---..."
	// Drop the closing "---" line.
	if nl := bytes.IndexByte(after, '\n'); nl >= 0 {
		body = string(after[nl+1:])
	} else {
		body = ""
	}
	_ = yaml.Unmarshal(yamlPart, &m)
	return m, body
}

// buildFile renders a Document back into on-disk file bytes.
func buildFile(d *Document) []byte {
	var b strings.Builder
	b.WriteString("---\n")
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	_ = enc.Encode(meta{
		Title:   d.Title,
		Date:    d.Date,
		Draft:   d.Draft,
		Summary: d.Summary,
	})
	_ = enc.Close()
	b.WriteString("---\n\n")
	body := strings.ReplaceAll(d.Body, "\r\n", "\n")
	b.WriteString(strings.TrimLeft(body, "\n"))
	if !strings.HasSuffix(b.String(), "\n") {
		b.WriteString("\n")
	}
	return []byte(b.String())
}
