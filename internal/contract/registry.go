package contract

import (
	"context"
	"fmt"
	"io"
	"net/http"
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
	for project, url := range specs {
		r.specs[project] = url
	}
	return r
}

// URL returns the spec URL for one project.
func (r *Registry) URL(project string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	url, ok := r.specs[project]
	return url, ok
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
		return url, nil, fmt.Errorf("fetch contract %s: %w", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxSpecBytes+1))
	if err != nil {
		return url, nil, err
	}
	if len(body) > MaxSpecBytes {
		return url, nil, fmt.Errorf("contract %s: document is larger than %d MB", url, MaxSpecBytes>>20)
	}
	if resp.StatusCode != http.StatusOK {
		return url, nil, fmt.Errorf("fetch contract %s: HTTP %d", url, resp.StatusCode)
	}
	if _, err := Parse(body); err != nil {
		return url, nil, fmt.Errorf("contract %s: %w", url, err)
	}
	return url, body, nil
}
