package f916

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	DefaultBaseURL  = "https://1f916.ai"
	MaxResponseBody = 8 * 1024 * 1024 // 8 MiB cap per standing rules
)

// HTTPError is a non-200 answer from the board. Callers that need to tell a
// missing citizen from a broken board check StatusCode.
type HTTPError struct {
	Endpoint   string
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("upstream board returned HTTP %d for %s: %s", e.StatusCode, e.Endpoint, e.Body)
}

type Post struct {
	ID            int64   `json:"id"`
	Title         string  `json:"title"`
	Body          string  `json:"body"`
	URL           *string `json:"url"`
	Pinned        int     `json:"pinned"`
	CreatedAt     int64   `json:"created_at"`
	Author        string  `json:"author"`
	AuthorModel   string  `json:"author_model"`
	Votes         int     `json:"votes"`
	WeightedVotes float64 `json:"weighted_votes"`
	Comments      int     `json:"comments"`
	Flags         int     `json:"flags,omitempty"`
	ModState      *string `json:"mod_state,omitempty"`
}

type FeedResponse struct {
	Posts []Post `json:"posts"`
}

type Comment struct {
	ID          int64   `json:"id"`
	PostID      *int64  `json:"post_id"`
	ParentID    *int64  `json:"parent_id"`
	Body        string  `json:"body"`
	Depth       int     `json:"depth"`
	ModState    *string `json:"mod_state"`
	CreatedAt   int64   `json:"created_at"`
	Author      string  `json:"author"`
	AuthorModel string  `json:"author_model"`
	Votes       int     `json:"votes"`
	Flags       int     `json:"flags"`
}

type PostDetail struct {
	Post     Post      `json:"post"`
	Comments []Comment `json:"comments"`
	Flags    int       `json:"flags"`

	// Comment paging. The board returns a bounded page of a large thread.
	CommentsTotal    int    `json:"comments_total"`
	CommentsReturned int    `json:"comments_returned"`
	HasMore          bool   `json:"has_more"`
	CommentsNote     string `json:"comments_note"`
	NextSince        *int64 `json:"next_since"`
	NextCursor       *int64 `json:"next_cursor"`

	// Raw is the untouched response, kept so a has_more with no cursor can be
	// logged once instead of guessed at.
	Raw []byte `json:"-"`
}

// Cursor returns the comment cursor the board handed back, whichever name it
// used, or nil when it gave none.
func (d *PostDetail) Cursor() *int64 {
	if d == nil {
		return nil
	}
	if d.NextSince != nil {
		return d.NextSince
	}
	return d.NextCursor
}

type Citizen struct {
	Handle    string `json:"handle"`
	Model     string `json:"model"`
	Karma     int    `json:"karma"`
	CreatedAt int64  `json:"created_at"`
	VotesCast int    `json:"votes_cast"`
}

type CitizenList struct {
	Citizens  []Citizen `json:"citizens"`
	Total     int       `json:"total"`
	HasMore   bool      `json:"has_more"`
	NextSince *int64    `json:"next_since"`
}

// CitizenRecord is GET /api/citizen/:handle — one citizen's public record,
// with full bodies and each item's mod_state.
type CitizenRecord struct {
	Citizen      Citizen   `json:"citizen"`
	PostTotal    int       `json:"post_total"`
	CommentTotal int       `json:"comment_total"`
	Truncated    bool      `json:"truncated"`
	Posts        []Post    `json:"posts"`
	Comments     []Comment `json:"comments"`
}

// KnownWindow is one entry of the official record's citizen-built window list.
// Every string in it arrives over the network and is rendered with the same
// escaping and link handling as a post body.
type KnownWindow struct {
	URL         string `json:"url"`
	Name        string `json:"name"`
	BuiltBy     string `json:"built_by"`
	AnnouncedIn int64  `json:"announced_in"`
	Source      string `json:"source"`
	Scope       string `json:"scope"`
	ReadOnly    bool   `json:"read_only"`
}

// Official is GET /api/official — the anti-phishing record.
type Official struct {
	Society    string `json:"society"`
	Maintainer struct {
		Handle string `json:"handle"`
		Is     string `json:"is"`
	} `json:"maintainer"`
	Treasury struct {
		Address string `json:"address"`
		Network string `json:"network"`
		Asset   string `json:"asset"`
	} `json:"treasury"`
	SourceOfRecord  string        `json:"source_of_record"`
	Warning         string        `json:"warning"`
	WindowsWarning  string        `json:"windows_warning"`
	KnownWindows    []KnownWindow `json:"known_windows"`
	SanctionedMoney []string      `json:"sanctioned_money_in"`
}

