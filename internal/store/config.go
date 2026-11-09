package store

import (
	"context"
	"errors"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// ContractSource is one OpenAPI document configured from the UI. It is loaded at server start and
// applied to the running registry on save, so changing it does not need a restart.
type ContractSource struct {
	Project       string     `json:"project"`
	URL           string     `json:"url"`
	Enabled       bool       `json:"enabled"`
	By            string     `json:"by,omitempty"`
	UpdatedAt     time.Time  `json:"updated_at"`
	LastCheckedAt *time.Time `json:"last_checked_at,omitempty"`
	LastStatus    string     `json:"last_status,omitempty"` // valid, invalid, unknown
	LastError     string     `json:"last_error,omitempty"`
	LastVersion   string     `json:"last_version,omitempty"`
	LastEndpoints int        `json:"last_endpoints,omitempty"`
	LastDuration  int64      `json:"last_duration_ms,omitempty"`
}

// SourceCheckRecord is the persisted result of a check, so the UI can show the last known state
// without fetching the spec on every page load.
type SourceCheckRecord struct {
	Status     string
	Error      string
	Version    string
	Endpoints  int
	DurationMS int64
}

// NotificationSettings is the default notification channel, used by projects without an override.
type NotificationSettings struct {
	Enabled    bool      `json:"enabled"`
	WebhookURL string    `json:"webhook_url"`
	Events     []string  `json:"events"`
	By         string    `json:"by,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// NotificationChannel overrides the default channel for one project. A disabled channel silences
// that project; a missing channel falls back to the default.
type NotificationChannel struct {
	Project    string    `json:"project"`
	Enabled    bool      `json:"enabled"`
	WebhookURL string    `json:"webhook_url"`
	Events     []string  `json:"events"`
	By         string    `json:"by,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// ContractSources returns every database-managed source, sorted by project.
func (s *Store) ContractSources(ctx context.Context) ([]ContractSource, error) {
	res, err := s.query(ctx, `MATCH (s:ContractSource) RETURN s ORDER BY s.project`, nil)
	if err != nil {
		return nil, err
	}
	out := make([]ContractSource, 0, len(res.Records))
	for _, record := range res.Records {
		node, _ := record.Get("s")
		out = append(out, toContractSource(node.(neo4j.Node).Props))
	}
	return out, nil
}

// ContractSource returns one source by project.
func (s *Store) ContractSource(ctx context.Context, project string) (ContractSource, error) {
	res, err := s.query(ctx, `MATCH (s:ContractSource {project: $project}) RETURN s`, map[string]any{"project": project})
	if err != nil {
		return ContractSource{}, err
	}
	if len(res.Records) == 0 {
		return ContractSource{}, ErrNotFound
	}
	node, _ := res.Records[0].Get("s")
	return toContractSource(node.(neo4j.Node).Props), nil
}

// SetContractSource creates or updates a source. The check state is reset when the URL changes,
// because an old result says nothing about the new document.
func (s *Store) SetContractSource(ctx context.Context, src ContractSource) (ContractSource, error) {
	res, err := s.query(ctx, `
		MERGE (s:ContractSource {project: $project})
		ON CREATE SET s.createdAt = datetime(), s.lastStatus = 'unknown'
		WITH s, s.url AS previous
		SET s.url = $url, s.enabled = $enabled, s.by = $by, s.updatedAt = datetime()
		FOREACH (_ IN CASE WHEN coalesce(previous, '') = $url THEN [] ELSE [1] END |
			SET s.lastCheckedAt = null, s.lastStatus = 'unknown', s.lastError = '',
			    s.lastVersion = '', s.lastEndpoints = 0, s.lastDurationMs = 0)
		RETURN s`,
		map[string]any{
			"project": src.Project, "url": src.URL, "enabled": src.Enabled, "by": src.By,
		})
	if err != nil {
		return ContractSource{}, err
	}
	node, _ := res.Records[0].Get("s")
	return toContractSource(node.(neo4j.Node).Props), nil
}

// DeleteContractSource removes a database source. Historical contract snapshots stay untouched.
func (s *Store) DeleteContractSource(ctx context.Context, project string) error {
	res, err := s.query(ctx, `
		MATCH (s:ContractSource {project: $project})
		WITH s, count(*) AS found
		DELETE s
		RETURN found`, map[string]any{"project": project})
	if err != nil {
		return err
	}
	if len(res.Records) == 0 || recInt64(res.Records[0], "found") == 0 {
		return ErrNotFound
	}
	return nil
}

// RecordSourceCheck stores the outcome of a validation. Environment-only sources have no node, so
// ErrNotFound lets the caller keep the check result without persisting it.
func (s *Store) RecordSourceCheck(ctx context.Context, project string, record SourceCheckRecord) error {
	res, err := s.query(ctx, `
		MATCH (s:ContractSource {project: $project})
		SET s.lastCheckedAt = datetime(), s.lastStatus = $status, s.lastError = $error,
		    s.lastVersion = $version, s.lastEndpoints = $endpoints, s.lastDurationMs = $duration
		RETURN count(s) AS found`,
		map[string]any{
			"project": project, "status": record.Status, "error": record.Error,
			"version": record.Version, "endpoints": record.Endpoints, "duration": record.DurationMS,
		})
	if err != nil {
		return err
	}
	if len(res.Records) == 0 || recInt64(res.Records[0], "found") == 0 {
		return ErrNotFound
	}
	return nil
}

// NotificationChannels returns every project override, sorted by project.
func (s *Store) NotificationChannels(ctx context.Context) ([]NotificationChannel, error) {
	res, err := s.query(ctx, `MATCH (n:NotificationChannel) RETURN n ORDER BY n.project`, nil)
	if err != nil {
		return nil, err
	}
	out := make([]NotificationChannel, 0, len(res.Records))
	for _, record := range res.Records {
		node, _ := record.Get("n")
		out = append(out, toNotificationChannel(node.(neo4j.Node).Props))
	}
	return out, nil
}

// NotificationChannel returns one project override, or ErrNotFound when the project inherits.
func (s *Store) NotificationChannel(ctx context.Context, project string) (NotificationChannel, error) {
	res, err := s.query(ctx, `MATCH (n:NotificationChannel {project: $project}) RETURN n`,
		map[string]any{"project": project})
	if err != nil {
		return NotificationChannel{}, err
	}
	if len(res.Records) == 0 {
		return NotificationChannel{}, ErrNotFound
	}
	node, _ := res.Records[0].Get("n")
	return toNotificationChannel(node.(neo4j.Node).Props), nil
}

// SetNotificationChannel creates or updates one project override.
func (s *Store) SetNotificationChannel(ctx context.Context, channel NotificationChannel) (NotificationChannel, error) {
	res, err := s.query(ctx, `
		MERGE (n:NotificationChannel {project: $project})
		ON CREATE SET n.createdAt = datetime()
		SET n.enabled = $enabled, n.webhookUrl = $webhook, n.events = $events,
		    n.by = $by, n.updatedAt = datetime()
		RETURN n`,
		map[string]any{
			"project": channel.Project, "enabled": channel.Enabled,
			"webhook": channel.WebhookURL, "events": channel.Events, "by": channel.By,
		})
	if err != nil {
		return NotificationChannel{}, err
	}
	node, _ := res.Records[0].Get("n")
	return toNotificationChannel(node.(neo4j.Node).Props), nil
}

// DeleteNotificationChannel removes an override so the project uses the default again.
func (s *Store) DeleteNotificationChannel(ctx context.Context, project string) error {
	res, err := s.query(ctx, `
		MATCH (n:NotificationChannel {project: $project})
		WITH n, count(*) AS found
		DELETE n
		RETURN found`, map[string]any{"project": project})
	if err != nil {
		return err
	}
	if len(res.Records) == 0 || recInt64(res.Records[0], "found") == 0 {
		return ErrNotFound
	}
	return nil
}

// NotificationSettingsFor resolves the project override, then falls back to the default channel.
func (s *Store) NotificationSettingsFor(ctx context.Context, project string) (NotificationSettings, error) {
	if project != "" {
		channel, err := s.NotificationChannel(ctx, project)
		if err == nil {
			return NotificationSettings{
				Enabled: channel.Enabled, WebhookURL: channel.WebhookURL, Events: channel.Events,
				By: channel.By, UpdatedAt: channel.UpdatedAt,
			}, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return NotificationSettings{}, err
		}
	}
	return s.NotificationSettings(ctx)
}

// NotificationSettings returns the saved default channel, or defaults when nothing is saved.
func (s *Store) NotificationSettings(ctx context.Context) (NotificationSettings, error) {
	res, err := s.query(ctx, `MATCH (n:NotificationConfig {id: 'default'}) RETURN n`, nil)
	if err != nil {
		return NotificationSettings{}, err
	}
	if len(res.Records) == 0 {
		return NotificationSettings{Events: DefaultNotificationEvents()}, nil
	}
	node, _ := res.Records[0].Get("n")
	return toNotificationSettings(node.(neo4j.Node).Props), nil
}

// SetNotificationSettings saves the config. Delivery is not implemented yet; the row exists so the
// contract and the UI are ready when it is.
func (s *Store) SetNotificationSettings(ctx context.Context, settings NotificationSettings) (NotificationSettings, error) {
	res, err := s.query(ctx, `
		MERGE (n:NotificationConfig {id: 'default'})
		ON CREATE SET n.createdAt = datetime()
		SET n.enabled = $enabled, n.webhookUrl = $webhook, n.events = $events,
		    n.by = $by, n.updatedAt = datetime()
		RETURN n`,
		map[string]any{
			"enabled": settings.Enabled, "webhook": settings.WebhookURL,
			"events": settings.Events, "by": settings.By,
		})
	if err != nil {
		return NotificationSettings{}, err
	}
	node, _ := res.Records[0].Get("n")
	return toNotificationSettings(node.(neo4j.Node).Props), nil
}

// DefaultNotificationEvents is the set selected when nothing is stored yet.
func DefaultNotificationEvents() []string {
	return []string{"new_failure", "regressed"}
}

// KnownNotificationEvents lists every event the Config UI may enable.
func KnownNotificationEvents() []string {
	return []string{"new_failure", "regressed", "quarantine_expiring"}
}

func toContractSource(p map[string]any) ContractSource {
	src := ContractSource{
		Project: str(p, "project"), URL: str(p, "url"), Enabled: boolValue(p["enabled"]),
		By: str(p, "by"), UpdatedAt: local(p["updatedAt"]),
		LastStatus: str(p, "lastStatus"), LastError: str(p, "lastError"),
		LastVersion: str(p, "lastVersion"), LastEndpoints: int(i64(p, "lastEndpoints")),
		LastDuration: i64(p, "lastDurationMs"),
	}
	if checked, ok := p["lastCheckedAt"]; ok {
		at := local(checked)
		src.LastCheckedAt = &at
	}
	return src
}

func toNotificationChannel(p map[string]any) NotificationChannel {
	return NotificationChannel{
		Project: str(p, "project"), Enabled: boolValue(p["enabled"]),
		WebhookURL: str(p, "webhookUrl"), Events: strs(p, "events"),
		By: str(p, "by"), UpdatedAt: local(p["updatedAt"]),
	}
}

func toNotificationSettings(p map[string]any) NotificationSettings {
	return NotificationSettings{
		Enabled: boolValue(p["enabled"]), WebhookURL: str(p, "webhookUrl"),
		Events: strs(p, "events"), By: str(p, "by"), UpdatedAt: local(p["updatedAt"]),
	}
}

func boolValue(v any) bool {
	value, _ := v.(bool)
	return value
}
