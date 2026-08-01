package reviewer

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestSessionWait_ReturnsSubmittedComments(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s, err := StartSession(ctx, writeTempSpec(t), 0, true)
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	defer s.Close()

	resultCh := make(chan WaitResult, 1)
	go func() { resultCh <- s.Wait(ctx, 5*time.Second) }()

	// Give the waiter a moment to subscribe before submitting.
	time.Sleep(50 * time.Millisecond)
	body := `{"comments":[{"text":"needs work","timestamp":"2026-01-01T00:00:00Z","author":"human","status":"open"}],"summary":""}`
	resp, err := http.Post(s.URL()+"/api/feedback", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}
	resp.Body.Close()

	select {
	case got := <-resultCh:
		if got.Outcome != WaitSubmitted {
			t.Fatalf("got outcome %q, want %q", got.Outcome, WaitSubmitted)
		}
		if len(got.Comments) != 1 {
			t.Fatalf("got %d comments, want 1", len(got.Comments))
		}
		if got.Comments[0].Text != "needs work" {
			t.Fatalf("got text %q, want %q", got.Comments[0].Text, "needs work")
		}
		if got.Comments[0].ID == "" {
			t.Fatal("comment ID should have been assigned on submit")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Wait did not return after a submit")
	}
}

func TestSessionWait_TimesOutWithoutError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s, err := StartSession(ctx, writeTempSpec(t), 0, true)
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	defer s.Close()

	got := s.Wait(ctx, 100*time.Millisecond)
	if got.Outcome != WaitTimeout {
		t.Fatalf("got outcome %q, want %q", got.Outcome, WaitTimeout)
	}
}

func TestSessionWait_ReportsSessionEnded(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s, err := StartSession(ctx, writeTempSpec(t), 0, true)
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}

	resultCh := make(chan WaitResult, 1)
	go func() { resultCh <- s.Wait(ctx, 5*time.Second) }()

	time.Sleep(50 * time.Millisecond)
	if err := s.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	select {
	case got := <-resultCh:
		if got.Outcome != WaitSessionEnded {
			t.Fatalf("got outcome %q, want %q", got.Outcome, WaitSessionEnded)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Wait did not return after the session ended")
	}
}
