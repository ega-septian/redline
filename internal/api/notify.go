package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"redline/internal/notify"
	"redline/internal/store"
)

const notificationColor = 0xE5484D

// notifyRun sends one summary per project, using that project's channel or the default. It runs in
// the background; a slow or broken webhook must not block the reporter. Only new and regressed
// failures are delivered.
func (s *Server) notifyRun(meta store.RunMeta, res *store.IngestResult) {
	newByProject := map[string][]store.GroupChange{}
	regressedByProject := map[string][]store.GroupChange{}
	for _, change := range res.New {
		newByProject[change.Project] = append(newByProject[change.Project], change)
	}
	for _, change := range res.Regressed {
		regressedByProject[change.Project] = append(regressedByProject[change.Project], change)
	}
	projects := make([]string, 0, len(newByProject)+len(regressedByProject))
	seen := map[string]bool{}
	for project := range newByProject {
		if !seen[project] {
			seen[project] = true
			projects = append(projects, project)
		}
	}
	for project := range regressedByProject {
		if !seen[project] {
			seen[project] = true
			projects = append(projects, project)
		}
	}
	sort.Strings(projects)

	for _, project := range projects {
		settings, err := s.Store.NotificationSettingsFor(context.Background(), project)
		if err != nil {
			s.logWarn("notifikasi tidak bisa membaca settings", "run", res.RunID, "project", project, "err", err)
			continue
		}
		if !settings.Enabled || settings.WebhookURL == "" {
			continue
		}
		events := map[string]bool{}
		for _, event := range settings.Events {
			events[event] = true
		}
		changes := make([]store.GroupChange, 0, len(newByProject[project])+len(regressedByProject[project]))
		parts := make([]string, 0, 2)
		selected := make([]string, 0, 2)
		if len(newByProject[project]) > 0 && events["new_failure"] {
			changes = append(changes, newByProject[project]...)
			parts = append(parts, fmt.Sprintf("%d new", len(newByProject[project])))
			selected = append(selected, "new_failure")
		}
		if len(regressedByProject[project]) > 0 && events["regressed"] {
			changes = append(changes, regressedByProject[project]...)
			parts = append(parts, fmt.Sprintf("%d regressed", len(regressedByProject[project])))
			selected = append(selected, "regressed")
		}
		if len(changes) == 0 {
			continue
		}

		failed, flaky := notificationCounts(res, project)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		attempts, status, sendErr := s.sendNotification(ctx, settings.WebhookURL,
			notificationMessage(meta, res, changes, parts, project, failed, flaky))
		cancel()
		delivery := store.NotificationDelivery{
			At: time.Now(), Event: strings.Join(selected, "+"), Project: project, RunID: res.RunID,
			OK: sendErr == nil, StatusCode: status, Attempts: attempts,
		}
		if sendErr != nil {
			delivery.Error = sendErr.Error()
			s.logWarn("notifikasi gagal", "run", res.RunID, "project", project, "err", sendErr)
		}
		recordCtx, recordCancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := s.Store.RecordNotificationDelivery(recordCtx, delivery); err != nil {
			s.logWarn("delivery log gagal disimpan", "run", res.RunID, "project", project, "err", err)
		}
		recordCancel()
	}
}

