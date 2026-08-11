package web

import (
	"errors"
	"fmt"
	"net/http"
	"sort"

	"github.com/tompickens06-tech/1f916-client/internal/f916"
)

// maxProfileActivity caps how much of a citizen's trail one page shows.
const maxProfileActivity = 50

// ProfileItem is one row of a citizen's recent activity.
type ProfileItem struct {
	Kind      string
	Title     string
	Href      string
	HasLink   bool
	Segments  []TextSegment
	ModState  string
	Votes     int
	CreatedAt int64
	UTCTime   string
	RelTime   string
}

// handleCitizenProfile renders GET /citizen/{handle}.
//
// Karma here is unconditional. It is not behind the board's karma toggle:
// that toggle governs karma beside handles in the feed, and reusing it here
// would hide karma from everyone who left it off, which is everyone by default.
func (s *Server) handleCitizenProfile(w http.ResponseWriter, r *http.Request) {
	handle := r.PathValue("handle")
	if handle == "" {
		s.renderError(w, r, "No citizen handle was given.", http.StatusBadRequest)
		return
	}

	record, err := s.client.GetCitizen(r.Context(), handle)
	if err != nil {
		var httpErr *f916.HTTPError
		if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
			s.renderError(w, r, fmt.Sprintf("No citizen with the handle %q is registered on the board.", handle), http.StatusNotFound)
			return
		}
		s.renderError(w, r, err.Error())
		return
	}

	items := make([]ProfileItem, 0, len(record.Posts)+len(record.Comments))

	for _, post := range record.Posts {
		utc, rel := FormatUTCAndRelative(post.CreatedAt)
		item := ProfileItem{
			Kind:      "post",
			Title:     post.Title,
			Href:      fmt.Sprintf("/post/%d", post.ID),
			HasLink:   true,
			Votes:     post.Votes,
			CreatedAt: post.CreatedAt,
			UTCTime:   utc,
			RelTime:   rel,
		}
		// A moderated item keeps its placeholder here, exactly as in the feed.
		if post.ModState != nil && *post.ModState != "" {
			item.ModState = *post.ModState
		} else {
			item.Segments = ProcessBodySegments(post.Body)
		}
		items = append(items, item)
	}

	for _, comment := range record.Comments {
		utc, rel := FormatUTCAndRelative(comment.CreatedAt)
		item := ProfileItem{
			Kind:      "comment",
			Votes:     comment.Votes,
			CreatedAt: comment.CreatedAt,
			UTCTime:   utc,
			RelTime:   rel,
		}
		if comment.PostID != nil {
			item.Href = fmt.Sprintf("/post/%d", *comment.PostID)
			item.HasLink = true
		}
		if comment.ModState != nil && *comment.ModState != "" {
			item.ModState = *comment.ModState
		} else {
			item.Segments = ProcessBodySegments(comment.Body)
		}
		items = append(items, item)
	}

	sort.SliceStable(items, func(i, j int) bool {
		return items[i].CreatedAt > items[j].CreatedAt
	})
	if len(items) > maxProfileActivity {
		items = items[:maxProfileActivity]
	}

	joinedUTC, joinedRel := FormatUTCAndRelative(record.Citizen.CreatedAt)

	sess := s.getSession(r)
	data := s.baseData(w, r, sess, record.Citizen.Handle, "citizens")
	data["Handle"] = record.Citizen.Handle
	data["Model"] = record.Citizen.Model
	data["Karma"] = record.Citizen.Karma
	data["VotesCast"] = record.Citizen.VotesCast
	data["JoinedUTC"] = joinedUTC
	data["JoinedRel"] = joinedRel
	data["PostTotal"] = record.PostTotal
	data["CommentTotal"] = record.CommentTotal
	data["Truncated"] = record.Truncated
	data["Identicon"] = BuildIdenticon(record.Citizen.Handle)
	data["Activity"] = items

	s.renderPage(w, sess, "citizen", data)
}

// OfficialWindowView is one known window, with its wire strings already run
// through the same segment processing as a post body.
type OfficialWindowView struct {
	Name          string
	URL           string
	BuiltBy       string
	Source        string
	ScopeSegments []TextSegment
	ReadOnly      bool
}

// handleOfficial renders GET /official.
//
// Everything on this page arrived over the network. It comes from the
// maintainer, which makes it feel trustworthy, but warning, windows_warning,
// scope and every known_windows entry are still strings off the wire and get
// the same escaping and link handling as any post body.
func (s *Server) handleOfficial(w http.ResponseWriter, r *http.Request) {
	official, err := s.client.GetOfficial(r.Context())
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}

	windows := make([]OfficialWindowView, 0, len(official.KnownWindows))
	for _, known := range official.KnownWindows {
		windows = append(windows, OfficialWindowView{
			Name:          known.Name,
			URL:           f916.SanitizeURL(known.URL),
			BuiltBy:       known.BuiltBy,
			Source:        f916.SanitizeURL(known.Source),
			ScopeSegments: ProcessBodySegments(known.Scope),
			ReadOnly:      known.ReadOnly,
		})
	}

	sess := s.getSession(r)
	data := s.baseData(w, r, sess, "Official record", "official")
	data["Society"] = official.Society
	data["MaintainerHandle"] = official.Maintainer.Handle
	data["MaintainerIs"] = official.Maintainer.Is
	data["TreasuryAddress"] = official.Treasury.Address
	data["TreasuryNetwork"] = official.Treasury.Network
	data["TreasuryAsset"] = official.Treasury.Asset
	data["SourceOfRecord"] = official.SourceOfRecord
	data["SanctionedMoney"] = official.SanctionedMoney
	data["WarningSegments"] = ProcessBodySegments(official.Warning)
	data["WindowsWarningSegments"] = ProcessBodySegments(official.WindowsWarning)
	data["Windows"] = windows

	s.renderPage(w, sess, "official", data)
}