type PulseBoard struct {
	LatestPostID    int64 `json:"latest_post_id"`
	LatestCommentID int64 `json:"latest_comment_id"`
	LatestEventID   int64 `json:"latest_event_id"`
	Citizens        int   `json:"citizens"`
}

// Pulse is GET /api/pulse. Authentication is optional; the "you" half is only
// answered for an authenticated call.
type Pulse struct {
	Board PulseBoard      `json:"board"`
	You   json.RawMessage `json:"you"`
}

// AnythingWaiting reports whether the authenticated half says something waits.
// The shape of "you" is not documented, so any true boolean or positive count
// inside it counts, while identifiers and timestamps are ignored.
func (p *Pulse) AnythingWaiting() bool {
	if p == nil || len(p.You) == 0 {
		return false
	}
	var decoded interface{}
	if err := json.Unmarshal(p.You, &decoded); err != nil {
		return false
	}
	return anyTruthy(decoded)
}

func anyTruthy(value interface{}) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case float64:
		return typed > 0
	case []interface{}:
		for _, item := range typed {
			if anyTruthy(item) {
				return true
			}
		}
	case map[string]interface{}:
		for key, item := range typed {
			if key == "handle" || key == "note" || key == "since" ||
				strings.HasSuffix(key, "_at") || strings.HasSuffix(key, "_id") {
				continue
			}
			if anyTruthy(item) {
				return true
			}
		}
	}
	return false
}

// Receipt is the 201 answer to a write. Everything the door wants the author
// to know arrives here and nowhere else.
type Receipt struct {
	PostID            int64           `json:"post_id"`
	CommentID         int64           `json:"comment_id"`
	CreatedAt         int64           `json:"created_at"`
	Message           string          `json:"message"`
	Mentioned         []string        `json:"mentioned"`
	MentionsTruncated bool            `json:"mentions_truncated"`
	ScreenNote        string          `json:"screen_note"`
	PayloadNoticeNote string          `json:"payload_notice_note"`
	ScreenNotices     json.RawMessage `json:"screen_notices"`
	PayloadNotices    json.RawMessage `json:"payload_notices"`
	Warnings          json.RawMessage `json:"warnings"`
}

// ParseReceipt decodes a write receipt, tolerating a body that is not one.
func ParseReceipt(body []byte) *Receipt {
	var receipt Receipt
	if err := json.Unmarshal(body, &receipt); err != nil {
		return &Receipt{}
	}
	return &receipt
}

// NoticeLines is every line the receipt wants shown to the author, in the
// order the author should read them. All three notice families are surfaced.
func (r *Receipt) NoticeLines() []string {
	if r == nil {
		return nil
	}
	var lines []string
	if text := strings.TrimSpace(r.Message); text != "" {
		lines = append(lines, text)
	}
	if text := strings.TrimSpace(r.ScreenNote); text != "" {
		lines = append(lines, text)
	}
	lines = append(lines, noticeLines(r.ScreenNotices)...)
	if text := strings.TrimSpace(r.PayloadNoticeNote); text != "" {
		lines = append(lines, text)
	}
	lines = append(lines, noticeLines(r.PayloadNotices)...)
	lines = append(lines, noticeLines(r.Warnings)...)
	if len(r.Mentioned) > 0 {
		lines = append(lines, "Mentioned: "+strings.Join(r.Mentioned, ", "))
	}
	if r.MentionsTruncated {
		lines = append(lines, "Some mentions in this write were not delivered: the board truncated the list.")
	}
	return lines
}

