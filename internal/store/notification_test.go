package store_test

import (
	"context"
	"testing"
	"time"

	"redline/internal/store"
)

func TestNotificationDeliveries(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	last, err := s.LastNotificationDelivery(ctx)
	if err != nil || last != nil {
		t.Fatalf("awal harus kosong: %+v %v", last, err)
	}

	first := store.NotificationDelivery{
		At: time.Now().Truncate(time.Second), Event: "new_failure", RunID: 7,
		OK: true, Attempts: 1,
	}
	if err := s.RecordNotificationDelivery(ctx, first); err != nil {
		t.Fatal(err)
	}
	got, err := s.LastNotificationDelivery(ctx)
	if err != nil || got == nil || got.Event != "new_failure" || got.RunID != 7 ||
		!got.OK || got.Attempts != 1 {
		t.Fatalf("delivery pertama salah: %+v %v", got, err)
	}

	second := store.NotificationDelivery{
		At: time.Now().Add(time.Minute).Truncate(time.Second), Event: "test",
		OK: false, StatusCode: 429, Attempts: 2, Error: "rate limited",
	}
	if err := s.RecordNotificationDelivery(ctx, second); err != nil {
		t.Fatal(err)
	}
	got, err = s.LastNotificationDelivery(ctx)
	if err != nil || got == nil || got.Event != "test" || got.OK || got.StatusCode != 429 ||
		got.Attempts != 2 || got.Error != "rate limited" {
		t.Fatalf("delivery terbaru salah: %+v %v", got, err)
	}

	history, err := s.NotificationDeliveries(ctx, 1)
	if err != nil || len(history) != 1 || history[0].Event != "test" {
		t.Fatalf("history limit salah: %+v %v", history, err)
	}
	history, err = s.NotificationDeliveries(ctx, 10)
	if err != nil || len(history) != 2 || history[0].Event != "test" ||
		history[1].Event != "new_failure" {
		t.Fatalf("history urutan salah: %+v %v", history, err)
	}
}
