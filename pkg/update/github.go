package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Release represents a GitHub Release with its downloadable assets.
type Release struct {
	Version string  // tag name, e.g. "v0.2.0"
	URL     string  // HTML URL of the release page
	Assets  []Asset // downloadable assets
}

// Asset is a single downloadable file attached to a release.
type Asset struct {
	Name        string // e.g. "evva-darwin-arm64.tar.gz"
	DownloadURL string // browser_download_url
	Size        int64  // bytes
}

func (r *Release) assetByName(name string) *Asset {
	for i := range r.Assets {
		if r.Assets[i].Name == name {
			return &r.Assets[i]
		}
	}
	return nil
}

// ghRelease is the JSON shape returned by the GitHub Releases API.
type ghRelease struct {
	TagName string    `json:"tag_name"`
	HTMLURL string    `json:"html_url"`
	Assets  []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

func fetchLatestRelease(ctx context.Context, owner, repo string) (*Release, error) {
	endpoint := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", owner, repo)
	return fetchRelease(ctx, endpoint, fmt.Sprintf("no releases found for %s/%s — publish a release first", owner, repo))
}

// fetchReleaseByTag resolves a single release by its exact tag (e.g. "v1.4.3"
// or "v1.4.3-beta.1"). Unlike the latest endpoint, this resolves pre-release
// tags too, so it backs `evva update <version>` for pinning to a beta.
func fetchReleaseByTag(ctx context.Context, owner, repo, tag string) (*Release, error) {
	endpoint := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/tags/%s", owner, repo, url.PathEscape(tag))
	return fetchRelease(ctx, endpoint, fmt.Sprintf("release %s not found for %s/%s", tag, owner, repo))
}

// fetchRelease performs the GitHub Releases API GET shared by the latest and
// by-tag lookups. notFoundMsg is surfaced verbatim on a 404 so each caller can
// phrase the miss in its own terms.
func fetchRelease(ctx context.Context, endpoint, notFoundMsg string) (*Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "evva-updater")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GitHub API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, errors.New(notFoundMsg)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("GitHub API returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var gr ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&gr); err != nil {
		return nil, fmt.Errorf("parse GitHub response: %w", err)
	}

	release := &Release{
		Version: gr.TagName,
		URL:     gr.HTMLURL,
		Assets:  make([]Asset, len(gr.Assets)),
	}
	for i, a := range gr.Assets {
		release.Assets[i] = Asset{
			Name:        a.Name,
			DownloadURL: a.BrowserDownloadURL,
			Size:        a.Size,
		}
	}
	return release, nil
}

func downloadAsset(ctx context.Context, asset *Asset) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.DownloadURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "evva-updater")
	req.Header.Set("Accept", "application/octet-stream")

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("download returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read download body: %w", err)
	}
	return data, nil
}
