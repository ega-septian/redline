package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"redline/internal/contract"
	"redline/internal/notify"
	"redline/internal/store"
)

var projectName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// decodeJSON limits request bodies so a config write cannot be used to exhaust memory.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	return json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(dst)
}

// contractSourceView is one row of the Config page.
type contractSourceView struct {
	Project        string     `json:"project"`
	URL            string     `json:"url"`
	Enabled        bool       `json:"enabled"`
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
	Enabled         bool                        `json:"enabled"`
	Provider        string                      `json:"provider"`
	WebhookURL      string                      `json:"webhook_url"`
	WebhookSet      bool                        `json:"webhook_set"`
	Events          []string                    `json:"events"`
	AvailableEvents []string                    `json:"available_events"`
	By              string                      `json:"by,omitempty"`
	UpdatedAt       *time.Time                  `json:"updated_at,omitempty"`
	Delivery        bool                        `json:"delivery"`
	LastDelivery    *store.NotificationDelivery `json:"last_delivery,omitempty"`
}

type notificationChannelView struct {
	Project         string     `json:"project"`
	Enabled         bool       `json:"enabled"`
	WebhookURL      string     `json:"webhook_url"`
	WebhookSet      bool       `json:"webhook_set"`
	Events          []string   `json:"events"`
	AvailableEvents []string   `json:"available_events"`
	By              string     `json:"by,omitempty"`
	UpdatedAt       *time.Time `json:"updated_at,omitempty"`
}

type configView struct {
	Contracts            []contractSourceView      `json:"contracts"`
	Notifications        notificationView          `json:"notifications"`
	NotificationChannels []notificationChannelView `json:"notification_channels"`
}

// handleGetConfig: GET /api/config — every stored contract source and the notification settings.
func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	sources, err := s.Store.ContractSources(r.Context())
	if err != nil {
		s.serverError(w, err)
		return
	}
	rows := make([]contractSourceView, 0, len(sources))
	for _, source := range sources {
		rows = append(rows, sourceView(source))
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Project < rows[j].Project })

	settings, err := s.Store.NotificationSettings(r.Context())
	if err != nil {
		s.serverError(w, err)
		return
	}
	lastDelivery, err := s.Store.LastNotificationDelivery(r.Context())
	if err != nil {
		s.serverError(w, err)
		return
	}
	channels, err := s.Store.NotificationChannels(r.Context())
	if err != nil {
		s.serverError(w, err)
		return
	}
	channelRows := make([]notificationChannelView, 0, len(channels))
	for _, channel := range channels {
		channelRows = append(channelRows, notificationChannelOut(channel))
	}
	writeJSON(w, http.StatusOK, configView{
		Contracts: rows, Notifications: notificationOut(settings, lastDelivery),
		NotificationChannels: channelRows,
	})
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
	writeJSON(w, http.StatusOK, sourceView(source))
}

// handleDeleteContract: DELETE /api/config/contracts?project=... — remove the source and stop using it.
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
	if s.Contracts != nil {
		s.Contracts.Delete(project)
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": project})
}

// handleSourceHistory: GET /api/config/contracts/history?project=...&limit=20
func (s *Server) handleSourceHistory(w http.ResponseWriter, r *http.Request) {
	project := strings.TrimSpace(r.URL.Query().Get("project"))
	if project == "" {
		writeError(w, http.StatusBadRequest, "project wajib diisi")
		return
	}
	if !projectName.MatchString(project) {
		writeError(w, http.StatusBadRequest, "project hanya boleh huruf, angka, titik, underscore atau strip (maks 64)")
		return
	}
	limit, err := queryInt(r, "limit", 20, 1, 100)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	checks, err := s.Store.SourceChecks(r.Context(), project, limit)
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, checks)
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

type notificationInput struct {
	Enabled    bool     `json:"enabled"`
	WebhookURL string   `json:"webhook_url"`
	Events     []string `json:"events"`
	By         string   `json:"by"`
}

// prepareNotification validates a channel body and preserves a masked webhook from the current one.
func (s *Server) prepareNotification(body notificationInput, currentWebhook string) (webhook string, events []string, err error) {
	webhook = strings.TrimSpace(body.WebhookURL)
	if strings.Contains(webhook, "••••") {
		webhook = currentWebhook
	}
	if len(webhook) > 2048 {
		return "", nil, errors.New("webhook_url terlalu panjang (maks 2048 karakter)")
	}
	if webhook != "" {
		parsed, err := url.Parse(webhook)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return "", nil, errors.New("webhook_url harus http(s)://host/path")
		}
		if blockedNotificationHost(parsed.Hostname()) {
			return "", nil, errors.New("host webhook ini tidak diizinkan (alamat metadata cloud)")
		}
	}
	events, err = normalizeEvents(body.Events)
	if err != nil {
		return "", nil, err
	}
	if body.Enabled && webhook == "" {
		return "", nil, errors.New("webhook_url wajib diisi kalau notifications diaktifkan")
	}
	if body.Enabled && len(events) == 0 {
		return "", nil, errors.New("pilih minimal satu event kalau notifications diaktifkan")
	}
	return webhook, events, nil
}

// handleSaveNotifications: PUT /api/config/notifications — the default channel.
func (s *Server) handleSaveNotifications(w http.ResponseWriter, r *http.Request) {
	var body notificationInput
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "body harus JSON: {\"enabled\": true, \"webhook_url\": \"https://...\", \"events\": [...]}")
		return
	}
	current, err := s.Store.NotificationSettings(r.Context())
	if err != nil {
		s.serverError(w, err)
		return
	}
	webhook, events, err := s.prepareNotification(body, current.WebhookURL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	saved, err := s.Store.SetNotificationSettings(r.Context(), store.NotificationSettings{
		Enabled: body.Enabled, WebhookURL: webhook, Events: events, By: s.actor(r, body.By),
	})
	if err != nil {
		s.serverError(w, err)
		return
	}
	lastDelivery, err := s.Store.LastNotificationDelivery(r.Context())
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, notificationOut(saved, lastDelivery))
}

