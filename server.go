package reviewer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/rs/zerolog/log"
)

// Comment authorship and lifecycle values.
const (
	AuthorHuman    = "human"
	AuthorAgent    = "agent"
	StatusOpen     = "open"
	StatusResolved = "resolved"
)

// Comment defines the unified structure for feedback comments.
//
// A comment is authored by a human against a document block. The agent does not
// create top-level comments; instead it attaches a Reply to the human comment and
// leaves resolution to the human (Status is only ever set to resolved by the user).
type Comment struct {
	ID             string `json:"id,omitempty"` // server-assigned, stable across rounds; how the agent addresses a comment
	Text           string `json:"text"`
	Timestamp      string `json:"timestamp"`
	Anchor         string `json:"anchor,omitempty"`         // Element selector ID/anchor
	Context        string `json:"context,omitempty"`        // Preview text context of the commented element
	Author         string `json:"author,omitempty"`         // human | agent
	Status         string `json:"status,omitempty"`         // open | resolved (resolved set by the human)
	Reply          string `json:"reply,omitempty"`          // agent's reply describing how the comment was addressed
	ReplyTimestamp string `json:"replyTimestamp,omitempty"` // when the agent replied
}

// Feedback is the shared review state persisted to <input>-feedback.json.
// Both the browser and the agent read/write this document.
type Feedback struct {
	Comments []Comment `json:"comments"`
	Summary  string    `json:"summary,omitempty"` // agent's page-level change summary for the latest pass
}

