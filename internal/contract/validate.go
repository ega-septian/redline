package contract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// MaxSpecBytes is the largest OpenAPI document Redline will read (10 MB).
const MaxSpecBytes = 10 << 20

// SourceCheck is the result of fetching and parsing one spec URL. The Config UI shows it before a
// source is saved, so an operator learns immediately whether a URL is an OpenAPI document.
type SourceCheck struct {
	OK          bool      `json:"ok"`
	URL         string    `json:"url"`
	Kind        string    `json:"kind,omitempty"` // invalid_url, blocked, status, timeout, network, html, too_large, json, not_openapi
	StatusCode  int       `json:"status_code,omitempty"`
	ContentType string    `json:"content_type,omitempty"`
	Title       string    `json:"title,omitempty"`
	Version     string    `json:"openapi,omitempty"`
	Endpoints   int       `json:"endpoints"`
	Error       string    `json:"error,omitempty"`
	Hint        string    `json:"hint,omitempty"`
	Candidates  []string  `json:"candidates,omitempty"`
	CheckedAt   time.Time `json:"checked_at"`
	DurationMS  int64     `json:"duration_ms"`
}

// ValidateSource fetches a URL and reports whether it is a usable OpenAPI 3 JSON document.
// It never returns an error: every failure is part of the check so the UI can explain it.
func ValidateSource(ctx context.Context, raw string) (check SourceCheck) {
	started := time.Now()
	check = SourceCheck{URL: strings.TrimSpace(raw), CheckedAt: started}
	defer func() { check.DurationMS = time.Since(started).Milliseconds() }()

	parsed, err := url.Parse(check.URL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return check.failed("invalid_url", "URL must be http(s)://host/path", "Contoh: https://api.example.com/docs")
	}
	if blockedSpecHost(parsed.Hostname()) {
		return check.failed("blocked", "this host is not allowed (cloud metadata address)",
			"Use the application documentation URL, not a cloud provider metadata endpoint.")
	}

	client := &http.Client{
		Timeout: 20 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("redirect to scheme %q is not allowed", req.URL.Scheme)
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, check.URL, nil)
	if err != nil {
		return check.failed("invalid_url", "URL cannot be used: "+err.Error(), "")
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		var netErr net.Error
		if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
			return check.failed("timeout", "server did not respond within 20 seconds",
				"Make sure the URL is reachable from the Redline server, not only from your browser.")
		}
		return check.failed("network", "could not fetch URL: "+err.Error(),
			"For Docker, use host.docker.internal for a service running on the host.")
	}
	defer resp.Body.Close()
	check.StatusCode = resp.StatusCode
	check.ContentType = resp.Header.Get("Content-Type")

	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxSpecBytes+1))
	if err != nil {
		return check.failed("network", "failed to read response: "+err.Error(), "")
	}
	if len(body) > MaxSpecBytes {
		return check.failed("too_large", "document is larger than 10 MB", "Check that the URL points to an OpenAPI file, not an archive.")
	}
	if resp.StatusCode != http.StatusOK {
		hint := "Check that the URL and path are correct."
		switch resp.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			hint = "This document requires authentication. Use a public URL or add a token on the server side."
		case http.StatusNotFound:
			hint = "Swagger UI is usually served from a different path than its JSON file. Find the URL the page loads."
		}
		return check.failed("status", fmt.Sprintf("URL membalas HTTP %d", resp.StatusCode), hint)
	}
	if isHTML(check.ContentType, body) {
		failed := check.failed("html", "URL returned an HTML page (Swagger UI), not a JSON document",
			"Open the Swagger UI page and find the JSON file it loads (often /docs, /openapi.json or /swagger.json).")
		failed.Candidates = specCandidates(check.URL, body)
		return failed
	}

	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return check.failed("json", "response is not valid JSON: "+err.Error(),
			"Redline needs an OpenAPI document in JSON format.")
	}
	if _, ok := root["swagger"]; ok {
		return check.failed("not_openapi", "dokumen ini Swagger 2.0; Redline membutuhkan OpenAPI 3",
			"If the application only has Swagger 2.0, convert it first or use an OpenAPI 3 contract.")
	}
	doc, err := Parse(body)
	if err != nil {
		return check.failed("not_openapi", err.Error(),
			"Make sure the \"openapi\" field is version 3.x and the document is JSON, not YAML.")
	}
	check.OK = true
	check.Version = "OpenAPI " + stringValue(root["openapi"])
	check.Title = documentTitle(root)
	check.Endpoints = len(doc.Endpoints())
	return check
}

func (c SourceCheck) failed(kind, message, hint string) SourceCheck {
	c.OK = false
	c.Kind = kind
	c.Error = message
	c.Hint = hint
	return c
}

// blockedSpecHost stops the server from being pointed at cloud metadata endpoints. Private hosts stay
// allowed because self-hosted Toolshop and other local services are legitimate spec sources.
func blockedSpecHost(host string) bool {
	switch strings.ToLower(host) {
	case "169.254.169.254", "metadata.google.internal", "metadata", "fd00:ec2::254":
		return true
	default:
		return false
	}
}

func isHTML(contentType string, body []byte) bool {
	if strings.Contains(strings.ToLower(contentType), "text/html") {
		return true
	}
	head := strings.ToLower(strings.TrimSpace(string(body[:min(len(body), 256)])))
	return strings.HasPrefix(head, "<!doctype html") || strings.HasPrefix(head, "<html")
}

// swaggerURL finds the JSON URL configured inside a Swagger UI page (`url: "..."`), so the Config
// form can offer the real spec instead of making the operator search the page source.
var swaggerURL = regexp.MustCompile(`(?i)url\s*:\s*["']([^"']{1,1000})["']`)

func specCandidates(base string, body []byte) []string {
	baseURL, err := url.Parse(base)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, 3)
	for _, match := range swaggerURL.FindAllSubmatch(body, 3) {
		ref, err := url.Parse(strings.TrimSpace(string(match[1])))
		if err != nil {
			continue
		}
		resolved := baseURL.ResolveReference(ref)
		if (resolved.Scheme != "http" && resolved.Scheme != "https") || resolved.Host == "" {
			continue
		}
		lower := strings.ToLower(resolved.Path + "?" + resolved.RawQuery)
		if !strings.Contains(lower, "json") && !strings.Contains(lower, "openapi") &&
			!strings.Contains(lower, "swagger") && !strings.Contains(lower, "docs") &&
			!strings.Contains(lower, "spec") {
			continue
		}
		candidate := resolved.String()
		if seen[candidate] {
			continue
		}
		seen[candidate] = true
		out = append(out, candidate)
		if len(out) == 3 {
			break
		}
	}
	return out
}

func documentTitle(root map[string]any) string {
	info, _ := root["info"].(map[string]any)
	title, _ := info["title"].(string)
	return title
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}
