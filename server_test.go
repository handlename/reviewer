package reviewer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// startTestServer boots a review server on a random port and returns its URL plus a stop func.
func startTestServer(t *testing.T, inputPath string) (string, func()) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	readyChan := make(chan string, 1)
	errChan := make(chan error, 1)

	go func() {
		errChan <- StartReviewServer(ctx, inputPath, 0, true, readyChan)
	}()

	var url string
	select {
	case url = <-readyChan:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("timed out waiting for server to signal readiness")
	}

	stop := func() {
		cancel()
		select {
		case <-errChan:
		case <-time.After(3 * time.Second):
			t.Error("timed out waiting for server shutdown")
		}
	}
	return url, stop
}

func writeMarkdown(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatalf("failed to write markdown: %v", err)
	}
}

// The server re-renders the document on every request, so an edit made while the server
// is running is reflected on the next GET (which is what an auto-reload triggers).
func TestStartReviewServer_RerendersOnEachRequest(t *testing.T) {
	tempDir := t.TempDir()
	inputPath := filepath.Join(tempDir, "spec.md")
	writeMarkdown(t, inputPath, "# Title\n\nOriginal body paragraph.\n")

	url, stop := startTestServer(t, inputPath)
	defer stop()

	body := httpGetBody(t, url)
	if !strings.Contains(body, "Original body paragraph.") {
		t.Errorf("expected rendered body to contain original text, got:\n%s", body)
	}

	// Simulate the agent editing the target document.
	writeMarkdown(t, inputPath, "# Title\n\nUpdated by the agent.\n")

	body = httpGetBody(t, url)
	if !strings.Contains(body, "Updated by the agent.") {
		t.Errorf("expected re-rendered body to reflect the edit, got:\n%s", body)
	}
	if strings.Contains(body, "Original body paragraph.") {
		t.Errorf("expected old text to be gone after edit, got:\n%s", body)
	}
}

// Submitting feedback writes the sidecar file but must NOT shut the server down.
func TestStartReviewServer_SubmitKeepsServerAlive(t *testing.T) {
	tempDir := t.TempDir()
	inputPath := filepath.Join(tempDir, "spec.md")
	writeMarkdown(t, inputPath, "# Spec\n\nContent.\n")

	url, stop := startTestServer(t, inputPath)
	defer stop()

	fb := Feedback{Comments: []Comment{{Text: "Please clarify", Timestamp: time.Now().Format(time.RFC3339), Author: AuthorHuman, Status: StatusOpen}}}
	postFeedback(t, url, fb)

	// Submitting seeds a "working" status so the activity panel survives the reload the submit triggers.
	if body := httpGetBody(t, url+"/api/status"); !strings.Contains(body, `"state":"working"`) {
		t.Errorf("expected working status seeded after submit, got: %s", body)
	}

	// Server must still respond after a submit.
	resp, err := http.Get(url + "/")
	if err != nil {
		t.Fatalf("server should still be alive after submit: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 from live server after submit, got %d", resp.StatusCode)
	}

	// Sidecar file written with the comment.
	feedbackPath := FeedbackPath(inputPath)
	var written Feedback
	readJSONFile(t, feedbackPath, &written)
	if len(written.Comments) != 1 || written.Comments[0].Text != "Please clarify" {
		t.Errorf("unexpected written feedback: %+v", written)
	}

	// GET returns the {comments,summary} shape.
	getResp, err := http.Get(url + "/api/feedback")
	if err != nil {
		t.Fatalf("failed to GET feedback: %v", err)
	}
	defer getResp.Body.Close()
	var got Feedback
	if err := json.NewDecoder(getResp.Body).Decode(&got); err != nil {
		t.Fatalf("GET feedback did not return Feedback shape: %v", err)
	}
	if len(got.Comments) != 1 {
		t.Errorf("expected 1 comment from GET, got %+v", got)
	}
}

