package web

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// fetchURL reads one page as text. Only http and https: a tool that reads
// file:// is a way to get the disk out through a page the model was told to
// read.
func fetchURL(ctx context.Context, client *http.Client, raw string, limit int64) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("web.fetch: %q is not a URL", raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("web.fetch: only http and https are allowed, not %q", u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("web.fetch: %q has no host", raw)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", fmt.Errorf("web.fetch: %w", err)
	}
	req.Header.Set("Accept", "text/html,text/plain;q=0.9,*/*;q=0.5")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("web.fetch: %w", err)
	}
	defer resp.Body.Close()

	// Read one page more than the cap so a truncated page can say so.
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return "", fmt.Errorf("web.fetch: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", httpError("web.fetch", resp.StatusCode, body)
	}

	truncated := int64(len(body)) > limit
	if truncated {
		body = body[:limit]
	}

	text := textOf(string(body))
	if truncated {
		text += "\n\n[page truncated]"
	}
	return text, nil
}

// blockTags end a line when they open or close. Everything else is inline: a
// sentence split across source lines should not become two.
var blockTags = map[string]bool{
	"p": true, "div": true, "br": true, "li": true, "tr": true, "section": true,
	"article": true, "header": true, "footer": true, "h1": true, "h2": true,
	"h3": true, "h4": true, "h5": true, "h6": true, "ul": true, "ol": true,
	"table": true, "blockquote": true, "pre": true, "hr": true, "nav": true,
}

// dropTags have contents that are not text, whatever they look like.
var dropTags = map[string]bool{"script": true, "style": true, "head": true, "svg": true}

// textOf turns HTML into the text a reader would see.
//
// A hand-rolled scanner rather than a parser: it does not need to build a tree
// to throw one away, and it keeps this module free of a dependency. It is
// deliberately crude — malformed markup degrades into slightly wrong
// whitespace, never into a failure.
func textOf(s string) string {
	var out strings.Builder
	var skipUntil string
	i := 0

	for i < len(s) {
		c := s[i]
		if c != '<' {
			if skipUntil == "" {
				// A newline in the source is whitespace. Only a block tag ends
				// a line, or a sentence wrapped in the markup becomes two.
				if c == '\n' || c == '\r' {
					c = ' '
				}
				out.WriteByte(c)
			}
			i++
			continue
		}

		// A comment runs to its own terminator, not to the next '>'.
		if strings.HasPrefix(s[i:], "<!--") {
			if end := strings.Index(s[i:], "-->"); end >= 0 {
				i += end + 3
				continue
			}
			break
		}

		end := strings.IndexByte(s[i:], '>')
		if end < 0 {
			break // an unterminated tag: the rest is not text
		}
		tag := s[i+1 : i+end]
		i += end + 1

		name := strings.ToLower(tagName(tag))
		switch {
		case skipUntil != "":
			if name == skipUntil && strings.HasPrefix(tag, "/") {
				skipUntil = ""
			}
		case dropTags[name] && !strings.HasPrefix(tag, "/"):
			skipUntil = name
		case blockTags[name]:
			out.WriteByte('\n')
		}
	}

	return tidy(html.UnescapeString(out.String()))
}

// tagName is the element name out of a tag's innards, closing slash and all.
func tagName(tag string) string {
	tag = strings.TrimPrefix(tag, "/")
	tag = strings.TrimPrefix(tag, "!")
	if cut := strings.IndexAny(tag, " \t\r\n/"); cut >= 0 {
		tag = tag[:cut]
	}
	return tag
}

// tidy collapses the whitespace HTML leaves behind: runs of spaces become one,
// runs of blank lines become one break, and every line is trimmed.
func tidy(s string) string {
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		if f := strings.Join(strings.Fields(line), " "); f != "" {
			lines = append(lines, f)
		}
	}
	return strings.Join(lines, "\n")
}
