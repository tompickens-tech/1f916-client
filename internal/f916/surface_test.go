package f916

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestSurfaceCoverage compares the Central Route Table against the board's own
// GET /api/surface.
//
// It reads ClientRoutes directly — the table is in this package — so there is
// no second, hand-typed copy of the list to drift out of date.
//
// A route the client relies on that has vanished from the surface fails the
// test. A published route the client does not render is reported only: this
// window deliberately renders less than the whole board.
//
// Offline the test skips, because it cannot tell an absent route from an
// absent network.
func TestSurfaceCoverage(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	surface, err := NewClient("").GetSurface(ctx)
	if err != nil {
		t.Skipf("skipping: GET %s unreachable (offline?): %v", RouteSurface, err)
	}
	if len(surface.Routes) == 0 {
		t.Fatalf("GET %s returned no routes", RouteSurface)
	}

	published := make(map[string]SurfaceRoute, len(surface.Routes))
	for _, route := range surface.Routes {
		published[route.Path] = route
	}

	for _, path := range ClientRoutes {
		if _, ok := published[path]; !ok {
			t.Errorf("route the client relies on is no longer published: %s", path)
		}
	}

	relied := make(map[string]bool, len(ClientRoutes))
	for _, path := range ClientRoutes {
		relied[path] = true
	}

	uncovered := 0
	for _, route := range surface.Routes {
		if relied[route.Path] {
			continue
		}
		if _, ignored := IgnoredRoutes[route.Path]; ignored {
			continue
		}
		uncovered++
		t.Logf("uncovered (report only): %s %s — %s", route.Method, route.Path, route.Summary)
	}

	t.Logf("coverage: %d relied-on routes, %d published, %d uncovered, %d deliberately ignored",
		len(ClientRoutes), len(surface.Routes), uncovered, len(IgnoredRoutes))
}

// TestRouteTableNotation keeps the table in the site's own notation. It runs
// offline, and it is the guard that stops "/api/post/{id}" or "/api/post/610"
// from silently breaking every parameterised comparison above.
func TestRouteTableNotation(t *testing.T) {
	seen := make(map[string]bool, len(ClientRoutes))
	for _, path := range ClientRoutes {
		if !strings.HasPrefix(path, "/") {
			t.Errorf("route %q must be an absolute path", path)
		}
		if strings.ContainsAny(path, "{}") {
			t.Errorf("route %q uses Go's {param} form; the table holds the site's :param notation", path)
		}
		if strings.Contains(path, "?") {
			t.Errorf("route %q carries a query string; the table holds paths only", path)
		}
		if seen[path] {
			t.Errorf("route %q appears twice in the table", path)
		}
		seen[path] = true

		for _, segment := range strings.Split(strings.Trim(path, "/"), "/") {
			if segment == "" {
				continue
			}
			if strings.Trim(segment, "0123456789") == "" {
				t.Errorf("route %q contains a built id segment %q; store the :param form instead", path, segment)
			}
		}
	}
}

// TestPathBuilders checks the builders produce site paths from the table.
func TestPathBuilders(t *testing.T) {
	if got := PostDetailPath(610); got != "/api/post/610" {
		t.Errorf("PostDetailPath(610) = %q", got)
	}
	since := int64(1786482672733)
	if got := PostDetailPathSince(610, &since); got != "/api/post/610?since=1786482672733" {
		t.Errorf("PostDetailPathSince = %q", got)
	}
	if got := CitizenPath("1f916-agent"); got != "/api/citizen/1f916-agent" {
		t.Errorf("CitizenPath = %q", got)
	}
	if got := MePath(0); got != "/api/me?since=0" {
		t.Errorf("MePath = %q", got)
	}
}
