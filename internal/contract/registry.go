package contract

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Registry maps Playwright projects to the URL of their OpenAPI document.
// It is safe for concurrent use: the Config page can change sources while runs are being ingested.
type Registry struct {
	mu    sync.RWMutex
	specs map[string]string // project -> OpenAPI URL (JSON)
	// HTTP is used for every fetch. A custom client is handy in tests.
	HTTP *http.Client
}

func NewRegistry(specs map[string]string) *Registry {
	r := &Registry{specs: map[string]string{}, HTTP: &http.Client{Timeout: 20 * time.Second}}
	r.Replace(specs)
	return r
}

// Specs returns a copy of the configured sources, so callers cannot mutate the registry by accident.
func (r *Registry) Specs() map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]string, len(r.specs))
	for project, url := range r.specs {
		out[project] = url
	}
	return out
}

// URL returns the spec URL for one project.
func (r *Registry) URL(project string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	url, ok := r.specs[project]
	return url, ok
}

// Replace swaps every source at once. Used at startup after the database config is loaded.
func (r *Registry) Replace(specs map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.specs = make(map[string]string, len(specs))
	for project, url := range specs {
		r.specs[project] = url
	}
}

// Set adds or replaces one project source at runtime.
func (r *Registry) Set(project, url string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.specs[project] = url
}

// Delete removes one project source at runtime.
func (r *Registry) Delete(project string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.specs, project)
}

// ParseSpecs reads "toolshop=http://localhost:8091/docs,gorest=https://..." (the OPENAPI_SPECS format).
func ParseSpecs(s string) (map[string]string, error) {
	out := map[string]string{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		project, url, ok := strings.Cut(part, "=")
		if !ok || strings.TrimSpace(project) == "" || !strings.HasPrefix(strings.TrimSpace(url), "http") {
			return nil, fmt.Errorf("OPENAPI_SPECS: %q harus berbentuk project=http(s)://url", part)
		}
		out[strings.TrimSpace(project)] = strings.TrimSpace(url)
	}
	return out, nil
}

// Fetch mengambil dokumen kontrak project. url kosong (tanpa error) kalau project tidak punya kontrak.
func (r *Registry) Fetch(ctx context.Context, project string) (url string, document []byte, err error) {
	url, ok := r.URL(project)
	if !ok {
		return "", nil, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return url, nil, err
	}
	resp, err := r.HTTP.Do(req)
	if err != nil {
		return url, nil, fmt.Errorf("ambil kontrak %s: %w", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxSpecBytes+1))
	if err != nil {
		return url, nil, err
	}
	if len(body) > MaxSpecBytes {
		return url, nil, fmt.Errorf("kontrak %s: dokumen lebih dari %d MB", url, MaxSpecBytes>>20)
	}
	if resp.StatusCode != http.StatusOK {
		return url, nil, fmt.Errorf("ambil kontrak %s: HTTP %d", url, resp.StatusCode)
	}
	if _, err := Parse(body); err != nil {
		return url, nil, fmt.Errorf("kontrak %s: %w", url, err)
	}
	return url, body, nil
}