// A thread the human resolves survives one round — the agent has to be told — and the submit
// after that drops it.
func TestStartReviewServer_PrunesResolvedOneRoundLater(t *testing.T) {
	tempDir := t.TempDir()
	inputPath := filepath.Join(tempDir, "spec.md")
	writeMarkdown(t, inputPath, "# Spec\n\nContent.\n")

	url, stop := startTestServer(t, inputPath)
	defer stop()

	now := time.Now().Format(time.RFC3339)
	postFeedback(t, url, Feedback{
		Summary: "Addressed one item.",
		Comments: []Comment{
			{Text: "still open", Timestamp: now, Status: StatusOpen},
			{Text: "done, resolved", Timestamp: now, Status: StatusResolved, Author: AuthorHuman},
		},
	})

	feedbackPath := FeedbackPath(inputPath)
	var written Feedback
	readJSONFile(t, feedbackPath, &written)
	if len(written.Comments) != 2 {
		t.Fatalf("a newly resolved thread must survive one round, got %+v", written.Comments)
	}

	// The page posts back what it holds, resolved thread included; now it goes.
	postFeedback(t, url, written)

	readJSONFile(t, feedbackPath, &written)
	if len(written.Comments) != 1 {
		t.Fatalf("expected the resolved thread pruned, got %+v", written.Comments)
	}
	if written.Comments[0].Text != "still open" {
		t.Errorf("expected the open comment to remain, got %q", written.Comments[0].Text)
	}
}

// The prune rule is a two-input decision — what is stored, and what the page just posted.
func TestPruneResolved(t *testing.T) {
	tests := []struct {
		name     string
		stored   []Comment
		incoming []Comment
		want     int
	}{
		{"open stays open", []Comment{{ID: "a", Status: StatusOpen}}, []Comment{{ID: "a", Status: StatusOpen}}, 1},
		{"a new comment is kept", nil, []Comment{{ID: "", Status: StatusOpen}}, 1},
		{"resolved just now survives", []Comment{{ID: "a", Status: StatusOpen}}, []Comment{{ID: "a", Status: StatusResolved}}, 1},
		{"resolved already delivered is dropped", []Comment{{ID: "a", Status: StatusResolved}}, []Comment{{ID: "a", Status: StatusResolved}}, 0},
		{"reopened is kept", []Comment{{ID: "a", Status: StatusResolved}}, []Comment{{ID: "a", Status: StatusOpen}}, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pruneResolved(tt.incoming, resolvedIDs(tt.stored))
			if len(got) != tt.want {
				t.Errorf("kept %d comments, want %d: %#v", len(got), tt.want, got)
			}
		})
	}
}

// The "End Review" button (POST /api/close) shuts the server down gracefully.
func TestStartReviewServer_CloseEndsSession(t *testing.T) {
	tempDir := t.TempDir()
	inputPath := filepath.Join(tempDir, "spec.md")
	writeMarkdown(t, inputPath, "# Spec\n\nContent.\n")

	ctx := t.Context()
	readyChan := make(chan string, 1)
	errChan := make(chan error, 1)
	go func() { errChan <- StartReviewServer(ctx, inputPath, 0, true, readyChan) }()

	var url string
	select {
	case url = <-readyChan:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for readiness")
	}

	resp, err := http.Post(url+"/api/close", "application/json", nil)
	if err != nil {
		t.Fatalf("failed to POST close: %v", err)
	}
	resp.Body.Close()

	select {
	case err := <-errChan:
		if err != nil {
			t.Errorf("server shutdown returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Error("server did not shut down after /api/close")
	}
}

// Editing the document pushes a "reload" to connected SSE clients.
func TestStartReviewServer_SSEReloadOnEdit(t *testing.T) {
	tempDir := t.TempDir()
	inputPath := filepath.Join(tempDir, "spec.md")
	writeMarkdown(t, inputPath, "# Spec\n\nBefore.\n")

	url, stop := startTestServer(t, inputPath)
	defer stop()

	reqCtx, reqCancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer reqCancel()
	req, _ := http.NewRequestWithContext(reqCtx, http.MethodGet, url+"/api/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to connect to SSE: %v", err)
	}
	defer resp.Body.Close()

	reloaded := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data:") && strings.Contains(line, `"kind":"reload"`) {
				reloaded <- true
				return
			}
		}
	}()

	// Give the SSE handler a moment to register, then edit the document.
	time.Sleep(150 * time.Millisecond)
	writeMarkdown(t, inputPath, "# Spec\n\nAfter.\n")

	select {
	case <-reloaded:
	case <-time.After(3 * time.Second):
		t.Error("did not receive SSE reload event after editing the document")
	}
}

