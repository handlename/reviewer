package reviewer

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/rs/zerolog/log"
)

// ReviewSession is one live review of one document. It owns the HTTP listener, the SSE hub
// that pushes reloads to open tabs, the notifier that releases submit waiters, and the single
// shutdown signal. Both `reviewer serve` and the MCP server drive a session through this type.
type ReviewSession struct {
	inputPath    string
	feedbackPath string
	statusPath   string
	url          string

	hub      *sseHub
	notifier *submitNotifier

	// done signals "the session is over". shutdown is separate and happens exactly once,
	// whoever gets there first: /api/close closes done without shutting the server down, and
	// StartReviewServer then calls Close() to do the shutdown after it stops blocking.
	done         chan struct{}
	closeOnce    sync.Once
	shutdownOnce sync.Once
	shutdownErr  error

	server   *http.Server
	listener net.Listener
	watcher  *fsnotify.Watcher
}

// StartSession binds a port, begins serving, and returns as soon as the URL is known. The
// caller decides how to wait: `reviewer serve` blocks on Done(), the MCP server keeps the
// handle and drives it through Wait/Reply/Progress.
func StartSession(ctx context.Context, inputPath string, port int, noOpen bool) (*ReviewSession, error) {
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		log.Warn().Err(err).Msgf("port %d busy, probing for auto-assigned port", port)
		listener, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, fmt.Errorf("failed to bind any local port: %w", err)
		}
	}

	actualPort := listener.Addr().(*net.TCPAddr).Port
	s := &ReviewSession{
		inputPath:    inputPath,
		feedbackPath: FeedbackPath(inputPath),
		statusPath:   StatusPath(inputPath),
		url:          fmt.Sprintf("http://127.0.0.1:%d", actualPort),
		hub:          newSSEHub(),
		notifier:     newSubmitNotifier(),
		done:         make(chan struct{}),
		listener:     listener,
	}
	log.Info().Msgf("Review server running at %s", s.url)

	go func() {
		select {
		case <-ctx.Done():
			s.triggerClose()
		case <-s.done:
		}
	}()

	s.startWatcher()

	s.server = &http.Server{Handler: s.newMux()}
	go func() {
		if err := s.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Error().Err(err).Msg("Server error")
		}
	}()

	if !noOpen {
		go func() {
			time.Sleep(50 * time.Millisecond)
			openBrowser(s.url)
		}()
	}

	return s, nil
}

func (s *ReviewSession) URL() string           { return s.url }
func (s *ReviewSession) InputPath() string     { return s.inputPath }
func (s *ReviewSession) Done() <-chan struct{} { return s.done }

func (s *ReviewSession) triggerClose() { s.closeOnce.Do(func() { close(s.done) }) }

// Close ends the session and shuts the HTTP server down. It is safe to call more than once,
// and safe to call after the session has already ended on its own — which is the normal path:
// the "End Review" button closes done, and StartReviewServer then calls Close to release the
// listener. Guarding the shutdown with closeOnce instead of its own Once would skip it in
// exactly that case and leak the port.
func (s *ReviewSession) Close() error {
	s.triggerClose()
	s.shutdownOnce.Do(func() {
		log.Info().Msg("Shutting down review server...")
		if s.watcher != nil {
			_ = s.watcher.Close()
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		s.shutdownErr = s.server.Shutdown(shutdownCtx)
	})
	return s.shutdownErr
}

// startWatcher watches the document's directory (not the files directly) so editors' atomic
// saves are still seen. Failure degrades to no auto-reload rather than failing the session.
func (s *ReviewSession) startWatcher() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Warn().Err(err).Msg("file watching unavailable; auto-reload disabled")
		return
	}
	if err := watcher.Add(filepath.Dir(s.inputPath)); err != nil {
		log.Warn().Err(err).Msg("failed to watch document directory; auto-reload disabled")
		_ = watcher.Close()
		return
	}
	s.watcher = watcher
	go watchForReload(watcher, s.inputPath, s.feedbackPath, s.statusPath, s.hub, s.done)
}

// WaitOutcome discriminates why Wait returned. Every value is a normal, expected result:
// none of them is an error condition. This matters because the MCP layer turns a returned
// Go error into an error tool result, which would make an ordinary idle expiry look like a
// failure to the agent.
type WaitOutcome string

const (
	WaitSubmitted    WaitOutcome = "submitted"
	WaitTimeout      WaitOutcome = "timeout"
	WaitSessionEnded WaitOutcome = "session_ended"
)

// WaitResult is what a completed wait reports back to the agent.
type WaitResult struct {
	Outcome  WaitOutcome `json:"outcome"`
	Comments []Comment   `json:"comments"`
	Summary  string      `json:"summary,omitempty"`
}

// Wait blocks until the human submits, the timeout expires, or the session ends, whichever
// comes first. It never returns an error: the caller distinguishes cases via Outcome.
func (s *ReviewSession) Wait(ctx context.Context, timeout time.Duration) WaitResult {
	ch := s.notifier.subscribe()
	defer s.notifier.unsubscribe(ch)

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-ch:
		fb := s.readFeedbackDoc()
		return WaitResult{Outcome: WaitSubmitted, Comments: fb.Comments, Summary: fb.Summary}
	case <-timer.C:
		return WaitResult{Outcome: WaitTimeout, Comments: []Comment{}}
	case <-s.done:
		return WaitResult{Outcome: WaitSessionEnded, Comments: []Comment{}}
	case <-ctx.Done():
		return WaitResult{Outcome: WaitSessionEnded, Comments: []Comment{}}
	}
}