// notifyExpiring posts one summary per project for active quarantines that end within 24 hours and
// have not been announced for their current until value. It is called after every ingest, so it
// needs no scheduler.
func (s *Server) notifyExpiring(meta store.RunMeta, runID int64) {
	expiring, err := s.Store.ExpiringQuarantines(context.Background(), 24*time.Hour)
	if err != nil {
		s.logWarn("expiry notification gagal membaca quarantine", "run", runID, "err", err)
		return
	}
	if len(expiring) == 0 {
		return
	}
	byProject := map[string][]store.Quarantine{}
	for _, quarantine := range expiring {
		byProject[quarantine.Project] = append(byProject[quarantine.Project], quarantine)
	}
	projects := make([]string, 0, len(byProject))
	for project := range byProject {
		projects = append(projects, project)
	}
	sort.Strings(projects)

	for _, project := range projects {
		settings, err := s.Store.NotificationSettingsFor(context.Background(), project)
		if err != nil {
			s.logWarn("expiry notification tidak bisa membaca settings", "run", runID, "project", project, "err", err)
			continue
		}
		if !settings.Enabled || settings.WebhookURL == "" || !contains(settings.Events, "quarantine_expiring") {
			continue
		}
		list := byProject[project]
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		attempts, status, sendErr := s.sendNotification(ctx, settings.WebhookURL,
			quarantineExpiryMessage(meta, runID, list, project))
		cancel()
		delivery := store.NotificationDelivery{
			At: time.Now(), Event: "quarantine_expiring", Project: project, RunID: runID,
			OK: sendErr == nil, StatusCode: status, Attempts: attempts,
		}
		if sendErr != nil {
			delivery.Error = sendErr.Error()
			s.logWarn("expiry notification gagal", "run", runID, "project", project, "err", sendErr)
		}
		recordCtx, recordCancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := s.Store.RecordNotificationDelivery(recordCtx, delivery); err != nil {
			s.logWarn("delivery log gagal disimpan", "run", runID, "project", project, "err", err)
		}
		if sendErr != nil {
			recordCancel()
			continue
		}
		keys := make([]string, 0, len(list))
		for _, quarantine := range list {
			keys = append(keys, quarantine.Key)
		}
		if err := s.Store.MarkQuarantineExpiryNotified(recordCtx, keys); err != nil {
			s.logWarn("expiry notification tidak bisa ditandai", "run", runID, "project", project, "err", err)
		}
		recordCancel()
	}
}

