package scrape

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// Kind classifies a fetch failure so callers can decide whether it is worth retrying later.
type Kind string

const (
	KindNotFound    Kind = "not_found"
	KindRateLimited Kind = "rate_limited"
	KindHTTP        Kind = "http"
	KindNetwork     Kind = "network"
	KindParse       Kind = "parse"
)

type FetchError struct {
	Kind   Kind
	Status int
	Msg    string
}

func (e *FetchError) Error() string { return string(e.Kind) + ": " + e.Msg }

// IsNotFound reports whether err is an HTTP 404 (typically a dead board).
func IsNotFound(err error) bool {
	var fe *FetchError
	return errors.As(err, &fe) && fe.Kind == KindNotFound
}

const maxBody = 64 << 20

// Client is a polite HTTP client: bounded per-host concurrency, retries with backoff,
// Retry-After support, conditional requests, and a response size cap.
type Client struct {
	HTTP      *http.Client
	UserAgent string
	Retries   int
	Backoff   time.Duration // base delay, doubled per attempt
	PerHost   int

	mu   sync.Mutex
	sems map[string]chan struct{}
}

func NewClient(userAgent string, perHost int) *Client {
	if perHost <= 0 {
		perHost = 4
	}
	return &Client{
		HTTP:      &http.Client{Timeout: 45 * time.Second},
		UserAgent: userAgent,
		Retries:   3,
		Backoff:   time.Second,
		PerHost:   perHost,
		sems:      map[string]chan struct{}{},
	}
}

func (c *Client) sem(host string) chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.sems[host]
	if !ok {
		s = make(chan struct{}, c.PerHost)
		c.sems[host] = s
	}
	return s
}

// GetJSON fetches rawURL and decodes JSON into v. If prevETag is set a conditional request is
// sent; a 304 returns notModified=true. It returns the response ETag.
func (c *Client) GetJSON(ctx context.Context, rawURL, prevETag string, v any) (etag string, notModified bool, err error) {
	u, perr := url.Parse(rawURL)
	if perr != nil {
		return "", false, &FetchError{Kind: KindParse, Msg: perr.Error()}
	}
	sem := c.sem(u.Host)
	var last error
	for attempt := 0; attempt <= c.Retries; attempt++ {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			return "", false, ctx.Err()
		}
		etag, notModified, delay, err := c.once(ctx, rawURL, prevETag, v)
		<-sem
		if err == nil {
			return etag, notModified, nil
		}
		last = err
		var fe *FetchError
		if errors.As(err, &fe) && (fe.Kind == KindNotFound || fe.Kind == KindParse || (fe.Kind == KindHTTP && fe.Status < 500)) {
			return "", false, err
		}
		if ctx.Err() != nil {
			return "", false, ctx.Err()
		}
		if attempt < c.Retries {
			if delay == 0 {
				delay = c.Backoff << attempt
			}
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return "", false, ctx.Err()
			}
		}
	}
	return "", false, last
}

func (c *Client) once(ctx context.Context, rawURL, prevETag string, v any) (etag string, notModified bool, retryAfter time.Duration, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", false, 0, &FetchError{Kind: KindParse, Msg: err.Error()}
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/json")
	if prevETag != "" {
		req.Header.Set("If-None-Match", prevETag)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", false, 0, &FetchError{Kind: KindNetwork, Msg: err.Error()}
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotModified:
		return prevETag, true, 0, nil
	case resp.StatusCode == http.StatusNotFound:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return "", false, 0, &FetchError{Kind: KindNotFound, Status: 404, Msg: "HTTP 404 " + rawURL}
	case resp.StatusCode == http.StatusTooManyRequests:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		var d time.Duration
		if s, e := strconv.Atoi(resp.Header.Get("Retry-After")); e == nil {
			d = time.Duration(s) * time.Second
		}
		return "", false, d, &FetchError{Kind: KindRateLimited, Status: 429, Msg: "HTTP 429 " + rawURL}
	case resp.StatusCode >= 400:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return "", false, 0, &FetchError{Kind: KindHTTP, Status: resp.StatusCode, Msg: fmt.Sprintf("HTTP %d %s", resp.StatusCode, rawURL)}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return "", false, 0, &FetchError{Kind: KindNetwork, Msg: err.Error()}
	}
	if len(body) > maxBody {
		return "", false, 0, &FetchError{Kind: KindParse, Msg: "response too large"}
	}
	if err := json.Unmarshal(body, v); err != nil {
		return "", false, 0, &FetchError{Kind: KindParse, Msg: "invalid JSON: " + err.Error()}
	}
	return resp.Header.Get("ETag"), false, 0, nil
}
