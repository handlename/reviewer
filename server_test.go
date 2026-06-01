package reviewer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStartReviewServer_Feedback(t *testing.T) {
	tempDir := t.TempDir()
	inputPath := filepath.Join(tempDir, "spec.md")
	htmlContent := []byte("<html><body>Hello Spec</body></html>")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Get a free port
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to get free port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	errChan := make(chan error, 1)
	go func() {
		errChan <- StartReviewServer(ctx, htmlContent, inputPath, port, true)
	}()

	// Wait for server to spin up
	time.Sleep(200 * time.Millisecond)

	url := fmt.Sprintf("http://127.0.0.1:%d", port)

	// Test GET /
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("failed to GET root: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
	if !bytes.Equal(body, htmlContent) {
		t.Errorf("expected HTML content %q, got %q", string(htmlContent), string(body))
	}

	// Test POST /api/feedback
	comments := []map[string]interface{}{
		{
			"comment": "Nice spec",
			"line":    10,
		},
	}
	commentBytes, err := json.Marshal(comments)
	if err != nil {
		t.Fatalf("failed to marshal comments: %v", err)
	}

	postResp, err := http.Post(url+"/api/feedback", "application/json", bytes.NewReader(commentBytes))
	if err != nil {
		t.Fatalf("failed to POST feedback: %v", err)
	}
	defer postResp.Body.Close()

	if postResp.StatusCode != http.StatusOK {
		t.Errorf("expected POST feedback to return 200 OK, got %d", postResp.StatusCode)
	}

	// Wait for server to shutdown gracefully (since POST /api/feedback triggers shutdown)
	select {
	case err := <-errChan:
		if err != nil {
			t.Errorf("server shutdown returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Error("timed out waiting for server to shut down after feedback")
	}

	// Verify feedback file was written
	feedbackPath := filepath.Join(tempDir, "spec-feedback.json")
	if _, err := os.Stat(feedbackPath); os.IsNotExist(err) {
		t.Fatalf("expected feedback file to exist at %s", feedbackPath)
	}

	feedbackBytes, err := os.ReadFile(feedbackPath)
	if err != nil {
		t.Fatalf("failed to read feedback file: %v", err)
	}

	var loadedComments []map[string]interface{}
	if err := json.Unmarshal(feedbackBytes, &loadedComments); err != nil {
		t.Fatalf("failed to unmarshal written feedback: %v", err)
	}

	if len(loadedComments) != 1 || loadedComments[0]["comment"] != "Nice spec" {
		t.Errorf("unexpected written feedback comments: %v", loadedComments)
	}
}

func TestStartReviewServer_ContextCancel(t *testing.T) {
	tempDir := t.TempDir()
	inputPath := filepath.Join(tempDir, "spec.md")
	htmlContent := []byte("<html><body>Hello Spec</body></html>")

	ctx, cancel := context.WithCancel(context.Background())

	// Get a free port
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to get free port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	errChan := make(chan error, 1)
	go func() {
		errChan <- StartReviewServer(ctx, htmlContent, inputPath, port, true)
	}()

	// Wait for server to spin up
	time.Sleep(200 * time.Millisecond)

	// Cancel context to trigger shutdown
	cancel()

	select {
	case err := <-errChan:
		if err != nil {
			t.Errorf("server shutdown returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Error("timed out waiting for server to shut down after context cancellation")
	}
}
