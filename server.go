package reviewer

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
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

// Message is one turn in a comment's thread: an agent response, or a human answer to it.
//
// The thread's first message is the Comment itself (see Comment.thread), so a Message only ever
// describes what came after it.
type Message struct {
	Author    string `json:"author"` // human | agent
	Text      string `json:"text"`
	Timestamp string `json:"timestamp"`
	// NeedsAnswer marks an agent message as a question the human is expected to answer. It is
	// opt-in: an ordinary "fixed it" report carries no flag, so an agent that predates threading
	// never leaves the page warning about an unanswered question.
	NeedsAnswer bool `json:"needsAnswer,omitempty"`
	// Declined records that the human closed the thread without answering that question. It is
	// what tells the agent its question did not come back, rather than leaving it to infer.
	Declined bool `json:"declined,omitempty"`
}

// Comment defines the unified structure for feedback comments.
//
// A comment is a thread. Its head — Text, Timestamp, Author — is the first message, and Messages
// holds everything said after it. Keeping the head on the Comment itself is what leaves Anchor,
// AnchorLines, Outdated and Context attached to the thread as a whole, and it reads the same
// whether the thread was opened by the human or by the agent.
//
// Resolution stays the human's: Status is only ever set to resolved from the page.
type Comment struct {
	ID        string `json:"id,omitempty"` // server-assigned, stable across rounds; how the agent addresses a comment
	Text      string `json:"text"`
	Timestamp string `json:"timestamp"`
	// Anchor is the comment's target: a document block (spec-element-N) for Markdown, or a
	// line range (<path>#<start>-<end>) for a diff. See FormatDiffAnchor for the diff form.
	Anchor string `json:"anchor,omitempty"`
	// AnchorLines is the exact content of the anchored diff lines, markers stripped. It is what
	// lets a comment find its lines again after the agent regenerates the diff, so it has to be
	// persisted alongside the anchor rather than derived from it.
	AnchorLines []string `json:"anchorLines,omitempty"`
	// Outdated marks a diff comment whose lines are no longer in the diff. It keeps its anchor
	// so it can come back if the lines reappear; the page just stops drawing it in the body.
	Outdated bool   `json:"outdated,omitempty"`
	Context  string `json:"context,omitempty"` // Preview text context of the commented element
	Author   string `json:"author,omitempty"`  // human | agent
	Status   string `json:"status,omitempty"`  // open | resolved (resolved set by the human)
	// NeedsAnswer and Declined apply to the head message, for a thread the agent opened.
	NeedsAnswer bool `json:"needsAnswer,omitempty"`
	Declined    bool `json:"declined,omitempty"`
	// AnchorQuote is the passage an agent-opened thread was written against. It is re-resolved to
	// an Anchor on every render rather than trusted, so the thread survives the document moving.
	AnchorQuote string `json:"anchorQuote,omitempty"`
	// AnchorQuoteText is AnchorQuote as the browser will see it: Markdown syntax resolved to the
	// text a rendered block actually displays. The agent quotes the source, the page matches
	// against textContent, and the two only meet through this.
	//
	// It is derived on every read of the feedback rather than stored, so a sidecar written before
	// it existed gets one too, and the quote the agent wrote stays the single source of truth.
	AnchorQuoteText string `json:"anchorQuoteText,omitempty"`
	// Messages is the rest of the thread, in chronological order.
	Messages []Message `json:"messages,omitempty"`
	// Reply and ReplyTimestamp are the pre-threading shape of a single agent response.
	//
	// Deprecated: superseded by Messages. They are still read so a sidecar written by an older
	// reviewer migrates (see migrateFeedback); nothing writes them any more.
	Reply          string `json:"reply,omitempty"`
	ReplyTimestamp string `json:"replyTimestamp,omitempty"`
}

// thread returns the head followed by Messages, so every rule about questions and answers is
// written once against a flat sequence instead of special-casing the head.
func (c Comment) thread() []Message {
	head := Message{
		Author:      c.Author,
		Text:        c.Text,
		Timestamp:   c.Timestamp,
		NeedsAnswer: c.NeedsAnswer,
		Declined:    c.Declined,
	}
	if head.Author == "" {
		head.Author = AuthorHuman
	}
	return append([]Message{head}, c.Messages...)
}

// PendingQuestion reports whether the thread's last flagged question is still unanswered.
//
// A question counts as answered by any human message appended after it — there is no dedicated
// answer control, because a second posting path would buy nothing over the reply the human is
// already writing.
func (c Comment) PendingQuestion() bool {
	msgs := c.thread()
	last := -1
	for i, m := range msgs {
		if m.NeedsAnswer {
			last = i
		}
	}
	if last < 0 {
		return false
	}
	// A declined question is settled — refused, but settled. It is still reported to the agent
	// once, carrying declined, and it never counts as something the human owes an answer to.
	if msgs[last].Declined {
		return false
	}
	for _, m := range msgs[last+1:] {
		if m.Author == AuthorHuman {
			return false
		}
	}
	return true
}

// Feedback is the review state for one document, persisted under the OS temp directory (see
// FeedbackPath). It is reviewer's internal storage: the browser and the session read and write
// it, the agent reaches it only through the MCP tools.
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

