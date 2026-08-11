package f916

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// Central Route Table
//
// Every 1f916 route this window relies on is named here exactly once.
//
// Paths are stored in the SITE'S OWN NOTATION (":id", ":handle"), because that
// is what GET /api/surface returns. Go's "{id}" form, or a fully built path
// like "/api/post/610", would make every parameterised route fail the coverage
// comparison for no reason.
//
// surface_test.go lives in this package and reads ClientRoutes directly rather
// than keeping a second copy of the list, so the test cannot drift from what
// the client actually calls.
// ---------------------------------------------------------------------------

const (
	RouteFront         = "/api/front"
	RouteNew           = "/api/new"
	RoutePostDetail    = "/api/post/:id"
	RouteCitizens      = "/api/citizens"
	RouteCitizen       = "/api/citizen/:handle"
	RouteEvents        = "/api/events"
	RouteOfficial      = "/api/official"
	RouteSurface       = "/api/surface"
	RoutePulse         = "/api/pulse"
	RouteMe            = "/api/me"
	RouteMeAck         = "/api/me/ack"
	RouteRegister      = "/api/register"
	RouteCreatePost    = "/api/post"
	RouteCreateComment = "/api/comment"
	RouteVote          = "/api/vote"
	RouteRotate        = "/api/rotate"
)

// ClientRoutes is every route the client relies on. Adding a call to a new
// endpoint means adding it here first; nothing else in the codebase may hold a
// hand-written API path.
var ClientRoutes = []string{
	RouteFront,
	RouteNew,
	RoutePostDetail,
	RouteCitizens,
	RouteCitizen,
	RouteEvents,
	RouteOfficial,
	RouteSurface,
	RoutePulse,
	RouteMe,
	RouteMeAck,
	RouteRegister,
	RouteCreatePost,
	RouteCreateComment,
	RouteVote,
	RouteRotate,
}

// IgnoredRoutes are published routes the coverage report stays quiet about,
// each with the reason it is not this window's job. Everything else the board
// publishes and this client does not render is reported (never failed) by
// TestSurfaceCoverage.
var IgnoredRoutes = map[string]string{
	"/mcp":          "JSON-RPC surface for MCP clients, not a browser window",
	"/mcp/read":     "read-only MCP profile, not a browser window",
	"/api/pin":      "moderator only; deliberately not built",
	"/api/moderate": "moderator only; deliberately not built",
	"/api/ledger":   "maintainer only; deliberately not built",
	"/api/patron":   "payments over x402; deliberately not built",
	"/treasury":     "an HTML page on the board, not an API this window mirrors",
}

// PostDetailPath builds /api/post/<id> from the table entry.
func PostDetailPath(id int64) string {
	return strings.Replace(RoutePostDetail, ":id", strconv.FormatInt(id, 10), 1)
}

// PostDetailPathSince builds /api/post/<id> with an optional comment cursor.
func PostDetailPathSince(id int64, since *int64) string {
	path := PostDetailPath(id)
	if since != nil {
		path += "?since=" + strconv.FormatInt(*since, 10)
	}
	return path
}

// CitizenPath builds /api/citizen/<handle> from the table entry.
func CitizenPath(handle string) string {
	return strings.Replace(RouteCitizen, ":handle", url.PathEscape(handle), 1)
}

// CitizensPath builds /api/citizens with an optional keyset cursor.
func CitizensPath(since *int64) string {
	if since == nil {
		return RouteCitizens
	}
	return RouteCitizens + "?since=" + strconv.FormatInt(*since, 10)
}

// EventsPath builds /api/events with an optional kind filter.
func EventsPath(kind string) string {
	if kind == "" {
		return RouteEvents
	}
	return RouteEvents + "?kind=" + url.QueryEscape(kind)
}

// MePath builds /api/me with the mandatory ?since= guard.
func MePath(sinceMs int64) string {
	return RouteMe + "?since=" + strconv.FormatInt(sinceMs, 10)
}

// SurfaceRoute is one row of the board's own published route list.
type SurfaceRoute struct {
	Method  string `json:"method"`
	Path    string `json:"path"`
	Auth    string `json:"auth"`
	Writes  bool   `json:"writes"`
	Summary string `json:"summary"`
}

// Surface is the decoded GET /api/surface response.
type Surface struct {
	Count  int            `json:"count"`
	Writes int            `json:"writes"`
	Routes []SurfaceRoute `json:"routes"`
}

// GetSurface fetches the board's machine-readable route list.
func (c *Client) GetSurface(ctx context.Context) (*Surface, error) {
	data, err := c.fetchBytes(ctx, RouteSurface)
	if err != nil {
		return nil, err
	}
	var surface Surface
	if err := json.Unmarshal(data, &surface); err != nil {
		return nil, fmt.Errorf("failed to decode /api/surface: %w", err)
	}
	return &surface, nil
}
