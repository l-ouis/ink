package render

import (
	"bytes"
	"html/template"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
)

// syntaxStyle is the Chroma style fenced code blocks are highlighted with. Its
// matching CSS lives in static/syntax.css (regenerate it if this changes; see
// the generator note there).
const syntaxStyle = "github"

// markdown renders CommonMark + GitHub extensions, with syntax-highlighted
// fenced code blocks. Raw HTML is allowed because the only author is the
// trusted site owner.
type markdown struct{ md goldmark.Markdown }

func newMarkdown() *markdown {
	return &markdown{md: goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			extension.Typographer,
			highlighting.NewHighlighting(
				highlighting.WithStyle(syntaxStyle),
				// Class-based output keeps the HTML small; colours come from the
				// stylesheet rather than inline styles on every token.
				highlighting.WithFormatOptions(chromahtml.WithClasses(true)),
			),
		),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRendererOptions(html.WithUnsafe()),
	)}
}

func (m *markdown) Render(source []byte) (template.HTML, error) {
	var buf bytes.Buffer
	if err := m.md.Convert(source, &buf); err != nil {
		return "", err
	}
	return template.HTML(buf.String()), nil
}

func init() {
	Register(".md", newMarkdown())
}
