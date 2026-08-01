package reviewer

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// writeTempSpec creates a minimal markdown document and returns its path.
func writeTempSpec(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "spec.md")
	if err := os.WriteFile(path, []byte("# Title\n\nBody paragraph.\n"), 0644); err != nil {
		t.Fatalf("failed to write temp spec: %v", err)
	}
	return path
}

func TestStartSession_ServesAndCloses(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s, err := StartSession(ctx, writeTempSpec(t), 0, true)
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}

	resp, err := http.Get(s.URL())
	if err != nil {
		t.Fatalf("GET %s failed: %v", s.URL(), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got status %d, want 200", resp.StatusCode)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	select {
	case <-s.Done():
	default:
		t.Fatal("Done() should be closed after Close()")
	}

	// Close is idempotent: a second call must not panic or error.
	if err := s.Close(); err != nil {
		t.Fatalf("second Close failed: %v", err)
	}
}

func TestStartSession_ClosesServerAfterSessionAlreadyEnded(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s, err := StartSession(ctx, writeTempSpec(t), 0, true)
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	url := s.URL()

	// This is the "End Review" path: the session ends first, and only then does the owner
	// call Close to release the listener. Guarding shutdown with the same Once that guards
	// done would skip the shutdown entirely here and leave the port bound.
	resp, err := http.Post(url+"/api/close", "application/json", nil)
	if err != nil {
		t.Fatalf("close request failed: %v", err)
	}
	resp.Body.Close()

	<-s.Done()
	if err := s.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	if _, err := http.Get(url); err == nil {
		t.Fatal("server should no longer accept connections after Close()")
	}
}