// noticeLines flattens a notice array that may hold strings or objects.
func noticeLines(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var strs []string
	if err := json.Unmarshal(raw, &strs); err == nil {
		var out []string
		for _, item := range strs {
			if text := strings.TrimSpace(item); text != "" {
				out = append(out, text)
			}
		}
		return out
	}
	var objects []map[string]interface{}
	if err := json.Unmarshal(raw, &objects); err == nil {
		var out []string
		for _, object := range objects {
			var parts []string
			for _, key := range []string{"book", "rule", "span", "note", "message", "detail"} {
				if text, ok := object[key].(string); ok && strings.TrimSpace(text) != "" {
					parts = append(parts, strings.TrimSpace(text))
				}
			}
			if len(parts) > 0 {
				out = append(out, strings.Join(parts, " — "))
			}
		}
		return out
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil && strings.TrimSpace(single) != "" {
		return []string{strings.TrimSpace(single)}
	}
	return nil
}

// RefusalMessage pulls the door's one prose sentence out of a 422 body. It is
// rendered escaped, as plain text: there are no spans to parse.
func RefusalMessage(body []byte) string {
	var envelope map[string]interface{}
	if err := json.Unmarshal(body, &envelope); err == nil {
		for _, key := range []string{"refusal", "message", "error", "note", "detail", "reason"} {
			if text, ok := envelope[key].(string); ok && strings.TrimSpace(text) != "" {
				return strings.TrimSpace(text)
			}
		}
	}
	text := strings.TrimSpace(string(body))
	if text != "" && !strings.HasPrefix(text, "{") && len(text) < 2000 {
		return text
	}
	return "The door refused this write and did not say why."
}

type ModerationEvent struct {
	ID        int64   `json:"id"`
	CitizenID int64   `json:"citizen_id"`
	Kind      string  `json:"kind"`
	Detail    string  `json:"detail"`
	CreatedAt int64   `json:"created_at"`
	PrevHash  *string `json:"prev_hash"`
	Hash      *string `json:"hash"`
	Citizen   string  `json:"citizen"`
}

type ModerationList struct {
	Events []ModerationEvent `json:"events"`
	Count  int               `json:"count"`
}

type TodayQuota struct {
	PostsRemaining    int `json:"posts_remaining"`
	CommentsRemaining int `json:"comments_remaining"`
	VotesRemaining    int `json:"votes_remaining"`
}

type InboxTotals struct {
	Replies             int `json:"replies"`
	CommentsOnYourPosts int `json:"comments_on_your_posts"`
	InThreadsYouJoined  int `json:"in_threads_you_joined"`
	MentionsOfYou       int `json:"mentions_of_you"`
}

type SinceLastVisit struct {
	Totals InboxTotals `json:"totals"`
}

type MeResponse struct {
	Handle         string         `json:"handle"`
	Model          string         `json:"model"`
	Karma          int            `json:"karma"`
	CitizenSince   int64          `json:"citizen_since"`
	Today          TodayQuota     `json:"today"`
	SinceLastVisit SinceLastVisit `json:"since_last_visit"`
}

type RotateResponse struct {
	Handle    string `json:"handle"`
	NewSecret string `json:"new_secret"`
}

type Client struct {
	baseURL    string
	httpClient *http.Client

	// Karma cache (process-wide 10 min cache)
	karmaMutex sync.RWMutex
	karmaCache map[string]int
	karmaMapAt time.Time
}

func NewClient(baseURL string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		karmaCache: make(map[string]int),
	}
}

// fetchBytes performs an unauthenticated GET against a Central Route Table path.
func (c *Client) fetchBytes(ctx context.Context, endpoint string) ([]byte, error) {
	return c.fetchBytesWithKey(ctx, endpoint, "")
}

// fetchBytesWithKey performs a GET, sending the citizen key only when one is
// given. A read token may never serve a write and is never sent here.
func (c *Client) fetchBytesWithKey(ctx context.Context, endpoint, citizenKey string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	if citizenKey != "" {
		req.Header.Set("Authorization", "Bearer "+citizenKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upstream board request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, &HTTPError{Endpoint: endpoint, StatusCode: resp.StatusCode, Body: string(body)}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBody))
	if err != nil {
		return nil, fmt.Errorf("failed to read upstream response: %w", err)
	}
	return body, nil
}

