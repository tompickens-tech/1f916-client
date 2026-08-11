package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tompickens06-tech/1f916-client/internal/f916"
	webassets "github.com/tompickens06-tech/1f916-client/web"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	// NewServer parses every page in the pages slice. A page named there with
	// no template file fails right here, at test time, instead of when someone
	// finally requests that URL.
	srv, err := NewServer(f916.NewClient(""), "", "")
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	return srv
}

// TestMuxCoverage asserts every route this window serves is actually wired.
// It runs entirely offline: no handler is invoked, only resolved.
func TestMuxCoverage(t *testing.T) {
	srv := newTestServer(t)

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	routes := []struct{ method, path string }{
		{"GET", "/"},
		{"GET", "/new"},
		{"GET", "/post/610"},
		{"GET", "/post/610/thread/42"},
		{"GET", "/citizens"},
		{"GET", "/citizen/1f916-agent"},
		{"GET", "/official"},
		{"GET", "/events"},
		{"GET", "/verify"},
		{"GET", "/toggle-karma"},
		{"GET", "/login"},
		{"POST", "/login"},
		{"GET", "/register"},
		{"POST", "/register"},
		{"GET", "/recovery"},
		{"POST", "/recovery"},
		{"GET", "/write-token"},
		{"POST", "/write-token"},
		{"POST", "/acknowledge-key"},
		{"GET", "/download-recovery"},
		{"GET", "/logout"},
		{"POST", "/logout"},
		{"GET", "/compose"},
		{"POST", "/compose/preview"},
		{"POST", "/compose/publish"},
		{"POST", "/comment"},
		{"POST", "/vote"},
		{"GET", "/inbox"},
		{"POST", "/inbox/ack"},
		{"GET", "/rotate"},
		{"POST", "/rotate"},
		{"GET", "/static/style.css"},
		{"GET", "/healthz"},
		{"GET", "/favicon.ico"},
	}

	for _, route := range routes {
		req := httptest.NewRequest(route.method, "http://127.0.0.1"+route.path, nil)
		handler, pattern := mux.Handler(req)
		if handler == nil || pattern == "" {
			t.Errorf("no handler registered for %s %s", route.method, route.path)
			continue
		}
		// "GET /" matches any unmatched GET path, so a GET route that resolves
		// to the catch-all is not really registered.
		if route.method == "GET" && route.path != "/" && pattern == "GET /" {
			t.Errorf("GET %s fell through to the front-page catch-all", route.path)
		}
	}
}

// TestEveryTemplateFileIsRegistered catches the other direction: a template
// file that exists but that no page in the pages slice ever loads.
func TestEveryTemplateFileIsRegistered(t *testing.T) {
	srv := newTestServer(t)

	entries, err := fs.ReadDir(webassets.TemplateFS, "templates")
	if err != nil {
		t.Fatalf("reading templates: %v", err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".html") {
			continue
		}
		page := strings.TrimSuffix(name, ".html")
		if page == "layout" {
			continue
		}
		if _, ok := srv.templates[page]; !ok {
			t.Errorf("template file %s is never loaded: add %q to the pages slice or delete the file", name, page)
		}
	}
}
