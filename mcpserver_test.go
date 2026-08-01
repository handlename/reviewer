package reviewer

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestReviewStart_OpensSessionAndReturnsURL(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newSessionHolder(ctx)
	defer h.closeCurrent()

	out, err := h.start(startInput{Path: writeTempSpec(t)}, MCPOptions{NoOpen: true})
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if out.URL == "" {
		t.Fatal("start should return the review URL")
	}
}

func TestReviewStart_SessionOutlivesTheToolCall(t *testing.T) {
	base, cancelBase := context.WithCancel(context.Background())
	defer cancelBase()

	h := newSessionHolder(base)
	defer h.closeCurrent()

	if _, err := h.start(startInput{Path: writeTempSpec(t)}, MCPOptions{NoOpen: true}); err != nil {
		t.Fatalf("start failed: %v", err)
	}

	// A review lives for many tool calls. Binding its lifetime to the context of the
	// review_start call that created it would kill it the moment that call returns, leaving
	// the human looking at a page nobody is serving.
	callCtx, cancelCall := context.WithCancel(context.Background())
	cancelCall()
	<-callCtx.Done()
	time.Sleep(50 * time.Millisecond)

	if _, err := h.wait(context.Background(), 50*time.Millisecond); err != nil {
		t.Fatalf("session did not survive the tool call: %v", err)
	}
}

func TestReviewStart_RejectsMissingDocument(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newSessionHolder(ctx)
	defer h.closeCurrent()

	// Returning a URL for a document that cannot be read would have the agent invite the
	// human to a page that only ever renders an error.
	if _, err := h.start(startInput{Path: filepath.Join(t.TempDir(), "missing.md")}, MCPOptions{NoOpen: true}); err == nil {
		t.Fatal("start should reject a document that does not exist")
	}
}

func TestReviewStart_RejectsDirectory(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newSessionHolder(ctx)
	defer h.closeCurrent()

	if _, err := h.start(startInput{Path: t.TempDir()}, MCPOptions{NoOpen: true}); err == nil {
		t.Fatal("start should reject a directory")
	}
}

func TestReviewWait_TimeoutIsNotAnError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newSessionHolder(ctx)
	defer h.closeCurrent()

	if _, err := h.start(startInput{Path: writeTempSpec(t)}, MCPOptions{NoOpen: true}); err != nil {
		t.Fatalf("start failed: %v", err)
	}

	out, err := h.wait(ctx, 100*time.Millisecond)
	// An idle expiry must arrive as a successful result. If this returns an error, the SDK
	// marks the tool result IsError and the agent sees a normal wait as a broken call.
	if err != nil {
		t.Fatalf("wait returned an error on timeout: %v", err)
	}
	if out.Outcome != string(WaitTimeout) {
		t.Fatalf("got outcome %q, want %q", out.Outcome, WaitTimeout)
	}
}

func TestReviewWait_AfterSessionEndedReportsSessionEnded(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newSessionHolder(ctx)
	defer h.closeCurrent()

	if _, err := h.start(startInput{Path: writeTempSpec(t)}, MCPOptions{NoOpen: true}); err != nil {
		t.Fatalf("start failed: %v", err)
	}

	// The human can click End Review while the agent is editing. The agent then calls
	// review_wait and must learn the review is over through the documented outcome, not
	// through an error that reads like a malfunction.
	h.mu.Lock()
	s := h.current
	h.mu.Unlock()
	if err := s.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	out, err := h.wait(ctx, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("wait after End Review returned an error: %v", err)
	}
	if out.Outcome != string(WaitSessionEnded) {
		t.Fatalf("got outcome %q, want %q", out.Outcome, WaitSessionEnded)
	}
}

func TestReviewWait_WithoutSessionIsAnError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newSessionHolder(ctx)
	// Calling wait before start is an agent mistake, not an expected outcome, so it IS an error.
	if _, err := h.wait(ctx, 100*time.Millisecond); err == nil {
		t.Fatal("wait without an active session should be an error")
	}
}

func TestReviewStart_ReplacesEndedSession(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newSessionHolder(ctx)
	defer h.closeCurrent()

	if _, err := h.start(startInput{Path: writeTempSpec(t)}, MCPOptions{NoOpen: true}); err != nil {
		t.Fatalf("first start failed: %v", err)
	}
	h.closeCurrent()

	// Once the human ends a review, starting the next one must just work.
	if _, err := h.start(startInput{Path: writeTempSpec(t)}, MCPOptions{NoOpen: true}); err != nil {
		t.Fatalf("second start after close failed: %v", err)
	}
}

func TestReviewStart_RejectsSecondLiveSession(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newSessionHolder(ctx)
	defer h.closeCurrent()

	if _, err := h.start(startInput{Path: writeTempSpec(t)}, MCPOptions{NoOpen: true}); err != nil {
		t.Fatalf("first start failed: %v", err)
	}
	if _, err := h.start(startInput{Path: writeTempSpec(t)}, MCPOptions{NoOpen: true}); err == nil {
		t.Fatal("starting a second review while one is live should be an error")
	}
}
