package davidlloyd

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSessionStoreRoundTripWithLastSynced(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	store := NewSessionStore(path)

	if sess, err := store.Load(); err != nil || sess != nil {
		t.Fatalf("missing file: got (%v, %v), want (nil, nil)", sess, err)
	}

	in := &StoredSession{
		UserID:               "u1",
		Cookie:               "c1",
		SavedAt:              time.Date(2026, 7, 10, 8, 0, 0, 0, time.UTC),
		LastSyncedMeasuredAt: "2026-07-09T06:10:23Z",
		LastSyncedKg:         82.1,
	}
	if err := store.Save(in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if *got != *in {
		t.Fatalf("round trip mismatch:\n got=%+v\nwant=%+v", got, in)
	}
}
