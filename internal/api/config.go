package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"redline/internal/contract"
	"redline/internal/store"
)

var projectName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// decodeJSON limits request bodies so a config write cannot be used to exhaust memory.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	return json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(dst)
}

// contractSourceView is one row of the Config page. Origin tells whether the source comes from the
// database (editable) or from OPENAPI_SPECS (fallback, read-only).
type contractSourceView struct {
	Project        string     `json:"project"`
	URL            string     `json:"url"`
	Enabled        bool       `json:"enabled"`
	Origin         string     `json:"origin"`
	By             string     `json:"by,omitempty"`
	UpdatedAt      *time.Time `json:"updated_at,omitempty"`
	LastCheckedAt  *time.Time `json:"last_checked_at,omitempty"`
	LastStatus     string     `json:"last_status,omitempty"`
	LastError      string     `json:"last_error,omitempty"`
	LastVersion    string     `json:"last_version,omitempty"`
	LastEndpoints  int        `json:"last_endpoints,omitempty"`
	LastDurationMS int64      `json:"last_duration_ms,omitempty"`
}

// notificationView masks the webhook URL: the secret token must not travel to the browser again.
type notificationView struct {
	Enabled         bool       `json:"enabled"`
	WebhookURL      string     `json:"webhook_url"`
	WebhookSet      bool       `json:"webhook_set"`
	Events          []string   `json:"events"`
	AvailableEvents []string   `json:"available_events"`
	By              string     `json:"by,omitempty"`
	UpdatedAt       *time.Time `json:"updated_at,omitempty"`
	Delivery        bool       `json:"delivery"`
}

type configView struct {
	Contracts     []contractSourceView `json:"contracts"`
	Notifications notificationView     `json:"notifications"`
}

// handleGetConfig: GET /api/config — database sources plus the environment fallback, and the saved
// notification settings. The database value wins for the same project.
func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	sources, err := s.Store.ContractSources(r.Context())
	if err != nil {
		s.serverError(w, err)
		return
	}
	env := map[string]string{}
	if s.EnvSpecs != nil {
		env = s.EnvSpecs
	}
	rows := make([]contractSourceView, 0, len(sources)+len(env))
	seen := map[string]bool{}
	for _, source := range sources {
		seen[source.Project] = true
		rows = append(rows, sourceView(source, "database"))
	}
	for project, url := range env {
		if seen[project] {
			continue
		}
		rows = append(rows, contractSourceView{
			Project: project, URL: url, Enabled: true, Origin: "environment", LastStatus: "unknown",
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Project < rows[j].Project })

	settings, err := s.Store.NotificationSettings(r.Context())
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, configView{Contracts: rows, Notifications: notificationOut(settings)})
}

// handleValidateSource: POST /api/config/contracts/validate — check a URL without saving it.
func (s *Server) handleValidateSource(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL string `json:"url"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "body harus JSON: {\"url\": \"https://...\"}")
		return
	}
	writeJSON(w, http.StatusOK, s.checkSource(r, strings.TrimSpace(body.URL)))
}

// handleSaveContract: PUT /api/config/contracts — validate, save and apply the source at runtime.
func (s *Server) handleSaveContract(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Project string `json:"project"`
		URL     string `json:"url"`
		By      string `json:"by"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "body harus JSON: {\"project\": \"toolshop\", \"url\": \"https://...\"}")
		return
	}
	project := strings.TrimSpace(body.Project)
	specURL := strings.TrimSpace(body.URL)
	if project == "" || specURL == "" {
		writeError(w, http.StatusBadRequest, "project dan url wajib diisi")
		return
	}
	if !projectName.MatchString(project) {
		writeError(w, http.StatusBadRequest, "project hanya boleh huruf, angka, titik, underscore atau strip (maks 64)")
		return
	}
	check := s.checkSource(r, specURL)
	if !check.OK {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": check.Error, "check": check})
		return
	}
	source, err := s.Store.SetContractSource(r.Context(), store.ContractSource{
		Project: project, URL: specURL, Enabled: true, By: s.actor(r, body.By),
	})
	if err != nil {
		s.serverError(w, err)
		return
	}
	if err := s.Store.RecordSourceCheck(r.Context(), project, store.SourceCheckRecord{
		Status: "valid", Version: check.Version, Endpoints: check.Endpoints, DurationMS: check.DurationMS,
	}); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.serverError(w, err)
		return
	}
	if saved, err := s.Store.ContractSource(r.Context(), project); err == nil {
		source = saved
	}
	s.applyContractSource(project, specURL)
	writeJSON(w, http.StatusOK, sourceView(source, "database"))
}

// handleDeleteContract: DELETE /api/config/contracts?project=... — remove the override and fall back
// to OPENAPI_SPECS when the project also exists there.
func (s *Server) handleDeleteContract(w http.ResponseWriter, r *http.Request) {
	project := strings.TrimSpace(r.URL.Query().Get("project"))
	if project == "" {
		writeError(w, http.StatusBadRequest, "project wajib diisi")
		return
	}
	if err := s.Store.DeleteContractSource(r.Context(), project); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "contract source tidak ditemukan")
			return
		}
		s.serverError(w, err)
		return
	}
	if url, ok := s.EnvSpecs[project]; ok {
		s.applyContractSource(project, url)
	} else if s.Contracts != nil {
		s.Contracts.Delete(project)
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": project})
}