// FeedbackPath returns the file holding a document's review comments.
func FeedbackPath(inputPath string) string {
	return sidecarPath(inputPath, "-feedback.json")
}

// StatusPath returns the file holding the agent's live activity for a document.
func StatusPath(inputPath string) string {
	return sidecarPath(inputPath, "-status.json")
}

// sidecarDir is where reviewer keeps its per-document review state.
//
// These files are reviewer's internal storage, not something the user edits, so they live under
// the OS temp directory. Writing them beside the document left untracked files in the working
// tree of any repository being reviewed.
func sidecarDir() string {
	return filepath.Join(os.TempDir(), "reviewer")
}

// sidecarPath derives a stable, collision-free name for one document's sidecar. The absolute
// path is hashed in because basenames collide across directories (docs/spec.md and
// notes/spec.md), while the readable stem is kept so the file is still identifiable by eye.
func sidecarPath(inputPath, suffix string) string {
	abs, err := filepath.Abs(inputPath)
	if err != nil {
		// A path we cannot absolutise still needs a stable name; the raw form is stable enough.
		abs = inputPath
	}
	sum := sha256.Sum256([]byte(abs))
	stem := strings.TrimSuffix(filepath.Base(abs), filepath.Ext(abs))
	return filepath.Join(sidecarDir(), fmt.Sprintf("%s-%s%s", stem, hex.EncodeToString(sum[:4]), suffix))
}

// writeSidecar creates the sidecar directory on demand and writes the file.
//
// Permissions are tight because the OS temp directory is world-writable on some platforms
// (/tmp on Linux) and review comments are the user's private notes.
func writeSidecar(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
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
// re-poll. This endpoint serves `reviewer serve`; agents use review_wait, whose window is set
// separately by MCPOptions.WaitTimeout. A package var so tests can shorten it.
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
		fb = migrateFeedback(fb)
		if out, err := json.Marshal(fb); err == nil {
			return out
		}
	}
	return raw
}

// migrateFeedback brings a sidecar written by an older reviewer into the threaded shape: the
// single Reply becomes the thread's first agent message.
//
// It runs on the read path rather than as a one-off upgrade step because the sidecar is a
// short-lived store under $TMPDIR — there is no migration moment to hook, only the next read.
// No compatibility mirror is kept: nothing writes Reply any more, and omitempty drops it.
func migrateFeedback(fb Feedback) Feedback {
	if fb.Comments == nil {
		fb.Comments = []Comment{}
	}
	for i := range fb.Comments {
		c := &fb.Comments[i]
		if c.Reply == "" || len(c.Messages) > 0 {
			continue
		}
		c.Messages = []Message{{
			Author:    AuthorAgent,
			Text:      c.Reply,
			Timestamp: c.ReplyTimestamp,
		}}
		c.Reply, c.ReplyTimestamp = "", ""
	}
	return fb
}

// pruneResolved drops the threads the human resolved in an earlier round, keeping the ordering of
// the rest. alreadyResolved holds the ids that were stored as resolved before this submit.
//
// A thread the human has just resolved is kept: dropping it on the very submit that carries
// status: "resolved" is what used to stop the agent from ever seeing the status — it could only
// infer resolution from the thread's disappearance, and a declined question would vanish with it.
// Keeping it for one round means the next review_wait delivers it, and the submit after that
// removes it.
func pruneResolved(comments []Comment, alreadyResolved map[string]bool) []Comment {
	kept := make([]Comment, 0, len(comments))
	for _, c := range comments {
		if c.Status == StatusResolved && alreadyResolved[c.ID] {
			continue
		}
		kept = append(kept, c)
	}
	return kept
}

// resolvedIDs is the set of threads already stored as resolved, which is what pruneResolved needs
// to tell "the human resolved this just now" from "the agent has already been told".
func resolvedIDs(comments []Comment) map[string]bool {
	ids := make(map[string]bool, len(comments))
	for _, c := range comments {
		if c.ID != "" && c.Status == StatusResolved {
			ids[c.ID] = true
		}
	}
	return ids
}

// watchForReload debounces filesystem events on the reviewed document and broadcasts a
// "reload" so the page picks up the agent's edits.
//
// Only the document is watched. The feedback and status files used to be watched too, back when
// the agent wrote them directly; now the session is their only writer and broadcasts the event
// itself. That also avoids watching a directory shared by every review on the machine, where one
// session's writes would fire another session's page.
func watchForReload(watcher *fsnotify.Watcher, inputPath string, hub *sseHub, done <-chan struct{}) {
	wantInput := filepath.Clean(inputPath)

	var reloadTimer *time.Timer
	debounceReload := func() {
		if reloadTimer != nil {
			reloadTimer.Stop()
		}
		reloadTimer = time.AfterFunc(150*time.Millisecond, func() { hub.broadcast(reloadPayload()) })
	}

	for {
		select {
		case <-done:
			return
		case ev, ok := <-watcher.Events:
			if !ok {
				return
			}
			if filepath.Clean(ev.Name) == wantInput {
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