// newCommentID mints the stable identifier the agent uses to address a comment.
// A package var so tests can make IDs deterministic.
var newCommentID = func() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Falling back to a timestamp keeps IDs unique enough for a single review session.
		return fmt.Sprintf("c%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// assignCommentIDs fills in IDs for comments the page created client-side. Existing IDs are
// preserved so a comment keeps the same identity across review rounds.
func assignCommentIDs(comments []Comment) []Comment {
	for i := range comments {
		if comments[i].ID == "" {
			comments[i].ID = newCommentID()
		}
	}
	return comments
}

// FeedbackPath returns the sidecar feedback file path for a given input document.
func FeedbackPath(inputPath string) string {
	return sidecarPath(inputPath, "-feedback.json")
}

// StatusPath returns the sidecar file the agent writes to report its live activity.
func StatusPath(inputPath string) string {
	return sidecarPath(inputPath, "-status.json")
}

func sidecarPath(inputPath, suffix string) string {
	dir := filepath.Dir(inputPath)
	base := filepath.Base(inputPath)
	ext := filepath.Ext(base)
	return filepath.Join(dir, strings.TrimSuffix(base, ext)+suffix)
}

// AgentStatus is the agent's live activity, surfaced on the page so the user can watch
// progress between submitting and the reply landing — without leaving the review page.
type AgentStatus struct {
	State     string `json:"state"`   // working | idle
	Message   string `json:"message"` // human-readable activity, e.g. "文書を更新しています…"
	Timestamp string `json:"timestamp,omitempty"`
}

// SSE payload builders. Events are JSON so the page can switch on "kind".
func reloadPayload() string { return `{"kind":"reload"}` }

func statusPayload(raw []byte) string {
	var s AgentStatus
	_ = json.Unmarshal(raw, &s)
	if s.State == "" {
		s.State = "idle"
	}
	out, err := json.Marshal(map[string]string{
		"kind": "status", "state": s.State, "message": s.Message, "timestamp": s.Timestamp,
	})
	if err != nil {
		return `{"kind":"status","state":"idle","message":""}`
	}
	return string(out)
}

// waitTimeout bounds how long GET /api/wait blocks before replying 204 so the client can
// re-poll. Kept below the agent monitor's curl --max-time so the server, not the client,
// closes the idle connection. A package var so tests can shorten it.
var waitTimeout = 25 * time.Second

// submitNotifier wakes every long-poll waiter the instant a review is submitted. It mirrors
// sseHub's fan-out, but carries no payload — a signal that "a submit happened"; the waiter
// then reads the current feedback itself.
type submitNotifier struct {
	mu      sync.Mutex
	waiters map[chan struct{}]struct{}
}

func newSubmitNotifier() *submitNotifier {
	return &submitNotifier{waiters: make(map[chan struct{}]struct{})}
}

func (n *submitNotifier) subscribe() chan struct{} {
	ch := make(chan struct{}, 1)
	n.mu.Lock()
	n.waiters[ch] = struct{}{}
	n.mu.Unlock()
	return ch
}

func (n *submitNotifier) unsubscribe(ch chan struct{}) {
	n.mu.Lock()
	delete(n.waiters, ch)
	n.mu.Unlock()
}

func (n *submitNotifier) broadcast() {
	n.mu.Lock()
	defer n.mu.Unlock()
	for ch := range n.waiters {
		// Non-blocking: the buffered channel already holds a pending signal, so the waiter
		// wakes regardless; a full buffer means it hasn't consumed the previous one yet.
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// sseHub fans a single reload signal out to every connected browser tab.
type sseHub struct {
	mu      sync.Mutex
	clients map[chan string]struct{}
}

func newSSEHub() *sseHub {
	return &sseHub{clients: make(map[chan string]struct{})}
}

func (h *sseHub) add() chan string {
	ch := make(chan string, 4)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *sseHub) remove(ch chan string) {
	h.mu.Lock()
	if _, ok := h.clients[ch]; ok {
		delete(h.clients, ch)
		close(ch)
	}
	h.mu.Unlock()
}

func (h *sseHub) broadcast(msg string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.clients {
		// Non-blocking: a slow client simply misses this tick and catches up on the next reload.
		select {
		case ch <- msg:
		default:
		}
	}
}

// StartReviewServer serves the live review page for inputPath and blocks until the session
// ends — the user clicking "End Review" (POST /api/close) or ctx being cancelled. It is the
// entry point for `reviewer serve`; the MCP server drives a ReviewSession directly instead.
//
// readyChan, when non-nil, receives the running server's URL once the port is bound.
func StartReviewServer(ctx context.Context, inputPath string, port int, noOpen bool, readyChan chan<- string) error {
	s, err := StartSession(ctx, inputPath, port, noOpen)
	if err != nil {
		return err
	}
	if readyChan != nil {
		readyChan <- s.URL()
	}
	<-s.Done()
	return s.Close()
}

// readFeedback returns the feedback document as JSON bytes, always in the {comments,summary}
// shape. A missing file yields an empty document rather than an error.
func readFeedback(feedbackPath string) []byte {
	empty := []byte(`{"comments":[]}`)
	raw, err := os.ReadFile(feedbackPath)
	if err != nil {
		return empty
	}
	var fb Feedback
	if err := json.Unmarshal(raw, &fb); err == nil {
		if fb.Comments == nil {
			fb.Comments = []Comment{}
		}
		if out, err := json.Marshal(fb); err == nil {
			return out
		}
	}
	return raw
}

// pruneResolved drops comments the human has marked resolved, keeping the ordering of the rest.
func pruneResolved(comments []Comment) []Comment {
	kept := make([]Comment, 0, len(comments))
	for _, c := range comments {
		if c.Status == StatusResolved {
			continue
		}
		kept = append(kept, c)
	}
	return kept
}

// watchForReload debounces filesystem events and broadcasts typed SSE events:
//   - the document or feedback sidecar changes -> "reload" (the page refreshes)
//   - the status sidecar changes             -> "status" (the page updates in place)
func watchForReload(watcher *fsnotify.Watcher, inputPath, feedbackPath, statusPath string, hub *sseHub, done <-chan struct{}) {
	wantInput := filepath.Clean(inputPath)
	wantFeedback := filepath.Clean(feedbackPath)
	wantStatus := filepath.Clean(statusPath)

	var reloadTimer, statusTimer *time.Timer
	debounceReload := func() {
		if reloadTimer != nil {
			reloadTimer.Stop()
		}
		reloadTimer = time.AfterFunc(150*time.Millisecond, func() { hub.broadcast(reloadPayload()) })
	}
	// Status is snappier than reload so progress feels live.
	debounceStatus := func() {
		if statusTimer != nil {
			statusTimer.Stop()
		}
		statusTimer = time.AfterFunc(60*time.Millisecond, func() {
			if raw, err := os.ReadFile(statusPath); err == nil {
				hub.broadcast(statusPayload(raw))
			}
		})
	}

	for {
		select {
		case <-done:
			return
		case ev, ok := <-watcher.Events:
			if !ok {
				return
			}
			switch filepath.Clean(ev.Name) {
			case wantStatus:
				debounceStatus()
			case wantInput, wantFeedback:
				debounceReload()
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Warn().Err(err).Msg("file watcher error")
		}
	}
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default: // linux, etc.
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Run(); err != nil {
		log.Warn().Err(err).Msg("Could not automatically open browser")
	}
}
