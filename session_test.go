package reviewer

import (
	"context"
	"encoding/json"
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

func TestSessionWait_ReturnsSubmitThatLandedBeforeTheCall(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s, err := StartSession(ctx, writeTempSpec(t), 0, true)
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	defer s.Close()

	// The human can submit while the agent is still editing, i.e. between one Wait returning
	// and the next being called. Waking only the waiters present at broadcast time would drop
	// that submit on the floor, and the agent would report a timeout while comments sit
	// unanswered.
	submitComment(t, s, "landed while the agent was busy")

	got := s.Wait(ctx, 100*time.Millisecond)
	if got.Outcome != WaitSubmitted {
		t.Fatalf("got outcome %q, want %q — a submit before the call must not be lost", got.Outcome, WaitSubmitted)
	}
	if len(got.Comments) != 1 {
		t.Fatalf("got %d comments, want 1", len(got.Comments))
	}
}

func TestSessionWait_DoesNotRedeliverTheSameSubmit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s, err := StartSession(ctx, writeTempSpec(t), 0, true)
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	defer s.Close()

	submitComment(t, s, "first round")

	if got := s.Wait(ctx, 100*time.Millisecond); got.Outcome != WaitSubmitted {
		t.Fatalf("first wait: got outcome %q, want %q", got.Outcome, WaitSubmitted)
	}
	// Without a new submit the next wait must block, not replay the round just delivered.
	if got := s.Wait(ctx, 100*time.Millisecond); got.Outcome != WaitTimeout {
		t.Fatalf("second wait: got outcome %q, want %q", got.Outcome, WaitTimeout)
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

// submitComment posts a single comment and returns the ID the server assigned it.
func submitComment(t *testing.T, s *ReviewSession, text string) string {
	t.Helper()
	body := `{"comments":[{"text":"` + text + `","timestamp":"2026-01-01T00:00:00Z","author":"human","status":"open"}],"summary":""}`
	resp, err := http.Post(s.URL()+"/api/feedback", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}
	resp.Body.Close()
	fb := s.readFeedbackDoc()
	if len(fb.Comments) != 1 {
		t.Fatalf("got %d comments after submit, want 1", len(fb.Comments))
	}
	return fb.Comments[0].ID
}

func TestSessionReply_WritesReplyAndSummary(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s, err := StartSession(ctx, writeTempSpec(t), 0, true)
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	defer s.Close()

	id := submitComment(t, s, "needs work")

	if err := s.Reply([]ReplyInput{{CommentID: id, Reply: "fixed in section 2"}}, "round 1 changes"); err != nil {
		t.Fatalf("Reply failed: %v", err)
	}

	fb := s.readFeedbackDoc()
	if fb.Comments[0].Reply != "fixed in section 2" {
		t.Fatalf("got reply %q, want %q", fb.Comments[0].Reply, "fixed in section 2")
	}
	if fb.Comments[0].ReplyTimestamp == "" {
		t.Fatal("ReplyTimestamp should have been set")
	}
	if fb.Summary != "round 1 changes" {
		t.Fatalf("got summary %q, want %q", fb.Summary, "round 1 changes")
	}
	// The original human fields must survive untouched.
	if fb.Comments[0].Text != "needs work" {
		t.Fatalf("got text %q, want %q", fb.Comments[0].Text, "needs work")
	}
}

func TestSessionReply_CannotResolveComment(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s, err := StartSession(ctx, writeTempSpec(t), 0, true)
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	defer s.Close()

	id := submitComment(t, s, "needs work")

	if err := s.Reply([]ReplyInput{{CommentID: id, Reply: "done"}}, ""); err != nil {
		t.Fatalf("Reply failed: %v", err)
	}

	// Resolution is the human's decision; replying must never flip it.
	if got := s.readFeedbackDoc().Comments[0].Status; got != StatusOpen {
		t.Fatalf("got status %q, want %q — the agent must not resolve comments", got, StatusOpen)
	}
}

func TestSessionReply_RejectsUnknownCommentID(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s, err := StartSession(ctx, writeTempSpec(t), 0, true)
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	defer s.Close()

	submitComment(t, s, "needs work")

	if err := s.Reply([]ReplyInput{{CommentID: "nonexistent", Reply: "done"}}, ""); err == nil {
		t.Fatal("Reply should reject an unknown comment ID")
	}
}

func TestSessionProgress_WritesStatusFile(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s, err := StartSession(ctx, writeTempSpec(t), 0, true)
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	defer s.Close()

	if err := s.Progress(StateWorking, "editing the document"); err != nil {
		t.Fatalf("Progress failed: %v", err)
	}

	raw, err := os.ReadFile(StatusPath(s.InputPath()))
	if err != nil {
		t.Fatalf("failed to read status file: %v", err)
	}
	var got AgentStatus
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("status file is not valid JSON: %v", err)
	}
	if got.State != StateWorking || got.Message != "editing the document" {
		t.Fatalf("got %+v, want state=%q message=%q", got, StateWorking, "editing the document")
	}
}

func TestSessionProgress_RejectsUnknownState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s, err := StartSession(ctx, writeTempSpec(t), 0, true)
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	defer s.Close()

	if err := s.Progress("thinking", "hmm"); err == nil {
		t.Fatal("Progress should reject a state outside working|idle")
	}
}