func quarantineExpiryMessage(meta store.RunMeta, runID int64, expiring []store.Quarantine, project string) notify.Message {
	noun, verb := "quarantines", "expire"
	if len(expiring) == 1 {
		noun, verb = "quarantine", "expires"
	}
	title := fmt.Sprintf("Redline: %d %s %s within 24h", len(expiring), noun, verb)
	var body strings.Builder
	const limit = 8
	for i, quarantine := range expiring {
		if i == limit {
			fmt.Fprintf(&body, "…and %d more\n", len(expiring)-limit)
			break
		}
		name := quarantine.TestCaseID
		if name == "" {
			name = quarantine.Key
		}
		body.WriteString("• " + name)
		if quarantine.Owner != "" {
			body.WriteString(" · owner " + quarantine.Owner)
		}
		body.WriteString(" · until " + quarantine.Until.Format("2006-01-02"))
		body.WriteString("\n")
		if quarantine.Reason != "" {
			body.WriteString("  " + clipText(quarantine.Reason, 180) + "\n")
		}
	}
	commit := meta.CommitSHA
	if len(commit) > 7 {
		commit = commit[:7]
	}
	if commit == "" {
		commit = "—"
	}
	branch := meta.Branch
	if branch == "" {
		branch = "—"
	}
	fields := []notify.Field{
		{Name: "Branch", Value: branch, Inline: true},
		{Name: "Commit", Value: commit, Inline: true},
		{Name: "Expiring", Value: fmt.Sprintf("%d", len(expiring)), Inline: true},
	}
	if project != "" {
		fields = append(fields, notify.Field{Name: "Project", Value: project, Inline: true})
	}
	return notify.Message{
		Title:       title,
		Description: body.String(),
		Color:       0xD29922,
		Fields:      fields,
		Footer:      fmt.Sprintf("Redline · run #%d", runID),
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// sendNotification applies the retry policy: one retry for network errors, 429 and 5xx. Other 4xx
// responses are final. Discord's retry_after is honoured but capped at five seconds so the reporter
// and the test button stay responsive.
func (s *Server) sendNotification(
	ctx context.Context,
	webhook string,
	message notify.Message,
) (attempts int, status int, err error) {
	if s.Notifier == nil {
		return 0, 0, fmt.Errorf("notifier belum dikonfigurasi")
	}
	for attempt := 1; attempt <= 2; attempt++ {
		err = s.Notifier.Send(ctx, webhook, message)
		attempts = attempt
		if err == nil {
			return attempts, 0, nil
		}
		status = 0
		delay := time.Duration(0)
		var deliveryErr *notify.DeliveryError
		if errors.As(err, &deliveryErr) {
			status = deliveryErr.StatusCode
			delay = deliveryErr.RetryAfter
			if status != 0 && status != http.StatusTooManyRequests && status < 500 {
				return attempts, status, err
			}
		}
		if attempt == 2 {
			return attempts, status, err
		}
		if delay <= 0 {
			delay = time.Second
		}
		if delay > 5*time.Second {
			delay = 5 * time.Second
		}
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return attempts, status, ctx.Err()
		}
	}
	return attempts, status, err
}

func notificationCounts(res *store.IngestResult, project string) (failed, flaky int) {
	if project == "" {
		return res.Failed, res.Flaky
	}
	for _, list := range [][]store.GroupChange{res.New, res.Regressed, res.Recurring} {
		for _, change := range list {
			if change.Project == project {
				failed++
			}
		}
	}
	prefix := project + " › "
	for _, key := range res.FlakyTests {
		if strings.HasPrefix(key, prefix) {
			flaky++
		}
	}
	return failed, flaky
}

func notificationMessage(
	meta store.RunMeta,
	res *store.IngestResult,
	changes []store.GroupChange,
	parts []string,
	project string,
	failed int,
	flaky int,
) notify.Message {
	title := fmt.Sprintf("Redline: %s in run #%d", strings.Join(parts, " + "), res.RunID)

	var body strings.Builder
	const limit = 8
	for i, change := range changes {
		if i == limit {
			fmt.Fprintf(&body, "…and %d more\n", len(changes)-limit)
			break
		}
		name := change.TestCaseID
		if name == "" {
			name = change.TestKey
		}
		body.WriteString("• " + name)
		if change.TestCaseID != "" && change.TestKey != "" {
			body.WriteString(" · " + clipText(change.TestKey, 120))
		}
		body.WriteString("\n")
		if change.Error != "" {
			body.WriteString("  " + clipText(firstLine(change.Error), 180) + "\n")
		}
	}

	commit := meta.CommitSHA
	if len(commit) > 7 {
		commit = commit[:7]
	}
	if commit == "" {
		commit = "—"
	}
	branch := meta.Branch
	if branch == "" {
		branch = "—"
	}
	source := meta.Source
	if source == "" {
		source = "local"
	}

	fields := []notify.Field{
		{Name: "Branch", Value: branch, Inline: true},
		{Name: "Commit", Value: commit, Inline: true},
		{Name: "Source", Value: source, Inline: true},
		{Name: "Failed", Value: fmt.Sprintf("%d", failed), Inline: true},
		{Name: "Flaky", Value: fmt.Sprintf("%d", flaky), Inline: true},
	}
	if project != "" {
		fields = append(fields, notify.Field{Name: "Project", Value: project, Inline: true})
	}
	return notify.Message{
		Title:       title,
		Description: body.String(),
		Color:       notificationColor,
		Fields:      fields,
		Footer:      fmt.Sprintf("Redline · run #%d", res.RunID),
	}
}

func (s *Server) logWarn(message string, args ...any) {
	if s.Log != nil {
		s.Log.Warn(message, args...)
	}
}

func clipText(value string, max int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max-1]) + "…"
}

func firstLine(value string) string {
	value = strings.TrimSpace(value)
	if i := strings.IndexByte(value, '\n'); i >= 0 {
		return value[:i]
	}
	return value
}
