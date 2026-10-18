// Package markdown renders user-written Markdown to sanitized HTML.
package markdown

import (
	"bytes"
	"html"
	"regexp"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark/v2/extension"
	"github.com/yuin/goldmark/v2/parser"
	ghtml "github.com/yuin/goldmark/v2/renderer/html"

	"github.com/harshadptl/gcore/internal/textutil"
)

// Renderer converts Markdown to safe HTML. It is safe for concurrent use.
type Renderer struct {
	sanitizer *bluemonday.Policy
	stripper  *bluemonday.Policy
}

// New returns a Renderer with GitHub-flavoured Markdown enabled.
func New() *Renderer {
	p := bluemonday.UGCPolicy()
	p.AllowAttrs("id").Matching(regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)).OnElements("h1", "h2", "h3", "h4", "h5", "h6")
	p.AllowAttrs("class").Matching(regexp.MustCompile(`^language-[a-zA-Z0-9+#_-]+$`)).OnElements("code")
	// GFM task lists render disabled checkboxes.
	p.AllowAttrs("type").Matching(regexp.MustCompile(`^checkbox$`)).OnElements("input")
	p.AllowAttrs("checked", "disabled").OnElements("input")
	p.AddTargetBlankToFullyQualifiedLinks(true)
	p.RequireNoReferrerOnFullyQualifiedLinks(true)

	return &Renderer{sanitizer: p, stripper: bluemonday.StrictPolicy()}
}

// Render returns sanitized HTML for src. Raw HTML in the Markdown is dropped
// by goldmark, and the output is sanitized again as defence in depth.
func (r *Renderer) Render(src string) (string, error) {
	source := []byte(src)

	// Parsers keep per-parse state, so build one per call (cheap).
	p := parser.New(
		parser.WithAutoHeadingID(),
		parser.WithExtensions(extension.GFMParser),
	)
	w := ghtml.New(ghtml.WithExtensions(extension.GFMHTMLRenderer))

	var buf bytes.Buffer
	if err := w.Render(&buf, source, p.Parse(source)); err != nil {
		return "", err
	}
	return r.sanitizer.Sanitize(buf.String()), nil
}

// PlainText strips all tags from rendered HTML and collapses whitespace.
func (r *Renderer) PlainText(renderedHTML string) string {
	return textutil.CollapseSpaces(html.UnescapeString(r.stripper.Sanitize(renderedHTML)))
}

// Sanitize cleans arbitrary user HTML with the same policy as Render.
func (r *Renderer) Sanitize(s string) string { return r.sanitizer.Sanitize(s) }
