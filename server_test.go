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

// Comments the user marked resolved are pruned on the next submit; open ones carry forward.
func TestStartReviewServer_PrunesResolvedOnSubmit(t *testing.T) {
	tempDir := t.TempDir()
	inputPath := filepath.Join(tempDir, "spec.md")
	writeMarkdown(t, inputPath, "# Spec\n\nContent.\n")

	url, stop := startTestServer(t, inputPath)
	defer stop()

	now := time.Now().Format(time.RFC3339)
	fb := Feedback{
		Summary: "Addressed one item.",
		Comments: []Comment{
			{Text: "still open", Timestamp: now, Status: StatusOpen},
			{Text: "done, resolved", Timestamp: now, Status: StatusResolved, Reply: "Fixed", Author: AuthorHuman},
		},
	}
	postFeedback(t, url, fb)

	feedbackPath := FeedbackPath(inputPath)
	var written Feedback
	readJSONFile(t, feedbackPath, &written)
	if len(written.Comments) != 1 {
		t.Fatalf("expected resolved comment pruned, got %d comments: %+v", len(written.Comments), written.Comments)
	}
	if written.Comments[0].Text != "still open" {
		t.Errorf("expected the open comment to remain, got %q", written.Comments[0].Text)
	}
}

// The "End Review" button (POST /api/close) shuts the server down gracefully.
func TestStartReviewServer_CloseEndsSession(t *testing.T) {
	tempDir := t.TempDir()
	inputPath := filepath.Join(tempDir, "spec.md")
	writeMarkdown(t, inputPath, "# Spec\n\nContent.\n")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
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

func TestFeedbackPath(t *testing.T) {
	got := FeedbackPath(filepath.Join("docs", "my-spec.md"))
	want := filepath.Join("docs", "my-spec-feedback.json")
	if got != want {
		t.Errorf("FeedbackPath = %q, want %q", got, want)
	}
}

func TestStatusPath(t *testing.T) {
	got := StatusPath(filepath.Join("docs", "my-spec.md"))
	want := filepath.Join("docs", "my-spec-status.json")
	if got != want {
		t.Errorf("StatusPath = %q, want %q", got, want)
	}
}

// The agent's status file drives a live "status" SSE event and a /api/status endpoint,
// so the page can show progress between submit and reply without a full reload.
func TestStartReviewServer_AgentStatus(t *testing.T) {
	tempDir := t.TempDir()
	inputPath := filepath.Join(tempDir, "spec.md")
	writeMarkdown(t, inputPath, "# Spec\n\nContent.\n")

	url, stop := startTestServer(t, inputPath)
	defer stop()

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
	if err := os.WriteFile(StatusPath(inputPath), []byte(`{"state":"working","message":"editing-doc"}`), 0644); err != nil {
		t.Fatalf("failed to write status: %v", err)
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