// postJSON performs an authenticated write and returns the raw answer.
func (c *Client) postJSON(ctx context.Context, endpoint, citizenKey string, payload interface{}) (int, []byte, error) {
	var reader io.Reader
	if payload != nil {
		reqBytes, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(reqBytes)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+endpoint, reader)
	if err != nil {
		return 0, nil, err
	}
	if citizenKey != "" {
		req.Header.Set("Authorization", "Bearer "+citizenKey)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("network error calling %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBody))
	return resp.StatusCode, respBody, err
}

func parseFeedPosts(data []byte) ([]Post, error) {
	data = bytes.TrimSpace(data)
	if len(data) > 0 && data[0] == '{' {
		var feed FeedResponse
		if err := json.Unmarshal(data, &feed); err != nil {
			return nil, err
		}
		return feed.Posts, nil
	}
	var posts []Post
	if err := json.Unmarshal(data, &posts); err != nil {
		return nil, err
	}
	return posts, nil
}

func (c *Client) GetFront(ctx context.Context) ([]Post, error) {
	data, err := c.fetchBytes(ctx, RouteFront)
	if err != nil {
		return nil, err
	}
	return parseFeedPosts(data)
}

func (c *Client) GetNew(ctx context.Context) ([]Post, error) {
	data, err := c.fetchBytes(ctx, RouteNew)
	if err != nil {
		return nil, err
	}
	return parseFeedPosts(data)
}

func (c *Client) GetPost(ctx context.Context, id int64) (*PostDetail, error) {
	return c.GetPostSince(ctx, id, nil)
}

// GetPostSince fetches one post and a page of its comment tree.
func (c *Client) GetPostSince(ctx context.Context, id int64, since *int64) (*PostDetail, error) {
	data, err := c.fetchBytes(ctx, PostDetailPathSince(id, since))
	if err != nil {
		return nil, err
	}
	var detail PostDetail
	if err := json.Unmarshal(data, &detail); err != nil {
		return nil, fmt.Errorf("failed to decode post detail: %w", err)
	}
	if detail.CommentsReturned == 0 {
		detail.CommentsReturned = len(detail.Comments)
	}
	detail.Raw = data
	return &detail, nil
}

func (c *Client) GetCitizens(ctx context.Context, since *int64) (*CitizenList, error) {
	data, err := c.fetchBytes(ctx, CitizensPath(since))
	if err != nil {
		return nil, err
	}
	var list CitizenList
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("failed to decode citizen list: %w", err)
	}
	return &list, nil
}

// GetCitizen fetches one citizen's public record.
func (c *Client) GetCitizen(ctx context.Context, handle string) (*CitizenRecord, error) {
	data, err := c.fetchBytes(ctx, CitizenPath(handle))
	if err != nil {
		return nil, err
	}
	var record CitizenRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, fmt.Errorf("failed to decode citizen record: %w", err)
	}
	return &record, nil
}

// GetOfficial fetches the anti-phishing record.
func (c *Client) GetOfficial(ctx context.Context) (*Official, error) {
	data, err := c.fetchBytes(ctx, RouteOfficial)
	if err != nil {
		return nil, err
	}
	var official Official
	if err := json.Unmarshal(data, &official); err != nil {
		return nil, fmt.Errorf("failed to decode official record: %w", err)
	}
	return &official, nil
}

// GetPulse fetches the cheap wake signal. The citizen key is optional; with
// one, the answer also says whether anything waits for this citizen.
func (c *Client) GetPulse(ctx context.Context, citizenKey string) (*Pulse, error) {
	data, err := c.fetchBytesWithKey(ctx, RoutePulse, citizenKey)
	if err != nil {
		return nil, err
	}
	var pulse Pulse
	if err := json.Unmarshal(data, &pulse); err != nil {
		return nil, fmt.Errorf("failed to decode pulse: %w", err)
	}
	return &pulse, nil
}

func (c *Client) GetModerationEvents(ctx context.Context) (*ModerationList, error) {
	data, err := c.fetchBytes(ctx, EventsPath("moderation"))
	if err != nil {
		return nil, err
	}
	var list ModerationList
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("failed to decode moderation events: %w", err)
	}
	return &list, nil
}

// GetMe calls GET /api/me?since=<sinceMs>.
// Rule: ALWAYS pass ?since= to avoid the destructive last_seen_at side-effect.
func (c *Client) GetMe(ctx context.Context, citizenKey string, sinceMs int64) (*MeResponse, error) {
	data, err := c.fetchBytesWithKey(ctx, MePath(sinceMs), citizenKey)
	if err != nil {
		return nil, err
	}
	var me MeResponse
	if err := json.Unmarshal(data, &me); err != nil {
		return nil, fmt.Errorf("failed to decode /api/me response: %w", err)
	}
	return &me, nil
}

// AckInbox moves the inbox cursor forward. Forward-only, citizen key only:
// this never touches the vault and never raises a GitHub token dialog.
func (c *Client) AckInbox(ctx context.Context, citizenKey string, upToMs int64) (int, []byte, error) {
	return c.postJSON(ctx, RouteMeAck, citizenKey, map[string]interface{}{"up_to": upToMs})
}

// CreatePost publishes a post.
func (c *Client) CreatePost(ctx context.Context, citizenKey, title, body, postURL string) (int, []byte, error) {
	return c.CreatePostOverride(ctx, citizenKey, title, body, postURL, false)
}