// GET /api/wait blocks until the next submit, then returns 200 with the current feedback JSON.
// This is the long-poll path the agent's monitor uses to detect submits with near-zero latency.
func TestStartReviewServer_WaitBlocksUntilSubmit(t *testing.T) {
	tempDir := t.TempDir()
	inputPath := filepath.Join(tempDir, "spec.md")
	writeMarkdown(t, inputPath, "# Spec\n\nContent.\n")

	url, stop := startTestServer(t, inputPath)
	defer stop()

	type waitResult struct {
		code int
		body string
	}
	done := make(chan waitResult, 1)
	go func() {
		resp, err := http.Get(url + "/api/wait")
		if err != nil {
			done <- waitResult{code: -1}
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		done <- waitResult{code: resp.StatusCode, body: string(b)}
	}()

	// The wait must NOT return before a submit happens.
	select {
	case r := <-done:
		t.Fatalf("/api/wait returned before any submit (code=%d)", r.code)
	case <-time.After(300 * time.Millisecond):
	}

	// Submit — this must release the waiter with 200 + the feedback JSON.
	fb := Feedback{Comments: []Comment{{Text: "please clarify", Timestamp: time.Now().Format(time.RFC3339), Author: AuthorHuman, Status: StatusOpen}}}
	postFeedback(t, url, fb)

	select {
	case r := <-done:
		if r.code != http.StatusOK {
			t.Fatalf("expected 200 from /api/wait after submit, got %d", r.code)
		}
		if !strings.Contains(r.body, "please clarify") {
			t.Errorf("expected wait body to contain the submitted feedback, got: %s", r.body)
		}
	case <-time.After(3 * time.Second):
		t.Error("/api/wait did not return after a submit")
	}
}

// With no activity, /api/wait returns 204 after its timeout so the client can re-poll (long-poll convention).
func TestStartReviewServer_WaitTimesOut(t *testing.T) {
	orig := waitTimeout
	waitTimeout = 200 * time.Millisecond
	defer func() { waitTimeout = orig }()

	tempDir := t.TempDir()
	inputPath := filepath.Join(tempDir, "spec.md")
	writeMarkdown(t, inputPath, "# Spec\n\nContent.\n")

	url, stop := startTestServer(t, inputPath)
	defer stop()

	resp, err := http.Get(url + "/api/wait")
	if err != nil {
		t.Fatalf("GET /api/wait failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204 on wait timeout, got %d", resp.StatusCode)
	}
}

// A single submit must release every concurrently-waiting /api/wait client.
func TestStartReviewServer_WaitMultipleWaiters(t *testing.T) {
	tempDir := t.TempDir()
	inputPath := filepath.Join(tempDir, "spec.md")
	writeMarkdown(t, inputPath, "# Spec\n\nContent.\n")

	url, stop := startTestServer(t, inputPath)
	defer stop()

	const n = 3
	codes := make(chan int, n)
	for range n {
		go func() {
			resp, err := http.Get(url + "/api/wait")
			if err != nil {
				codes <- -1
				return
			}
			defer resp.Body.Close()
			_, _ = io.ReadAll(resp.Body)
			codes <- resp.StatusCode
		}()
	}

	// Let all waiters subscribe before the single submit.
	time.Sleep(200 * time.Millisecond)
	fb := Feedback{Comments: []Comment{{Text: "shared", Timestamp: time.Now().Format(time.RFC3339), Author: AuthorHuman, Status: StatusOpen}}}
	postFeedback(t, url, fb)

	for i := range n {
		select {
		case code := <-codes:
			if code != http.StatusOK {
				t.Errorf("waiter %d: expected 200, got %d", i, code)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("only %d of %d waiters were released by one submit", i, n)
		}
	}
}

// Ending the session (ctx cancel / close) must release a blocked /api/wait instead of hanging.
func TestStartReviewServer_WaitReleasedOnClose(t *testing.T) {
	tempDir := t.TempDir()
	inputPath := filepath.Join(tempDir, "spec.md")
	writeMarkdown(t, inputPath, "# Spec\n\nContent.\n")

	url, stop := startTestServer(t, inputPath)

	done := make(chan int, 1)
	go func() {
		resp, err := http.Get(url + "/api/wait")
		if err != nil {
			done <- -1
			return
		}
		defer resp.Body.Close()
		_, _ = io.ReadAll(resp.Body)
		done <- resp.StatusCode
	}()

	// Ensure the waiter is blocked, then shut the server down.
	time.Sleep(200 * time.Millisecond)
	stop() // cancels ctx -> triggers the server's done signal

	select {
	case <-done:
		// Released (any status is fine — the point is it did not hang).
	case <-time.After(3 * time.Second):
		t.Error("/api/wait did not unblock when the server shut down")
	}
}

func TestFeedbackPath(t *testing.T) {
	got := FeedbackPath(filepath.Join("docs", "my-spec.md"))

	// Sidecars live under the OS temp directory, not beside the document: reviewing a file
	// inside a repository must not leave untracked files in its working tree.
	if dir := filepath.Dir(got); dir != filepath.Join(os.TempDir(), "reviewer") {
		t.Errorf("FeedbackPath dir = %q, want it under the OS temp directory", dir)
	}
	base := filepath.Base(got)
	if !strings.HasPrefix(base, "my-spec-") || !strings.HasSuffix(base, "-feedback.json") {
		t.Errorf("FeedbackPath base = %q, want the document stem kept for legibility", base)
	}
}

func TestSidecarPath_DistinguishesSameNameInDifferentDirectories(t *testing.T) {
	// Basenames collide across directories, so the absolute path is hashed into the name.
	a := FeedbackPath(filepath.Join("docs", "spec.md"))
	b := FeedbackPath(filepath.Join("notes", "spec.md"))
	if a == b {
		t.Errorf("two documents named spec.md share the sidecar %q", a)
	}
}

func TestSidecarPath_IsStableForTheSameDocument(t *testing.T) {
	// A page reload, or a fresh process reviewing the same file, must find the same sidecar.
	if a, b := FeedbackPath("docs/spec.md"), FeedbackPath("docs/spec.md"); a != b {
		t.Errorf("FeedbackPath is not stable: %q then %q", a, b)
	}
}

func TestStatusPath(t *testing.T) {
	got := StatusPath(filepath.Join("docs", "my-spec.md"))

	if dir := filepath.Dir(got); dir != filepath.Join(os.TempDir(), "reviewer") {
		t.Errorf("StatusPath dir = %q, want it under the OS temp directory", dir)
	}
	base := filepath.Base(got)
	if !strings.HasPrefix(base, "my-spec-") || !strings.HasSuffix(base, "-status.json") {
		t.Errorf("StatusPath base = %q, want the document stem kept for legibility", base)
	}
}

func TestSidecars_AreNotWrittenBesideTheDocument(t *testing.T) {
	ctx := t.Context()

	tempDir := t.TempDir()
	inputPath := filepath.Join(tempDir, "spec.md")
	writeMarkdown(t, inputPath, "# Spec\n\nContent.\n")

	s, err := StartSession(ctx, inputPath, 0, true)
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	defer s.Close()

	postFeedback(t, s.URL(), Feedback{Comments: []Comment{{
		Text: "a note", Timestamp: "2026-01-01T00:00:00Z", Author: AuthorHuman, Status: StatusOpen,
	}}})
	if err := s.Progress(StateWorking, "editing"); err != nil {
		t.Fatalf("Progress failed: %v", err)
	}

	entries, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatalf("failed to read the document directory: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "spec.md" {
			t.Errorf("review left %q beside the document; sidecars belong in the temp directory", e.Name())
		}
	}
}

// The agent's status file drives a live "status" SSE event and a /api/status endpoint,
// so the page can show progress between submit and reply without a full reload.
func TestStartReviewServer_AgentStatus(t *testing.T) {
	ctx := t.Context()

	tempDir := t.TempDir()
	inputPath := filepath.Join(tempDir, "spec.md")
	writeMarkdown(t, inputPath, "# Spec\n\nContent.\n")

	// Driven through the session rather than by writing the sidecar directly: the agent no
	// longer touches these files, so the session is their only writer and broadcasts the
	// event itself instead of watching for its own write to come back.
	s, err := StartSession(ctx, inputPath, 0, true)
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	defer s.Close()
	url := s.URL()

	// Missing status file → idle.
	if body := httpGetBody(t, url+"/api/status"); !strings.Contains(body, `"state":"idle"`) {
		t.Errorf("expected idle status by default, got: %s", body)
	}

	reqCtx, reqCancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer reqCancel()
	req, _ := http.NewRequestWithContext(reqCtx, http.MethodGet, url+"/api/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to connect to SSE: %v", err)
	}
	defer resp.Body.Close()

	gotStatus := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data:") && strings.Contains(line, `"kind":"status"`) && strings.Contains(line, "editing-doc") {
				gotStatus <- true
				return
			}
		}
	}()

	time.Sleep(150 * time.Millisecond)
	// Agent reports its activity.
	if err := s.Progress(StateWorking, "editing-doc"); err != nil {
		t.Fatalf("Progress failed: %v", err)
	}

	select {
	case <-gotStatus:
	case <-time.After(3 * time.Second):
		t.Error("did not receive SSE status event after the agent wrote its status")
	}

	// The endpoint reflects the working status too.
	if body := httpGetBody(t, url+"/api/status"); !strings.Contains(body, "editing-doc") {
		t.Errorf("expected /api/status to report the working message, got: %s", body)
	}
}

