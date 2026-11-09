package store_test

import (
	"context"
	"errors"
	"testing"

	"redline/internal/store"
)

func TestContractSources(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	list, err := s.ContractSources(ctx)
	if err != nil || len(list) != 0 {
		t.Fatalf("awal harus kosong: %+v %v", list, err)
	}
	if _, err := s.ContractSource(ctx, "toolshop"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("source belum ada: mau ErrNotFound, dapat %v", err)
	}

	source, err := s.SetContractSource(ctx, store.ContractSource{
		Project: "toolshop", URL: "https://api.example.com/docs", Enabled: true, By: "ega",
	})
	if err != nil {
		t.Fatal(err)
	}
	if source.Project != "toolshop" || source.URL != "https://api.example.com/docs" ||
		!source.Enabled || source.By != "ega" || source.UpdatedAt.IsZero() {
		t.Errorf("source tersimpan salah: %+v", source)
	}
	if source.LastStatus != "unknown" || source.LastCheckedAt != nil {
		t.Errorf("source baru harus unknown: %+v", source)
	}

	if err := s.RecordSourceCheck(ctx, "toolshop", store.SourceCheckRecord{
		Status: "valid", Version: "OpenAPI 3.2.0", Endpoints: 19, DurationMS: 42,
	}); err != nil {
		t.Fatal(err)
	}
	checked, err := s.ContractSource(ctx, "toolshop")
	if err != nil {
		t.Fatal(err)
	}
	if checked.LastStatus != "valid" || checked.LastVersion != "OpenAPI 3.2.0" ||
		checked.LastEndpoints != 19 || checked.LastDuration != 42 || checked.LastCheckedAt == nil {
		t.Errorf("hasil check tidak tersimpan: %+v", checked)
	}

	// Saving the same URL keeps the last check; changing the URL resets it.
	if _, err := s.SetContractSource(ctx, store.ContractSource{
		Project: "toolshop", URL: "https://api.example.com/docs", Enabled: true, By: "ega",
	}); err != nil {
		t.Fatal(err)
	}
	if same, _ := s.ContractSource(ctx, "toolshop"); same.LastStatus != "valid" {
		t.Errorf("URL sama tidak boleh mereset check: %+v", same)
	}
	if _, err := s.SetContractSource(ctx, store.ContractSource{
		Project: "toolshop", URL: "https://api.example.com/v2/docs", Enabled: true, By: "budi",
	}); err != nil {
		t.Fatal(err)
	}
	changed, err := s.ContractSource(ctx, "toolshop")
	if err != nil {
		t.Fatal(err)
	}
	if changed.LastStatus != "unknown" || changed.LastCheckedAt != nil || changed.LastEndpoints != 0 {
		t.Errorf("URL berubah harus mereset check: %+v", changed)
	}
	if changed.By != "budi" || changed.URL != "https://api.example.com/v2/docs" {
		t.Errorf("update tidak tersimpan: %+v", changed)
	}

	if err := s.RecordSourceCheck(ctx, "missing", store.SourceCheckRecord{Status: "valid"}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("check untuk source env: mau ErrNotFound, dapat %v", err)
	}
	if err := s.DeleteContractSource(ctx, "toolshop"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ContractSource(ctx, "toolshop"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("setelah delete: mau ErrNotFound, dapat %v", err)
	}
	if err := s.DeleteContractSource(ctx, "toolshop"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("delete kedua: mau ErrNotFound, dapat %v", err)
	}
}

func TestNotificationSettingsStore(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	defaults, err := s.NotificationSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if defaults.Enabled || len(defaults.Events) != 2 || defaults.WebhookURL != "" {
		t.Errorf("default salah: %+v", defaults)
	}

	saved, err := s.SetNotificationSettings(ctx, store.NotificationSettings{
		Enabled: true, WebhookURL: "https://hooks.example.com/services/T/B/SECRET",
		Events: []string{"new_failure", "regressed"}, By: "ega",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !saved.Enabled || saved.By != "ega" || saved.UpdatedAt.IsZero() ||
		saved.WebhookURL != "https://hooks.example.com/services/T/B/SECRET" {
		t.Errorf("settings tersimpan salah: %+v", saved)
	}

	again, err := s.NotificationSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !again.Enabled || len(again.Events) != 2 || again.Events[0] != "new_failure" {
		t.Errorf("settings tidak terbaca kembali: %+v", again)
	}

	if _, err := s.SetNotificationSettings(ctx, store.NotificationSettings{
		Enabled: false, Events: []string{"quarantine_expiring"}, By: "budi",
	}); err != nil {
		t.Fatal(err)
	}
	updated, _ := s.NotificationSettings(ctx)
	if updated.Enabled || len(updated.Events) != 1 || updated.Events[0] != "quarantine_expiring" ||
		updated.WebhookURL != "" || updated.By != "budi" {
		t.Errorf("update settings salah: %+v", updated)
	}
}

func TestNotificationChannelStore(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	global, err := s.SetNotificationSettings(ctx, store.NotificationSettings{
		Enabled: true, WebhookURL: "https://discord.test/default", Events: []string{"new_failure"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if channels, err := s.NotificationChannels(ctx); err != nil || len(channels) != 0 {
		t.Fatalf("awal harus kosong: %+v %v", channels, err)
	}

	channel, err := s.SetNotificationChannel(ctx, store.NotificationChannel{
		Project: "toolshop", Enabled: true, WebhookURL: "https://discord.test/toolshop",
		Events: []string{"new_failure", "regressed"}, By: "ega",
	})
	if err != nil {
		t.Fatal(err)
	}
	if channel.Project != "toolshop" || !channel.Enabled || channel.UpdatedAt.IsZero() ||
		channel.WebhookURL != "https://discord.test/toolshop" {
		t.Errorf("channel tersimpan salah: %+v", channel)
	}
	if channels, err := s.NotificationChannels(ctx); err != nil || len(channels) != 1 ||
		channels[0].Project != "toolshop" {
		t.Errorf("daftar channel salah: %+v %v", channels, err)
	}

	resolved, err := s.NotificationSettingsFor(ctx, "toolshop")
	if err != nil || !resolved.Enabled || resolved.WebhookURL != "https://discord.test/toolshop" ||
		len(resolved.Events) != 2 {
		t.Errorf("override tidak dipakai: %+v %v", resolved, err)
	}
	fallback, err := s.NotificationSettingsFor(ctx, "web")
	if err != nil || fallback.WebhookURL != global.WebhookURL {
		t.Errorf("project tanpa override harus memakai default: %+v %v", fallback, err)
	}

	if _, err := s.SetNotificationChannel(ctx, store.NotificationChannel{
		Project: "web", Enabled: false, Events: []string{}, By: "ega",
	}); err != nil {
		t.Fatal(err)
	}
	disabled, err := s.NotificationSettingsFor(ctx, "web")
	if err != nil || disabled.Enabled {
		t.Errorf("override disabled harus menonaktifkan project: %+v %v", disabled, err)
	}

	if err := s.DeleteNotificationChannel(ctx, "toolshop"); err != nil {
		t.Fatal(err)
	}
	resolved, err = s.NotificationSettingsFor(ctx, "toolshop")
	if err != nil || resolved.WebhookURL != global.WebhookURL {
		t.Errorf("setelah delete harus kembali ke default: %+v %v", resolved, err)
	}
	if err := s.DeleteNotificationChannel(ctx, "toolshop"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("delete kedua: mau ErrNotFound, dapat %v", err)
	}
}
