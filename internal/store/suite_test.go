package store_test

import (
	"context"
	"testing"

	"redline/internal/store"
)

// newStore membuat database test kosong. sources = STATUS_FROM; kosong berarti semua source mengubah status.
func newStore(t *testing.T, sources ...string) *store.Store {
	s := store.TestStore(context.Background(), t, "redlinetest-suite")
	s.StatusSources = sources
	return s
}