// handleCheckContract: POST /api/config/contracts/check — re-check a source that is already saved
// (or the environment fallback) and persist the result when a database row exists.
func (s *Server) handleCheckContract(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Project string `json:"project"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "body harus JSON: {\"project\": \"toolshop\"}")
		return
	}
	project := strings.TrimSpace(body.Project)
	if project == "" {
		writeError(w, http.StatusBadRequest, "project wajib diisi")
		return
	}
	specURL := ""
	if s.Contracts != nil {
		specURL, _ = s.Contracts.URL(project)
	}
	if specURL == "" {
		writeError(w, http.StatusNotFound, "project ini tidak punya contract source")
		return
	}
	check := s.checkSource(r, specURL)
	status := "invalid"
	if check.OK {
		status = "valid"
	}
	err := s.Store.RecordSourceCheck(r.Context(), project, store.SourceCheckRecord{
		Status: status, Error: check.Error, Version: check.Version,
		Endpoints: check.Endpoints, DurationMS: check.DurationMS,
	})
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, check)
}

// handleSaveNotifications: PUT /api/config/notifications — saved config only; delivery is not wired.
func (s *Server) handleSaveNotifications(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled    bool     `json:"enabled"`
		WebhookURL string   `json:"webhook_url"`
		Events     []string `json:"events"`
		By         string   `json:"by"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "body harus JSON: {\"enabled\": true, \"webhook_url\": \"https://...\", \"events\": [...]}")
		return
	}
	current, err := s.Store.NotificationSettings(r.Context())
	if err != nil {
		s.serverError(w, err)
		return
	}
	webhook := strings.TrimSpace(body.WebhookURL)
	if strings.Contains(webhook, "••••") {
		webhook = current.WebhookURL
	}
	if len(webhook) > 2048 {
		writeError(w, http.StatusBadRequest, "webhook_url terlalu panjang (maks 2048 karakter)")
		return
	}
	if webhook != "" {
		parsed, err := url.Parse(webhook)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			writeError(w, http.StatusBadRequest, "webhook_url harus http(s)://host/path")
			return
		}
	}
	events, err := normalizeEvents(body.Events)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.Enabled && webhook == "" {
		writeError(w, http.StatusBadRequest, "webhook_url wajib diisi kalau notifications diaktifkan")
		return
	}
	if body.Enabled && len(events) == 0 {
		writeError(w, http.StatusBadRequest, "pilih minimal satu event kalau notifications diaktifkan")
		return
	}
	saved, err := s.Store.SetNotificationSettings(r.Context(), store.NotificationSettings{
		Enabled: body.Enabled, WebhookURL: webhook, Events: events, By: s.actor(r, body.By),
	})
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, notificationOut(saved))
}

// handleTestNotification: POST /api/config/notifications/test — explicit mock, nothing leaves the server.
func (s *Server) handleTestNotification(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"sent": false, "mock": true,
		"message": "Notifications are mock-only in this version; nothing was sent.",
	})
}

func (s *Server) checkSource(r *http.Request, specURL string) contract.SourceCheck {
	if s.SourceCheck != nil {
		return s.SourceCheck(r.Context(), specURL)
	}
	return contract.ValidateSource(r.Context(), specURL)
}

func (s *Server) applyContractSource(project, specURL string) {
	if s.Contracts != nil {
		s.Contracts.Set(project, specURL)
	}
}

func (s *Server) actor(r *http.Request, body string) string {
	by := strings.TrimSpace(body)
	if s.User != nil {
		if user := s.User(r); user != "" {
			by = user
		}
	}
	if by == "" {
		by = "api"
	}
	return by
}

func sourceView(src store.ContractSource, origin string) contractSourceView {
	view := contractSourceView{
		Project: src.Project, URL: src.URL, Enabled: src.Enabled, Origin: origin,
		By: src.By, LastCheckedAt: src.LastCheckedAt, LastStatus: src.LastStatus,
		LastError: src.LastError, LastVersion: src.LastVersion, LastEndpoints: src.LastEndpoints,
		LastDurationMS: src.LastDuration,
	}
	if !src.UpdatedAt.IsZero() {
		at := src.UpdatedAt
		view.UpdatedAt = &at
	}
	if view.LastStatus == "" {
		view.LastStatus = "unknown"
	}
	return view
}

func notificationOut(settings store.NotificationSettings) notificationView {
	view := notificationView{
		Enabled: settings.Enabled, WebhookSet: settings.WebhookURL != "",
		WebhookURL: maskWebhook(settings.WebhookURL), Events: settings.Events,
		AvailableEvents: store.KnownNotificationEvents(), By: settings.By, Delivery: false,
	}
	if view.Events == nil {
		view.Events = []string{}
	}
	if !settings.UpdatedAt.IsZero() {
		at := settings.UpdatedAt
		view.UpdatedAt = &at
	}
	return view
}

// maskWebhook keeps the scheme, host and first path segment so an operator can recognise the target
// while the secret part stays out of the response.
func maskWebhook(raw string) string {
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "••••"
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(segments) == 1 && segments[0] != "" {
		segments[0] = "••••"
	} else {
		for i := 1; i < len(segments); i++ {
			segments[i] = "••••"
		}
	}
	path := strings.Join(segments, "/")
	masked := parsed.Scheme + "://" + parsed.Host
	if path != "" {
		masked += "/" + path
	}
	// Query strings often carry the real token (for example ?token=...); keep only the fact that one exists.
	if parsed.RawQuery != "" {
		masked += "?••••"
	}
	return masked
}

func normalizeEvents(events []string) ([]string, error) {
	known := map[string]bool{}
	for _, event := range store.KnownNotificationEvents() {
		known[event] = true
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(events))
	for _, event := range events {
		event = strings.TrimSpace(event)
		if event == "" || seen[event] {
			continue
		}
		if !known[event] {
			return nil, errors.New("event tidak dikenal: " + event)
		}
		seen[event] = true
		out = append(out, event)
	}
	return out, nil
}
