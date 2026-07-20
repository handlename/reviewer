package reviewer

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
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
	State     string `json:"state"`             // working | idle
	Message   string `json:"message"`           // human-readable activity, e.g. "文書を更新しています…"
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

// StartReviewServer serves the live review page for inputPath and keeps running across
// review rounds. Submitting comments no longer shuts the server down; the page stays open,
// the agent edits the document and writes replies, and connected tabs auto-reload via SSE.
// The session ends when the user clicks "End Review" (POST /api/close) or ctx is cancelled.
//
// readyChan, when non-nil, receives the running server's URL once the port is bound.
func StartReviewServer(ctx context.Context, inputPath string, port int, noOpen bool, readyChan chan<- string) error {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Warn().Err(err).Msgf("port %d busy, probing for auto-assigned port", port)
		listener, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return fmt.Errorf("failed to bind any local port: %w", err)
		}
	}
	defer listener.Close()

	actualPort := listener.Addr().(*net.TCPAddr).Port
	url := fmt.Sprintf("http://127.0.0.1:%d", actualPort)
	log.Info().Msgf("Review server running at %s", url)

	if readyChan != nil {
		readyChan <- url
	}

	feedbackPath := FeedbackPath(inputPath)
	statusPath := StatusPath(inputPath)
	hub := newSSEHub()
	notifier := newSubmitNotifier()

	// done is the single shutdown signal: closed by /api/close or by ctx cancellation.
	done := make(chan struct{})
	var closeOnce sync.Once
	triggerClose := func() { closeOnce.Do(func() { close(done) }) }
	go func() {
		select {
		case <-ctx.Done():
			triggerClose()
		case <-done:
		}
	}()

	// Watch the document and its feedback sidecar; on change, tell every tab to reload.
	// Watching the parent directory (not the files directly) survives editors' atomic saves.
	if watcher, werr := fsnotify.NewWatcher(); werr != nil {
		log.Warn().Err(werr).Msg("file watching unavailable; auto-reload disabled")
	} else {
		defer watcher.Close()
		if aerr := watcher.Add(filepath.Dir(inputPath)); aerr != nil {
			log.Warn().Err(aerr).Msg("failed to watch document directory; auto-reload disabled")
		} else {
			go watchForReload(watcher, inputPath, feedbackPath, statusPath, hub, done)
		}
	}

	mux := http.NewServeMux()

	// GET / — re-render the current document on every request so the agent's edits appear on reload.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		mdContent, err := os.ReadFile(inputPath)
		if err != nil {
			http.Error(w, "Failed to read document: "+err.Error(), http.StatusInternalServerError)
			return
		}
		htmlContent, err := RenderSpec(mdContent)
		if err != nil {
			http.Error(w, "Failed to render document: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(htmlContent)
	})

	mux.HandleFunc("/api/feedback", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(readFeedback(feedbackPath))

		case http.MethodPost:
			var fb Feedback
			if err := json.NewDecoder(r.Body).Decode(&fb); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			// Prune comments the user marked resolved in the previous cycle; only open ones carry forward.
			fb.Comments = pruneResolved(fb.Comments)

			encoded, err := json.MarshalIndent(fb, "", "  ")
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if err := os.WriteFile(feedbackPath, encoded, 0644); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			// Seed the activity panel with a "waiting for the agent" status. This survives the
			// reload the feedback write triggers, so the indicator stays put instead of flashing
			// away; the agent then overwrites it with its own progress and clears it when done.
			_ = os.WriteFile(statusPath, []byte(`{"state":"working","message":"エージェントの応答を待っています…"}`), 0644)

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))

			// Wake any long-poll waiter (GET /api/wait) — the low-latency submit signal.
			notifier.broadcast()

			log.Info().Msg("Feedback received & written; server staying alive for the agent.")
			// stdout signal the agent watches for (kept for back-compat/debugging; server does NOT shut down).
			fmt.Println("FEEDBACK_RECEIVED")

		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// POST /api/close — the page's "End Review" button ends the session gracefully.
	mux.HandleFunc("/api/close", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"closing"}`))
		log.Info().Msg("End Review requested. Shutting down server...")
		triggerClose()
	})

	// GET /api/status — the agent's current activity, for restoring state on (re)load.
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		raw, err := os.ReadFile(statusPath)
		if err != nil {
			_, _ = w.Write([]byte(`{"state":"idle","message":""}`))
			return
		}
		_, _ = w.Write(raw)
	})

	// GET /api/wait — long-poll: block until the next submit, then return 200 + the current
	// feedback JSON. Idle timeout replies 204 so the agent's monitor re-polls; the session ending
	// (close / ctx cancel) or the client disconnecting releases the waiter too.
	mux.HandleFunc("/api/wait", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		ch := notifier.subscribe()
		defer notifier.unsubscribe(ch)

		select {
		case <-ch:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(readFeedback(feedbackPath))
		case <-time.After(waitTimeout):
			w.WriteHeader(http.StatusNoContent)
		case <-done:
			w.WriteHeader(http.StatusNoContent)
		case <-r.Context().Done():
			// Client hung up; nothing to write.
		}
	})

	// GET /api/events — SSE stream that pushes typed events (reload / status) as files change.
	mux.HandleFunc("/api/events", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		ch := hub.add()
		defer hub.remove(ch)

		fmt.Fprint(w, ": connected\n\n")
		flusher.Flush()

		for {
			select {
			case <-done:
				return
			case <-r.Context().Done():
				return
			case msg, ok := <-ch:
				if !ok {
					return
				}
				fmt.Fprintf(w, "data: %s\n\n", msg)
				flusher.Flush()
			}
		}
	})

	server := &http.Server{Handler: mux}

	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Error().Err(err).Msg("Server error")
		}
	}()

	if !noOpen {
		go func() {
			time.Sleep(50 * time.Millisecond)
			openBrowser(url)
		}()
	}

	<-done
	log.Info().Msg("Shutting down review server...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	return server.Shutdown(shutdownCtx)
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
