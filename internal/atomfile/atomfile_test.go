package atomfile

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type payload struct {
	Token string `json:"token"`
	N     int    `json:"n"`
}

func TestReadJSON_MissingFile(t *testing.T) {
	var p payload
	found, err := ReadJSON(filepath.Join(t.TempDir(), "absent.json"), &p)
	if err != nil {
		t.Fatalf("missing file must not error: %v", err)
	}
	if found {
		t.Fatal("missing file reported found=true")
	}
}

func TestWriteReadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.json")
	want := payload{Token: "secret", N: 7}
	if err := WriteJSON(path, want); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}

	var got payload
	found, err := ReadJSON(path, &got)
	if err != nil || !found {
		t.Fatalf("ReadJSON = found %v, err %v", found, err)
	}
	if got != want {
		t.Fatalf("round-trip = %+v, want %+v", got, want)
	}

	// Overwrite replaces the previous content in full.
	want2 := payload{Token: "rotated", N: 8}
	if err := WriteJSON(path, want2); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if _, err := ReadJSON(path, &got); err != nil || got != want2 {
		t.Fatalf("after overwrite = %+v, err %v; want %+v", got, err, want2)
	}
}

func TestWriteJSON_OwnerOnlyPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not enforced on Windows")
	}
	path := filepath.Join(t.TempDir(), "store.json")
	if err := WriteJSON(path, payload{Token: "secret"}); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("mode = %o, want 600", perm)
	}
}

func TestReadJSON_MalformedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	var p payload
	if _, err := ReadJSON(path, &p); err == nil || !strings.Contains(err.Error(), "parsing") {
		t.Fatalf("malformed file: err = %v, want parsing error", err)
	}
}
