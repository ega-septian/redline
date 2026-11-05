package api

import (
	"context"
	"fmt"
	"strings"
	"time"

	"redline/internal/notify"
	"redline/internal/store"
)

const notificationColor = 0xE5484D

// notifyRun sends one summary per run. It runs in the background; a slow or broken webhook must not
// block the reporter. Only new and regressed failures are delivered.
func (s *Server) notifyRun(meta store.RunMeta, res *store.IngestResult) {
	settings, err := s.Store.NotificationSettings(context.Background())
	if err != nil {
		s.logWarn("notifikasi tidak bisa membaca settings", "run", res.RunID, "err", err)
		return
	}
	if !settings.Enabled || settings.WebhookURL == "" {
		return
	}
	events := map[string]bool{}
	for _, event := range settings.Events {
		events[event] = true
	}
	changes := make([]store.GroupChange, 0, len(res.New)+len(res.Regressed))
	parts := make([]string, 0, 2)
	if len(res.New) > 0 && events["new_failure"] {
		changes = append(changes, res.New...)
		parts = append(parts, fmt.Sprintf("%d new", len(res.New)))
	}
	if len(res.Regressed) > 0 && events["regressed"] {
		changes = append(changes, res.Regressed...)
		parts = append(parts, fmt.Sprintf("%d regressed", len(res.Regressed)))
	}
	if len(changes) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := s.Notifier.Send(ctx, settings.WebhookURL, notificationMessage(meta, res, changes, parts)); err != nil {
		s.logWarn("notifikasi gagal", "run", res.RunID, "err", err)
	}
}

func notificationMessage(
	meta store.RunMeta,
	res *store.IngestResult,
	changes []store.GroupChange,
	parts []string,
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

	return notify.Message{
		Title:       title,
		Description: body.String(),
		Color:       notificationColor,
		Fields: []notify.Field{
			{Name: "Branch", Value: branch, Inline: true},
			{Name: "Commit", Value: commit, Inline: true},
			{Name: "Source", Value: source, Inline: true},
			{Name: "Failed", Value: fmt.Sprintf("%d", res.Failed), Inline: true},
			{Name: "Flaky", Value: fmt.Sprintf("%d", res.Flaky), Inline: true},
		},
		Footer: fmt.Sprintf("Redline · run #%d", res.RunID),
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
