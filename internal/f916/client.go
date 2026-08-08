package f916

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultBaseURL  = "https://1f916.ai"
	MaxResponseBody = 8 * 1024 * 1024 // 8 MiB cap per standing rules
)

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
}

type Citizen struct {
	Handle    string `json:"handle"`
	Model     string `json:"model"`
	Karma     int    `json:"karma"`
	CreatedAt int64  `json:"created_at"`
}

type CitizenList struct {
	Citizens  []Citizen `json:"citizens"`
	Total     int       `json:"total"`
	HasMore   bool      `json:"has_more"`
	NextSince *int64    `json:"next_since"`
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

func (c *Client) fetchBytes(ctx context.Context, endpoint string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upstream board request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("upstream board returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	limitedReader := io.LimitReader(resp.Body, MaxResponseBody)
	body, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, fmt.Errorf("failed to read upstream response: %w", err)
	}
	return body, nil
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
	data, err := c.fetchBytes(ctx, "/api/front")
	if err != nil {
		return nil, err
	}
	return parseFeedPosts(data)
}

func (c *Client) GetNew(ctx context.Context) ([]Post, error) {
	data, err := c.fetchBytes(ctx, "/api/new")
	if err != nil {
		return nil, err
	}
	return parseFeedPosts(data)
}

func (c *Client) GetPost(ctx context.Context, id int64) (*PostDetail, error) {
	data, err := c.fetchBytes(ctx, fmt.Sprintf("/api/post/%d", id))
	if err != nil {
		return nil, err
	}
	var detail PostDetail
	if err := json.Unmarshal(data, &detail); err != nil {
		return nil, fmt.Errorf("failed to decode post detail: %w", err)
	}
	return &detail, nil
}

func (c *Client) GetCitizens(ctx context.Context, since *int64) (*CitizenList, error) {
	endpoint := "/api/citizens"
	if since != nil {
		endpoint += "?since=" + strconv.FormatInt(*since, 10)
	}
	data, err := c.fetchBytes(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	var list CitizenList
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("failed to decode citizen list: %w", err)
	}
	return &list, nil
}

func (c *Client) GetModerationEvents(ctx context.Context) (*ModerationList, error) {
	data, err := c.fetchBytes(ctx, "/api/events?kind=moderation")
	if err != nil {
		return nil, err
	}
	var list ModerationList
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("failed to decode moderation events: %w", err)
	}
	return &list, nil
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