// --- helpers ---

func httpGetBody(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s failed: %v", url, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body failed: %v", err)
	}
	return string(b)
}

func postFeedback(t *testing.T, url string, fb Feedback) {
	t.Helper()
	payload, err := json.Marshal(fb)
	if err != nil {
		t.Fatalf("marshal feedback: %v", err)
	}
	resp, err := http.Post(url+"/api/feedback", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("POST feedback: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST feedback returned %d", resp.StatusCode)
	}
}

// The sidecar round-trips Comment through encoding/json, so a field added for diffs must not
// change what a Markdown comment serialises to — old sidecars are read back after an upgrade.
func TestCommentJSONRoundTrip(t *testing.T) {
	t.Run("markdown comment carries no anchorLines", func(t *testing.T) {
		encoded, err := json.Marshal(Comment{
			ID: "abc", Text: "tighten this", Timestamp: "2026-08-15T00:00:00Z",
			Anchor: "spec-element-3", Context: "The system MUST…", Author: AuthorHuman, Status: StatusOpen,
		})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "anchorLines") {
			t.Errorf("anchorLines leaked into a Markdown comment: %s", encoded)
		}
	})

	t.Run("diff comment keeps its lines", func(t *testing.T) {
		want := Comment{
			ID: "def", Text: "rename this", Timestamp: "2026-08-15T00:00:00Z",
			Anchor:      FormatDiffAnchor("render.go", 3, 4),
			AnchorLines: []string{"\tfoo()", "}"},
			Author:      AuthorHuman, Status: StatusOpen,
		}
		encoded, err := json.Marshal(want)
		if err != nil {
			t.Fatal(err)
		}
		var got Comment
		if err := json.Unmarshal(encoded, &got); err != nil {
			t.Fatal(err)
		}
		if got.Anchor != want.Anchor {
			t.Errorf("anchor = %q, want %q", got.Anchor, want.Anchor)
		}
		if len(got.AnchorLines) != 2 || got.AnchorLines[0] != "\tfoo()" || got.AnchorLines[1] != "}" {
			t.Errorf("anchorLines = %#v, want %#v", got.AnchorLines, want.AnchorLines)
		}
	})

	t.Run("a sidecar written before diff support still parses", func(t *testing.T) {
		var fb Feedback
		if err := json.Unmarshal([]byte(`{"comments":[{"id":"old","text":"hi","timestamp":"t","anchor":"spec-element-1"}]}`), &fb); err != nil {
			t.Fatalf("legacy sidecar failed to parse: %v", err)
		}
		if len(fb.Comments) != 1 || fb.Comments[0].AnchorLines != nil {
			t.Errorf("legacy comment = %#v", fb.Comments)
		}
	})

	t.Run("a comment with no agent activity serialises as it did before threading", func(t *testing.T) {
		encoded, err := json.Marshal(Comment{
			ID: "abc", Text: "tighten this", Timestamp: "2026-08-15T00:00:00Z",
			Anchor: "spec-element-3", Context: "The system MUST…", Author: AuthorHuman, Status: StatusOpen,
		})
		if err != nil {
			t.Fatal(err)
		}
		want := `{"id":"abc","text":"tighten this","timestamp":"2026-08-15T00:00:00Z","anchor":"spec-element-3","context":"The system MUST…","author":"human","status":"open"}`
		if string(encoded) != want {
			t.Errorf("serialisation changed\n got: %s\nwant: %s", encoded, want)
		}
	})

	t.Run("a message drops its flags when they are false", func(t *testing.T) {
		encoded, err := json.Marshal(Message{Author: AuthorAgent, Text: "fixed", Timestamp: "t"})
		if err != nil {
			t.Fatal(err)
		}
		want := `{"author":"agent","text":"fixed","timestamp":"t"}`
		if string(encoded) != want {
			t.Errorf("message serialisation = %s, want %s", encoded, want)
		}

		var got Message
		if err := json.Unmarshal([]byte(`{"author":"agent","text":"which one?","timestamp":"t","needsAnswer":true,"declined":true}`), &got); err != nil {
			t.Fatal(err)
		}
		if !got.NeedsAnswer || !got.Declined {
			t.Errorf("message round trip = %#v", got)
		}
	})
}

