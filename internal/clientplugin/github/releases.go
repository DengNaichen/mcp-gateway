// Package github reads plugin release metadata and assets from GitHub.
package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	APIURL string
	Assets string
	HTTP   *http.Client
}

type Release struct {
	Tag        string                  `json:"tag_name"`
	Draft      bool                    `json:"draft"`
	Prerelease bool                    `json:"prerelease"`
	Assets     []struct{ Name string } `json:"assets"`
}

func (c Client) Releases(ctx context.Context) ([]Release, time.Time, error) {
	var releases []Release
	for page := 1; ; page++ {
		url := fmt.Sprintf("%s/releases?per_page=50&page=%d", c.APIURL, page)
		raw, headers, retryAt, err := c.get(ctx, url, 8<<20)
		if err != nil {
			return nil, retryAt, err
		}
		var batch []Release
		if err := json.Unmarshal(raw, &batch); err != nil {
			return nil, time.Time{}, fmt.Errorf("invalid GitHub release catalog: %w", err)
		}
		releases = append(releases, batch...)
		if !strings.Contains(headers.Get("Link"), `rel="next"`) {
			return releases, time.Time{}, nil
		}
	}
}

func (c Client) Download(ctx context.Context, tag, file string, limit int) ([]byte, time.Time, error) {
	raw, _, retryAt, err := c.get(ctx, c.Assets+tag+"/"+file, limit)
	return raw, retryAt, err
}

func releaseRetryAt(res *http.Response) time.Time {
	if res.StatusCode != http.StatusTooManyRequests && res.StatusCode != http.StatusForbidden && res.StatusCode != http.StatusServiceUnavailable {
		return time.Time{}
	}
	value := res.Header.Get("Retry-After")
	if seconds, err := strconv.ParseInt(value, 10, 32); err == nil && seconds >= 0 {
		return time.Now().Add(time.Duration(seconds) * time.Second)
	}
	if date, err := http.ParseTime(value); err == nil {
		return date
	}
	if res.Header.Get("X-RateLimit-Remaining") == "0" {
		if unix, err := strconv.ParseInt(res.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			return time.Unix(unix, 0)
		}
	}
	return time.Time{}
}

func (c Client) get(ctx context.Context, url string, limit int) ([]byte, http.Header, time.Time, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, time.Time{}, err
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, nil, time.Time{}, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, nil, releaseRetryAt(res), fmt.Errorf("GitHub returned HTTP %d for the plugin update", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, int64(limit)+1))
	if err != nil {
		return nil, nil, time.Time{}, err
	}
	if len(body) > limit {
		return nil, nil, time.Time{}, fmt.Errorf("plugin update response exceeds the size limit")
	}
	return body, res.Header, time.Time{}, nil
}