// handleSaveNotificationChannel: PUT /api/config/notifications/projects/{project} — a project override.
func (s *Server) handleSaveNotificationChannel(w http.ResponseWriter, r *http.Request) {
	project := strings.TrimSpace(r.PathValue("project"))
	if !projectName.MatchString(project) {
		writeError(w, http.StatusBadRequest, "project hanya boleh huruf, angka, titik, underscore atau strip (maks 64)")
		return
	}
	var body notificationInput
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "body harus JSON: {\"enabled\": true, \"webhook_url\": \"https://...\", \"events\": [...]}")
		return
	}
	currentWebhook := ""
	if channel, err := s.Store.NotificationChannel(r.Context(), project); err == nil {
		currentWebhook = channel.WebhookURL
	} else if !errors.Is(err, store.ErrNotFound) {
		s.serverError(w, err)
		return
	} else {
		settings, err := s.Store.NotificationSettings(r.Context())
		if err != nil {
			s.serverError(w, err)
			return
		}
		currentWebhook = settings.WebhookURL
	}
	webhook, events, err := s.prepareNotification(body, currentWebhook)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	saved, err := s.Store.SetNotificationChannel(r.Context(), store.NotificationChannel{
		Project: project, Enabled: body.Enabled, WebhookURL: webhook, Events: events,
		By: s.actor(r, body.By),
	})
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, notificationChannelOut(saved))
}

// handleDeleteNotificationChannel: DELETE /api/config/notifications/projects/{project}
func (s *Server) handleDeleteNotificationChannel(w http.ResponseWriter, r *http.Request) {
	project := strings.TrimSpace(r.PathValue("project"))
	if err := s.Store.DeleteNotificationChannel(r.Context(), project); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "project channel tidak ditemukan")
			return
		}
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": project})
}

// handleNotificationDeliveries: GET /api/config/notifications/deliveries?limit=20
func (s *Server) handleNotificationDeliveries(w http.ResponseWriter, r *http.Request) {
	limit, err := queryInt(r, "limit", 20, 1, 100)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	deliveries, err := s.Store.NotificationDeliveries(r.Context(), limit)
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, deliveries)
}

// handleTestNotification: POST /api/config/notifications/test — sends a real Discord test message.
func (s *Server) handleTestNotification(w http.ResponseWriter, r *http.Request) {
	project := strings.TrimSpace(r.URL.Query().Get("project"))
	if project != "" && !projectName.MatchString(project) {
		writeError(w, http.StatusBadRequest, "project hanya boleh huruf, angka, titik, underscore atau strip (maks 64)")
		return
	}
	settings, err := s.Store.NotificationSettingsFor(r.Context(), project)
	if err != nil {
		s.serverError(w, err)
		return
	}
	if settings.WebhookURL == "" {
		writeError(w, http.StatusBadRequest, "webhook_url belum diisi; simpan Discord webhook dulu")
		return
	}
	if s.Notifier == nil {
		writeError(w, http.StatusServiceUnavailable, "notifier belum dikonfigurasi di server")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	message := notify.Message{
		Title: "Redline test notification",
		Description: "If you can read this, the Discord webhook is connected. " +
			"New failures and regressions will be posted here.",
		Color:  0x58A6FF,
		Fields: []notify.Field{{Name: "Status", Value: "test", Inline: true}},
		Footer: "Redline",
	}
	attempts, status, sendErr := s.sendNotification(ctx, settings.WebhookURL, message)
	delivery := store.NotificationDelivery{
		At: time.Now(), Event: "test", Project: project, OK: sendErr == nil,
		StatusCode: status, Attempts: attempts,
	}
	if sendErr != nil {
		delivery.Error = sendErr.Error()
	}
	if err := s.Store.RecordNotificationDelivery(r.Context(), delivery); err != nil {
		s.logWarn("delivery log gagal disimpan", "err", err)
	}
	if sendErr != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"sent": false, "mock": false, "error": "Discord menolak webhook: " + sendErr.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sent": true, "mock": false, "message": "Test notification sent to Discord.",
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

func sourceView(src store.ContractSource) contractSourceView {
	view := contractSourceView{
		Project: src.Project, URL: src.URL, Enabled: src.Enabled,
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

func notificationChannelOut(channel store.NotificationChannel) notificationChannelView {
	view := notificationChannelView{
		Project: channel.Project, Enabled: channel.Enabled,
		WebhookURL: maskWebhook(channel.WebhookURL), WebhookSet: channel.WebhookURL != "",
		Events: channel.Events, AvailableEvents: store.KnownNotificationEvents(), By: channel.By,
	}
	if view.Events == nil {
		view.Events = []string{}
	}
	if !channel.UpdatedAt.IsZero() {
		at := channel.UpdatedAt
		view.UpdatedAt = &at
	}
	return view
}

func notificationOut(settings store.NotificationSettings, last *store.NotificationDelivery) notificationView {
	view := notificationView{
		Enabled: settings.Enabled, Provider: "discord", WebhookSet: settings.WebhookURL != "",
		WebhookURL: maskWebhook(settings.WebhookURL), Events: settings.Events,
		AvailableEvents: store.KnownNotificationEvents(), By: settings.By, Delivery: true,
		LastDelivery: last,
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

func blockedNotificationHost(host string) bool {
	switch strings.ToLower(host) {
	case "169.254.169.254", "metadata.google.internal", "metadata", "fd00:ec2::254":
		return true
	default:
		return false
	}
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
