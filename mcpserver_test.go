package reviewer

import (
	"context"
	"testing"
	"time"
)

func TestReviewStart_OpensSessionAndReturnsURL(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newSessionHolder()
	defer h.closeCurrent()

	out, err := h.start(ctx, startInput{Path: writeTempSpec(t)}, MCPOptions{NoOpen: true})
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if out.URL == "" {
		t.Fatal("start should return the review URL")
	}
}

func TestReviewWait_TimeoutIsNotAnError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newSessionHolder()
	defer h.closeCurrent()

	if _, err := h.start(ctx, startInput{Path: writeTempSpec(t)}, MCPOptions{NoOpen: true}); err != nil {
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

func TestReviewWait_WithoutSessionIsAnError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newSessionHolder()
	// Calling wait before start is an agent mistake, not an expected outcome, so it IS an error.
	if _, err := h.wait(ctx, 100*time.Millisecond); err == nil {
		t.Fatal("wait without an active session should be an error")
	}
}

func TestReviewStart_ReplacesEndedSession(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newSessionHolder()
	defer h.closeCurrent()

	if _, err := h.start(ctx, startInput{Path: writeTempSpec(t)}, MCPOptions{NoOpen: true}); err != nil {
		t.Fatalf("first start failed: %v", err)
	}
	h.closeCurrent()

	// Once the human ends a review, starting the next one must just work.
	if _, err := h.start(ctx, startInput{Path: writeTempSpec(t)}, MCPOptions{NoOpen: true}); err != nil {
		t.Fatalf("second start after close failed: %v", err)
	}
}

func TestReviewStart_RejectsSecondLiveSession(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newSessionHolder()
	defer h.closeCurrent()

	if _, err := h.start(ctx, startInput{Path: writeTempSpec(t)}, MCPOptions{NoOpen: true}); err != nil {
		t.Fatalf("first start failed: %v", err)
	}
	if _, err := h.start(ctx, startInput{Path: writeTempSpec(t)}, MCPOptions{NoOpen: true}); err == nil {
		t.Fatal("starting a second review while one is live should be an error")
	}
}