// An older reviewer wrote one reply per comment. Reading such a sidecar has to yield the same
// exchange in threaded form, because the reply is the only record of what the agent said.
func TestMigrateFeedback_FoldsTheOldReplyIntoTheThread(t *testing.T) {
	var fb Feedback
	raw := `{"comments":[
		{"id":"one","text":"tighten this","timestamp":"t1","reply":"Rewrote the paragraph.","replyTimestamp":"t2"},
		{"id":"two","text":"no reply yet","timestamp":"t3"}
	]}`
	if err := json.Unmarshal([]byte(raw), &fb); err != nil {
		t.Fatal(err)
	}

	got := migrateFeedback(fb)

	first := got.Comments[0]
	if len(first.Messages) != 1 {
		t.Fatalf("expected the reply to become one message, got %#v", first.Messages)
	}
	if first.Messages[0].Author != AuthorAgent || first.Messages[0].Text != "Rewrote the paragraph." || first.Messages[0].Timestamp != "t2" {
		t.Errorf("migrated message = %#v", first.Messages[0])
	}
	if first.Reply != "" || first.ReplyTimestamp != "" {
		t.Errorf("the deprecated fields survived migration: %#v", first)
	}
	if len(got.Comments[1].Messages) != 0 {
		t.Errorf("a comment with no reply gained messages: %#v", got.Comments[1])
	}

	encoded, err := json.Marshal(got.Comments[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"reply"`) {
		t.Errorf("a migrated comment still writes reply: %s", encoded)
	}
}

// PendingQuestion is the rule the page and the agent both read: it decides whether the human is
// still being asked something. The table is the specification.
func TestCommentPendingQuestion(t *testing.T) {
	human := func(text string) Message { return Message{Author: AuthorHuman, Text: text} }
	agent := func(text string) Message { return Message{Author: AuthorAgent, Text: text} }
	asking := func(text string) Message {
		return Message{Author: AuthorAgent, Text: text, NeedsAnswer: true}
	}

	tests := []struct {
		name    string
		comment Comment
		want    bool
	}{
		{"an empty thread asks nothing", Comment{}, false},
		{"a human comment alone", Comment{Author: AuthorHuman, Text: "a"}, false},
		{"an ordinary agent report is not a question", Comment{
			Author: AuthorHuman, Text: "a", Messages: []Message{agent("b")},
		}, false},
		{"a flagged agent question", Comment{
			Author: AuthorHuman, Text: "a", Messages: []Message{asking("b")},
		}, true},
		{"the human answered", Comment{
			Author: AuthorHuman, Text: "a", Messages: []Message{asking("b"), human("c")},
		}, false},
		{"a second question after the answer", Comment{
			Author: AuthorHuman, Text: "a", Messages: []Message{asking("b"), human("c"), asking("d")},
		}, true},
		{"one human message answers every question before it", Comment{
			Author: AuthorHuman, Text: "a", Messages: []Message{asking("b"), asking("c"), human("d")},
		}, false},
		{"a declined question is settled", Comment{
			Author: AuthorHuman, Text: "a",
			Messages: []Message{{Author: AuthorAgent, Text: "b", NeedsAnswer: true, Declined: true}},
		}, false},
		{"an agent-opened thread asks from its head", Comment{
			Author: AuthorAgent, Text: "a", NeedsAnswer: true,
		}, true},
		{"an agent-opened thread the human answered", Comment{
			Author: AuthorAgent, Text: "a", NeedsAnswer: true, Messages: []Message{human("b")},
		}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.comment.PendingQuestion(); got != tt.want {
				t.Errorf("PendingQuestion() = %v, want %v", got, tt.want)
			}
		})
	}
}

// thread() is what lets every rule be written against a flat sequence: the head first, then the
// messages, with the head's author defaulted so a comment written before authors existed reads
// as the human's.
func TestCommentThread_PutsTheHeadFirst(t *testing.T) {
	c := Comment{Text: "head", Timestamp: "t1", Messages: []Message{
		{Author: AuthorAgent, Text: "reply", Timestamp: "t2"},
	}}

	got := c.thread()

	if len(got) != 2 {
		t.Fatalf("thread() = %#v, want head + 1 message", got)
	}
	if got[0].Author != AuthorHuman || got[0].Text != "head" || got[0].Timestamp != "t1" {
		t.Errorf("head = %#v", got[0])
	}
	if got[1].Text != "reply" {
		t.Errorf("tail = %#v", got[1])
	}
}

func readJSONFile(t *testing.T, path string, v any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
}
