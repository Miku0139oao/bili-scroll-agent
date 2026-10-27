package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

// HTTPDoer is the minimal client surface the adapters need.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// HTTPResponse is the JSON-friendly result returned to JavaScript adapters.
type HTTPResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
	JSON    any               `json:"json"`
}

// ValidateBaseURL enforces the rules that keep an outbound provider request
// from becoming an SSRF primitive: http(s) only, no credentials, no query, no
// fragment, and plain HTTP only on loopback.
//
// A path is allowed because several providers (OpenRouter, self-hosted
// gateways) publish their API under a prefix such as /api/v1.
func ValidateBaseURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" {
		return nil, errors.New("invalid provider base URL")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return nil, errors.New("invalid provider base URL")
	}
	if parsed.User != nil {
		return nil, errors.New("provider base URL must not include credentials")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("provider base URL must not include a query or fragment")
	}
	if parsed.Scheme == "http" && !isLoopbackHost(parsed.Hostname()) {
		return nil, errors.New("non-HTTPS provider base URL is only allowed on loopback")
	}
	return parsed, nil
}

func isLoopbackHost(host string) bool {
	switch strings.ToLower(host) {
	case "127.0.0.1", "localhost", "::1":
		return true
	}
	return false
}

// guard allows only the hosts a provider is configured to talk to.
type guard struct {
	allowed map[string]struct{}
	// contacted records hosts that passed the guard, for audit output.
	contacted atomic.Value // []string
}

func newGuard(allowHosts []string, baseURL string) *guard {
	allowed := make(map[string]struct{}, len(allowHosts)+1)
	for _, host := range allowHosts {
		host = strings.ToLower(strings.TrimSpace(host))
		if host != "" {
			allowed[host] = struct{}{}
		}
	}
	if parsed, err := url.Parse(baseURL); err == nil && parsed.Hostname() != "" {
		allowed[strings.ToLower(parsed.Hostname())] = struct{}{}
	}
	guard := &guard{allowed: allowed}
	guard.contacted.Store([]string{})
	return guard
}

func (g *guard) permits(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" {
		return errors.New("invalid request URL")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "https" && !(scheme == "http" && isLoopbackHost(parsed.Hostname())) {
		return fmt.Errorf("adapter may only use https (or loopback http), got %q", scheme)
	}
	host := strings.ToLower(parsed.Hostname())
	if _, ok := g.allowed[host]; !ok {
		return fmt.Errorf("host %q is not allowed for this provider", host)
	}
	g.record(host)
	return nil
}

func (g *guard) record(host string) {
	current, _ := g.contacted.Load().([]string)
	for _, item := range current {
		if item == host {
			return
		}
	}
	g.contacted.Store(append(append([]string{}, current...), host))
}

// Hosts returns the hosts contacted so far, sorted by first contact.
func (g *guard) Hosts() []string {
	current, _ := g.contacted.Load().([]string)
	return append([]string{}, current...)
}

// fetchOptions controls a single guarded request.
type fetchOptions struct {
	Method  string
	URL     string
	Headers map[string]string
	Body    []byte
	// MaxBytes caps this response; 0 uses the registry default.
	MaxBytes int64
	// Throw converts a >=400 status into an error.
	Throw bool
}

// guardedClient performs allowlisted requests with body and size limits.
type guardedClient struct {
	guard      *guard
	client     HTTPDoer
	maxBytes   int64
	totalLimit int64
	total      *atomic.Int64
	userAgent  string
}

func (c *guardedClient) do(ctx context.Context, opts fetchOptions) (HTTPResponse, error) {
	if err := c.guard.permits(opts.URL); err != nil {
		return HTTPResponse{}, err
	}
	method := strings.ToUpper(strings.TrimSpace(opts.Method))
	if method == "" {
		method = http.MethodGet
	}
	var body io.Reader
	if len(opts.Body) > 0 {
		body = bytes.NewReader(opts.Body)
	}
	request, err := http.NewRequestWithContext(ctx, method, opts.URL, body)
	if err != nil {
		return HTTPResponse{}, fmt.Errorf("invalid request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	if c.userAgent != "" {
		request.Header.Set("User-Agent", c.userAgent)
	}
	for key, value := range opts.Headers {
		request.Header.Set(key, value)
	}
	client := c.client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return HTTPResponse{}, fmt.Errorf("network error: %w", err)
	}
	defer response.Body.Close()

	limit := opts.MaxBytes
	if limit <= 0 {
		limit = c.maxBytes
	}
	remaining := c.totalLimit
	if remaining <= 0 {
		remaining = 1 << 62
	}
	if c.total != nil && c.totalLimit > 0 {
		remaining = c.totalLimit - c.total.Load()
		if remaining <= 0 {
			return HTTPResponse{}, errors.New("response budget exhausted")
		}
	}
	if limit > remaining {
		limit = remaining
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, limit))
	if err != nil {
		return HTTPResponse{}, fmt.Errorf("read response: %w", err)
	}
	if c.total != nil {
		c.total.Add(int64(len(raw)))
	}
	result := HTTPResponse{
		Status:  response.StatusCode,
		Headers: map[string]string{},
		Body:    string(raw),
	}
	for key := range response.Header {
		result.Headers[strings.ToLower(key)] = response.Header.Get(key)
	}
	var decoded any
	if json.Unmarshal(raw, &decoded) == nil {
		result.JSON = decoded
	}
	if opts.Throw && response.StatusCode >= 400 {
		return result, fmt.Errorf("request failed (HTTP %d)", response.StatusCode)
	}
	return result, nil
}