// readFeedbackDoc loads the feedback sidecar. A missing or malformed file yields an empty
// document rather than an error — the review can still proceed.
func (s *ReviewSession) readFeedbackDoc() Feedback {
	fb := Feedback{Comments: []Comment{}}
	raw, err := os.ReadFile(s.feedbackPath)
	if err != nil {
		return fb
	}
	if err := json.Unmarshal(raw, &fb); err != nil {
		log.Warn().Err(err).Msg("feedback file is malformed; treating as empty")
		return Feedback{Comments: []Comment{}}
	}
	if fb.Comments == nil {
		fb.Comments = []Comment{}
	}
	return fb
}

// Agent activity states surfaced on the review page.
const (
	StateWorking = "working"
	StateIdle    = "idle"
)

// ReplyInput is one agent response, addressed to a comment by the ID the server assigned it.
// JSON names are camelCase per AGENTS.md section 5.
type ReplyInput struct {
	CommentID string `json:"commentId" jsonschema:"the id of the comment being answered, from review_wait"`
	Reply     string `json:"reply" jsonschema:"what you changed and why"`
}

// Reply threads the agent's responses under the human's comments and records the round's
// summary. It writes only Reply and ReplyTimestamp: Status is deliberately untouched, because
// marking a comment resolved is the human's decision alone (see DESIGN.md section 4).
func (s *ReviewSession) Reply(replies []ReplyInput, summary string) error {
	fb := s.readFeedbackDoc()

	byID := make(map[string]int, len(fb.Comments))
	for i, c := range fb.Comments {
		byID[c.ID] = i
	}

	now := time.Now().UTC().Format(time.RFC3339)
	for _, r := range replies {
		i, ok := byID[r.CommentID]
		if !ok {
			return fmt.Errorf("no comment with id %q; call review_wait to get the current comments", r.CommentID)
		}
		fb.Comments[i].Reply = r.Reply
		fb.Comments[i].ReplyTimestamp = now
	}
	fb.Summary = summary

	encoded, err := json.MarshalIndent(fb, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode feedback: %w", err)
	}
	if err := os.WriteFile(s.feedbackPath, encoded, 0644); err != nil {
		return fmt.Errorf("failed to write feedback: %w", err)
	}
	return nil
}

// Progress reports the agent's current activity to the open page. The server's file watcher
// picks the write up and pushes a status event over SSE, so the panel updates without a reload.
func (s *ReviewSession) Progress(state, message string) error {
	if state != StateWorking && state != StateIdle {
		return fmt.Errorf("state must be %q or %q, got %q", StateWorking, StateIdle, state)
	}
	encoded, err := json.Marshal(AgentStatus{
		State:     state,
		Message:   message,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return fmt.Errorf("failed to encode status: %w", err)
	}
	if err := os.WriteFile(s.statusPath, encoded, 0644); err != nil {
		return fmt.Errorf("failed to write status: %w", err)
	}
	return nil
}

// newMux wires the HTTP surface: the rendered page for the browser, and the endpoints that
// `reviewer serve` clients (including the page itself) use. The MCP server does not go through
// HTTP — it drives the session directly.
func (s *ReviewSession) newMux() *http.ServeMux {
	mux := http.NewServeMux()

	// GET / — re-render the current document on every request so the agent's edits appear on reload.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		mdContent, err := os.ReadFile(s.inputPath)
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
			_, _ = w.Write(readFeedback(s.feedbackPath))

		case http.MethodPost:
			var fb Feedback
			if err := json.NewDecoder(r.Body).Decode(&fb); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			// Prune comments the user marked resolved in the previous cycle; only open ones carry forward.
			fb.Comments = pruneResolved(fb.Comments)
			// Give every comment a stable identity so the agent can address it by ID.
			fb.Comments = assignCommentIDs(fb.Comments)

			encoded, err := json.MarshalIndent(fb, "", "  ")
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if err := os.WriteFile(s.feedbackPath, encoded, 0644); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			// Seed the activity panel with a "waiting for the agent" status. This survives the
			// reload the feedback write triggers, so the indicator stays put instead of flashing
			// away; the agent then overwrites it with its own progress and clears it when done.
			_ = os.WriteFile(s.statusPath, []byte(`{"state":"working","message":"エージェントの応答を待っています…"}`), 0644)

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))

			// Wake any long-poll waiter (GET /api/wait) — the low-latency submit signal.
			s.notifier.broadcast()

			// NOTE: the previous fmt.Println("FEEDBACK_RECEIVED") is gone. Stdout is the MCP
			// JSON-RPC transport in `reviewer mcp`, so writing to it corrupts the protocol
			// stream. The agent's submit signal is review_wait (and GET /api/wait for `serve`).
			log.Info().Msg("Feedback received & written; server staying alive for the agent.")

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
		s.triggerClose()
	})

	// GET /api/status — the agent's current activity, for restoring state on (re)load.
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		raw, err := os.ReadFile(s.statusPath)
		if err != nil {
			_, _ = w.Write([]byte(`{"state":"idle","message":""}`))
			return
		}
		_, _ = w.Write(raw)
	})

	// GET /api/wait — long-poll: block until the next submit, then return 200 + the current
	// feedback JSON. Idle timeout replies 204 so the client re-polls; the session ending
	// (close / ctx cancel) or the client disconnecting releases the waiter too.
	mux.HandleFunc("/api/wait", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		ch := s.notifier.subscribe()
		defer s.notifier.unsubscribe(ch)

		select {
		case <-ch:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(readFeedback(s.feedbackPath))
		case <-time.After(waitTimeout):
			w.WriteHeader(http.StatusNoContent)
		case <-s.done:
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

		ch := s.hub.add()
		defer s.hub.remove(ch)

		fmt.Fprint(w, ": connected\n\n")
		flusher.Flush()

		for {
			select {
			case <-s.done:
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

	return mux
}
