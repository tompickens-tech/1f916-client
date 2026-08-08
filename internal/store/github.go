package store

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"strings"
	"time"
)

const (
	CommitMessage = "update vault storage"
	MaxGitHubBody = 1024 * 1024 // 1 MiB cap per standing rules
)

type RepoProbeResponse struct {
	Name        string `json:"name"`
	Private     bool   `json:"private"`
	Permissions struct {
		Admin bool `json:"admin"`
		Push  bool `json:"push"`
		Pull  bool `json:"pull"`
	} `json:"permissions"`
}

type ContentMetadataResponse struct {
	Name string `json:"name"`
	Path string `json:"path"`
	SHA  string `json:"sha"`
	Size int    `json:"size"`
	Type string `json:"type"`
}

type PutContentRequest struct {
	Message string `json:"message"`
	Content string `json:"content"`
	SHA     string `json:"sha,omitempty"`
}

type Client struct {
	httpClient *http.Client
}

func NewClient() *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func NormalizeRepo(repo string) string {
	repo = strings.TrimSpace(repo)
	repo = strings.TrimPrefix(repo, "https://github.com/")
	repo = strings.TrimPrefix(repo, "github.com/")
	return strings.Trim(repo, "/")
}

// ProbeRepo runs GET /repos/{owner}/{repo}.
// Returns probe response or error (404 means token cannot see repo).
func (c *Client) ProbeRepo(ctx context.Context, repo, token string) (*RepoProbeResponse, error) {
	repoPath := NormalizeRepo(repo)
	url := fmt.Sprintf("https://api.github.com/repos/%s", repoPath)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("network error reaching GitHub API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("repo 404: token cannot see repository %s (check token scope or repository name)", repoPath)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("GitHub API returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	limitedReader := io.LimitReader(resp.Body, MaxGitHubBody)
	var probe RepoProbeResponse
	if err := json.NewDecoder(limitedReader).Decode(&probe); err != nil {
		return nil, fmt.Errorf("failed to decode repo probe response: %w", err)
	}

	return &probe, nil
}

// GetBlob fetches raw bytes from GET /repos/{owner}/{repo}/contents/v/{locator}.bin.
// Uses 404 disambiguation probe if blob 404s.
func (c *Client) GetBlob(ctx context.Context, repo, token, locator string) ([]byte, string, error) {
	repoPath := NormalizeRepo(repo)
	blobURL := fmt.Sprintf("https://api.github.com/repos/%s/contents/v/%s.bin", repoPath, locator)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, blobURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github.raw")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("network error fetching blob: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		// Blob 404 -> Run probe to disambiguate bad token vs non-existent blob
		_, probeErr := c.ProbeRepo(ctx, repo, token)
		if probeErr != nil {
			// Probe failed -> Bad token!
			return nil, "", probeErr
		}
		// Probe succeeded -> Blob genuinely not found (wrong email or password!)
		return nil, "", fmt.Errorf("blob 404: locator not found (wrong email or password)")
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, "", fmt.Errorf("GitHub API returned HTTP %d fetching blob: %s", resp.StatusCode, string(body))
	}

	// Fetch ETag or SHA if header present
	etag := resp.Header.Get("ETag")

	limitedReader := io.LimitReader(resp.Body, MaxGitHubBody)
	blobData, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read blob content: %w", err)
	}

	return blobData, etag, nil
}

// GetBlobMetadata fetches metadata JSON (including SHA) for existing blob.
func (c *Client) GetBlobMetadata(ctx context.Context, repo, token, path string) (*ContentMetadataResponse, error) {
	repoPath := NormalizeRepo(repo)
	url := fmt.Sprintf("https://api.github.com/repos/%s/contents/%s", repoPath, strings.TrimPrefix(path, "/"))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("not found")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var meta ContentMetadataResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, MaxGitHubBody)).Decode(&meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

// PutBlob uploads or replaces a file at v/{locator}.bin using write token.
// Handles BOTH 409 and 422 conflict status codes for stale sha.
func (c *Client) PutBlob(ctx context.Context, repo, writeToken, path string, data []byte, existingSHA string) error {
	repoPath := NormalizeRepo(repo)
	url := fmt.Sprintf("https://api.github.com/repos/%s/contents/%s", repoPath, strings.TrimPrefix(path, "/"))

	payload := PutContentRequest{
		Message: CommitMessage,
		Content: base64.StdEncoding.EncodeToString(data),
		SHA:     existingSHA,
	}

	reqBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal PUT payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewBuffer(reqBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+writeToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("network error during PUT: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusConflict || resp.StatusCode == 422 {
		return fmt.Errorf("conflict (HTTP %d): stale SHA when writing blob %s", resp.StatusCode, path)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("GitHub API returned HTTP %d writing blob: %s", resp.StatusCode, string(body))
	}

	return nil
}

// CheckIsNewStore lists contents of v/ directory.
// Returns true if v/ is empty or returns 404 (needs decoy seeding).
func (c *Client) CheckIsNewStore(ctx context.Context, repo, token string) (bool, error) {
	repoPath := NormalizeRepo(repo)
	url := fmt.Sprintf("https://api.github.com/repos/%s/contents/v", repoPath)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return true, nil
	}
	if resp.StatusCode != http.StatusOK {
		return false, nil
	}

	var items []ContentMetadataResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, MaxGitHubBody)).Decode(&items); err != nil {
		return false, nil
	}

	return len(items) == 0, nil
}