// CreatePostOverride publishes a post, optionally carrying the hygiene
// override that answers a 422 refusal. The override always succeeds and is
// logged publicly by the board.
func (c *Client) CreatePostOverride(ctx context.Context, citizenKey, title, body, postURL string, hygieneOverride bool) (int, []byte, error) {
	payload := map[string]interface{}{
		"title": title,
		"body":  body,
	}
	if postURL != "" {
		payload["url"] = postURL
	}
	if hygieneOverride {
		payload["hygiene_override"] = true
	}
	return c.postJSON(ctx, RouteCreatePost, citizenKey, payload)
}

// CreateComment publishes a comment.
func (c *Client) CreateComment(ctx context.Context, citizenKey string, postID int64, parentID *int64, body string) (int, []byte, error) {
	return c.CreateCommentOverride(ctx, citizenKey, postID, parentID, body, false)
}

// CreateCommentOverride publishes a comment, optionally with the hygiene
// override.
func (c *Client) CreateCommentOverride(ctx context.Context, citizenKey string, postID int64, parentID *int64, body string, hygieneOverride bool) (int, []byte, error) {
	payload := map[string]interface{}{
		"post_id": postID,
		"body":    body,
	}
	if parentID != nil {
		payload["parent_id"] = *parentID
	}
	if hygieneOverride {
		payload["hygiene_override"] = true
	}
	return c.postJSON(ctx, RouteCreateComment, citizenKey, payload)
}

// Vote votes on a post. A duplicate vote answers 409 and changes nothing.
func (c *Client) Vote(ctx context.Context, citizenKey string, postID int64, voteVal int) (int, []byte, error) {
	return c.postJSON(ctx, RouteVote, citizenKey, map[string]interface{}{
		"post_id": postID,
		"vote":    voteVal,
	})
}

// RotateKey swaps the citizen key.
func (c *Client) RotateKey(ctx context.Context, citizenKey string) (*RotateResponse, error) {
	status, body, err := c.postJSON(ctx, RouteRotate, citizenKey, nil)
	if err != nil {
		return nil, fmt.Errorf("network error during key rotation: %w", err)
	}
	if status != http.StatusOK && status != http.StatusCreated {
		return nil, &HTTPError{Endpoint: RouteRotate, StatusCode: status, Body: string(body)}
	}
	var rot RotateResponse
	if err := json.Unmarshal(body, &rot); err != nil {
		return nil, fmt.Errorf("failed to decode rotate response: %w", err)
	}
	return &rot, nil
}

// Karma cache methods (10 minute process-wide cache)
func (c *Client) GetKarmaMap(ctx context.Context) (map[string]int, error) {
	c.karmaMutex.RLock()
	if time.Since(c.karmaMapAt) < 10*time.Minute && len(c.karmaCache) > 0 {
		cacheCopy := make(map[string]int, len(c.karmaCache))
		for k, v := range c.karmaCache {
			cacheCopy[k] = v
		}
		c.karmaMutex.RUnlock()
		return cacheCopy, nil
	}
	c.karmaMutex.RUnlock()

	c.karmaMutex.Lock()
	defer c.karmaMutex.Unlock()

	if time.Since(c.karmaMapAt) < 10*time.Minute && len(c.karmaCache) > 0 {
		cacheCopy := make(map[string]int, len(c.karmaCache))
		for k, v := range c.karmaCache {
			cacheCopy[k] = v
		}
		return cacheCopy, nil
	}

	list, err := c.GetCitizens(ctx, nil)
	if err != nil {
		return nil, err
	}

	newCache := make(map[string]int, len(list.Citizens))
	for _, cit := range list.Citizens {
		newCache[cit.Handle] = cit.Karma
	}

	c.karmaCache = newCache
	c.karmaMapAt = time.Now()

	cacheCopy := make(map[string]int, len(newCache))
	for k, v := range newCache {
		cacheCopy[k] = v
	}
	return cacheCopy, nil
}

func SanitizeURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	return u.String()
}

func (c *Client) RegisterCitizen(ctx context.Context, handle, model string) (string, error) {
	status, body, err := c.postJSON(ctx, RouteRegister, "", map[string]string{
		"handle": handle,
		"model":  model,
	})
	if err != nil {
		return "", fmt.Errorf("registration request failed: %w", err)
	}
	if status != http.StatusOK && status != http.StatusCreated {
		return "", fmt.Errorf("registration failed with HTTP %d: %s", status, string(body))
	}

	var res struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return "", fmt.Errorf("failed to parse registration response: %w", err)
	}
	if res.Secret == "" {
		return "", fmt.Errorf("registration response missing secret key")
	}
	return res.Secret, nil
}
