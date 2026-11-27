package renderer

import (
	"bytes"
	"fmt"
	"html"
	"net/url"
	"strings"

	xhtml "golang.org/x/net/html"
)

var allowedFragmentTags = map[string]bool{
	"b":      true,
	"i":      true,
	"em":     true,
	"strong": true,
	"p":      true,
	"br":     true,
	"ul":     true,
	"ol":     true,
	"li":     true,
	"span":   true,
	"a":      true,
}

var voidTags = map[string]bool{
	"br": true,
}

var dropSubtreeTags = map[string]bool{
	"script":   true,
	"style":    true,
	"iframe":   true,
	"object":   true,
	"embed":    true,
	"svg":      true,
	"math":     true,
	"template": true,
	"head":     true,
}

// SanitizeHTMLFragment tokenizes untrusted HTML using golang.org/x/net/html and
// emits only allowlisted formatting tags (b,i,em,strong,p,br,ul,ol,li,span,a).
// On <a>, only clean https:// hrefs are retained; scripts and event attributes
// are stripped completely.
func SanitizeHTMLFragment(input string) string {
	if input == "" {
		return ""
	}
	tokenizer := xhtml.NewTokenizer(strings.NewReader(input))
	var out bytes.Buffer
	var openStack []string
	skipDepth := 0

	for {
		tt := tokenizer.Next()
		switch tt {
		case xhtml.ErrorToken:
			for i := len(openStack) - 1; i >= 0; i-- {
				out.WriteString("</")
				out.WriteString(openStack[i])
				out.WriteByte('>')
			}
			return out.String()

		case xhtml.TextToken:
			if skipDepth > 0 {
				continue
			}
			out.WriteString(html.EscapeString(string(tokenizer.Text())))

		case xhtml.StartTagToken, xhtml.SelfClosingTagToken:
			rawName, hasAttr := tokenizer.TagName()
			tag := strings.ToLower(string(rawName))
			if skipDepth > 0 {
				if dropSubtreeTags[tag] && tt == xhtml.StartTagToken {
					skipDepth++
				}
				continue
			}
			if dropSubtreeTags[tag] {
				if tt == xhtml.StartTagToken {
					skipDepth = 1
				}
				continue
			}
			if !allowedFragmentTags[tag] {
				continue
			}
			var safeHref string
			if tag == "a" && hasAttr {
				for {
					k, v, more := tokenizer.TagAttr()
					if strings.EqualFold(string(k), "href") {
						candidate := strings.TrimSpace(string(v))
						if isSafeHTTPSURL(candidate) {
							safeHref = candidate
						}
					}
					if !more {
						break
					}
				}
			}
			out.WriteByte('<')
			out.WriteString(tag)
			if tag == "a" && safeHref != "" {
				out.WriteString(` href="`)
				out.WriteString(html.EscapeString(safeHref))
				out.WriteString(`" rel="noopener noreferrer"`)
			}
			out.WriteByte('>')
			if !voidTags[tag] && tt == xhtml.StartTagToken {
				openStack = append(openStack, tag)
			}

		case xhtml.EndTagToken:
			rawName, _ := tokenizer.TagName()
			tag := strings.ToLower(string(rawName))
			if skipDepth > 0 {
				if dropSubtreeTags[tag] {
					skipDepth--
				}
				continue
			}
			if !allowedFragmentTags[tag] || voidTags[tag] {
				continue
			}
			idx := -1
			for i := len(openStack) - 1; i >= 0; i-- {
				if openStack[i] == tag {
					idx = i
					break
				}
			}
			if idx >= 0 {
				for i := len(openStack) - 1; i >= idx; i-- {
					out.WriteString("</")
					out.WriteString(openStack[i])
					out.WriteByte('>')
				}
				openStack = openStack[:idx]
			}
		}
	}
}

func isSafeHTTPSURL(raw string) bool {
	if raw == "" || strings.ContainsAny(raw, "\r\n\t\x00 ") {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Host == "" || u.User != nil {
		return false
	}
	return true
}

// ValidateEmailHTMLLinks inspects rendered HTML for insecure non-HTTPS links
// or external resource sources.
func ValidateEmailHTMLLinks(renderedHTML string) error {
	if renderedHTML == "" {
		return nil
	}
	tokenizer := xhtml.NewTokenizer(strings.NewReader(renderedHTML))
	for {
		tt := tokenizer.Next()
		if tt == xhtml.ErrorToken {
			return nil
		}
		if tt != xhtml.StartTagToken && tt != xhtml.SelfClosingTagToken {
			continue
		}
		_, hasAttr := tokenizer.TagName()
		if !hasAttr {
			continue
		}
		for {
			k, v, more := tokenizer.TagAttr()
			attr := strings.ToLower(string(k))
			val := strings.TrimSpace(string(v))
			if (attr == "href" || attr == "src") && val != "" && !strings.HasPrefix(val, "#") {
				u, err := url.Parse(val)
				if err != nil || u.Scheme == "" {
					return fmt.Errorf("attribute %s=%q must use an explicit https:// URL", attr, val)
				}
				scheme := strings.ToLower(u.Scheme)
				if attr == "src" && scheme != "https" {
					return fmt.Errorf("attribute src=%q must use https://", val)
				}
				if attr == "href" && scheme != "https" && scheme != "mailto" && scheme != "tel" {
					return fmt.Errorf("attribute href=%q must use https://, mailto:, or tel:", val)
				}
			}
			if !more {
				break
			}
		}
	}
}
