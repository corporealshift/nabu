package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const page = `<!doctype html>
<html><head>
<title>Go 1.26</title>
<style>body { color: red }</style>
<script>console.log("not text")</script>
</head>
<body>
<h1>Go 1.26</h1>
<p>The garbage collector was <strong>rewritten</strong>.</p>
<ul><li>Faster</li><li>Smaller</li></ul>
<!-- a comment -->
<p>See &amp; read the <a href="/dl/">downloads</a>.</p>
</body></html>`

func TestTextOfStripsMarkup(t *testing.T) {
	got := textOf(page)

	for _, want := range []string{"Go 1.26", "garbage collector was rewritten", "Faster", "See & read the downloads"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"<p>", "console.log", "color: red", "a comment", "doctype"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("kept %q in:\n%s", unwanted, got)
		}
	}
}

// Blocks become separate lines; a sentence broken over source lines does not.
func TestTextOfKeepsBlockBreaks(t *testing.T) {
	got := textOf("<p>one</p><p>two</p>")
	if got != "one\ntwo" {
		t.Errorf("got %q, want %q", got, "one\ntwo")
	}

	got = textOf("<p>a sentence\nbroken over lines</p>")
	if got != "a sentence broken over lines" {
		t.Errorf("got %q", got)
	}
}

func TestFetchReturnsText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	}))
	defer srv.Close()

	out, err := fetchURL(context.Background(), srv.Client(), srv.URL, 1<<20)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if !strings.Contains(out, "garbage collector") {
		t.Errorf("got %q", out)
	}
}

// A page bigger than the cap is cut, not refused: the top of a long article is
// usually what was wanted.
func TestFetchCapsTheBody(t *testing.T) {
	long := "<p>" + strings.Repeat("word ", 5000) + "</p>"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(long))
	}))
	defer srv.Close()

	out, err := fetchURL(context.Background(), srv.Client(), srv.URL, 512)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(out) > 700 {
		t.Errorf("body was not capped: %d bytes", len(out))
	}
}

// Only http and https. A tool that reads file:// is a way to exfiltrate the
// disk through a page the model was told to read.
func TestFetchRefusesOtherSchemes(t *testing.T) {
	for _, u := range []string{"file:///c:/windows/win.ini", "ftp://example.com/x", "notaurl"} {
		if _, err := fetchURL(context.Background(), http.DefaultClient, u, 1024); err == nil {
			t.Errorf("%q should have been refused", u)
		}
	}
}

func TestFetchReportsAnHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if _, err := fetchURL(context.Background(), srv.Client(), srv.URL, 1024); err == nil {
		t.Fatal("a 404 should be an error")
	}
}
