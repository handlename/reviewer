package reviewer

import (
	"bufio"
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
	ctx := t.Context()

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
	ctx := t.Context()

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
	ctx := t.Context()

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
	ctx := t.Context()

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
	ctx := t.Context()

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
	ctx := t.Context()

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
	ctx := t.Context()

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
	ctx := t.Context()

	s, err := StartSession(ctx, writeTempSpec(t), 0, true)
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	defer s.Close()

	id := submitComment(t, s, "needs work")

	if err := s.Reply([]ReplyInput{{CommentID: id, Reply: "fixed in section 2"}}, nil, "round 1 changes"); err != nil {
		t.Fatalf("Reply failed: %v", err)
	}

	fb := s.readFeedbackDoc()
	msgs := fb.Comments[0].Messages
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want the reply threaded as one: %#v", len(msgs), msgs)
	}
	if msgs[0].Author != AuthorAgent || msgs[0].Text != "fixed in section 2" {
		t.Fatalf("got message %#v, want the agent's reply", msgs[0])
	}
	if msgs[0].Timestamp == "" {
		t.Fatal("the reply should have been timestamped")
	}
	if fb.Summary != "round 1 changes" {
		t.Fatalf("got summary %q, want %q", fb.Summary, "round 1 changes")
	}
	// The original human fields must survive untouched.
	if fb.Comments[0].Text != "needs work" {
		t.Fatalf("got text %q, want %q", fb.Comments[0].Text, "needs work")
	}
}

// Replies accumulate: a second round appends to the thread rather than overwriting the first.
func TestSessionReply_AppendsToTheThread(t *testing.T) {
	ctx := t.Context()

	s, err := StartSession(ctx, writeTempSpec(t), 0, true)
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	defer s.Close()

	id := submitComment(t, s, "needs work")

	if err := s.Reply([]ReplyInput{{CommentID: id, Reply: "first pass"}}, nil, ""); err != nil {
		t.Fatalf("Reply failed: %v", err)
	}
	if err := s.Reply([]ReplyInput{{CommentID: id, Reply: "which style do you want?", NeedsAnswer: true}}, nil, ""); err != nil {
		t.Fatalf("Reply failed: %v", err)
	}

	c := s.readFeedbackDoc().Comments[0]
	if len(c.Messages) != 2 {
		t.Fatalf("got %d messages, want both replies: %#v", len(c.Messages), c.Messages)
	}
	if c.Messages[0].Text != "first pass" || c.Messages[0].NeedsAnswer {
		t.Errorf("first message = %#v, want an ordinary report", c.Messages[0])
	}
	if c.Messages[1].Text != "which style do you want?" || !c.Messages[1].NeedsAnswer {
		t.Errorf("second message = %#v, want the flagged question", c.Messages[1])
	}
	if !c.PendingQuestion() {
		t.Error("the thread should be awaiting an answer")
	}
	if c.Text != "needs work" {
		t.Errorf("the human's own text changed to %q", c.Text)
	}
}

// A reply without needsAnswer must behave exactly as it did before questions existed: the flag is
// absent from the sidecar, and the thread is not awaiting anything.
func TestSessionReply_WithoutNeedsAnswerIsUnchanged(t *testing.T) {
	ctx := t.Context()

	s, err := StartSession(ctx, writeTempSpec(t), 0, true)
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	defer s.Close()

	id := submitComment(t, s, "needs work")
	if err := s.Reply([]ReplyInput{{CommentID: id, Reply: "fixed"}}, nil, ""); err != nil {
		t.Fatalf("Reply failed: %v", err)
	}

	if s.readFeedbackDoc().Comments[0].PendingQuestion() {
		t.Error("an ordinary reply left the thread awaiting an answer")
	}
	raw, err := os.ReadFile(s.feedbackPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "needsAnswer") {
		t.Errorf("needsAnswer leaked into the sidecar: %s", raw)
	}
}

// The agent has to learn that a thread was resolved, not infer it from the thread's absence:
// a resolved thread reaches review_wait exactly once, then disappears.
func TestSessionWait_DeliversResolvedOnceThenPrunes(t *testing.T) {
	ctx := t.Context()

	s, err := StartSession(ctx, writeTempSpec(t), 0, true)
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	defer s.Close()

	id := submitComment(t, s, "needs work")

	// The human marks it resolved and submits again.
	postComments(t, s, `[{"id":"`+id+`","text":"needs work","timestamp":"2026-01-01T00:00:00Z","author":"human","status":"resolved"}]`)

	got := s.Wait(ctx, time.Second)
	if got.Outcome != WaitSubmitted {
		t.Fatalf("outcome = %q, want %q", got.Outcome, WaitSubmitted)
	}
	if len(got.Comments) != 1 || got.Comments[0].Status != StatusResolved {
		t.Fatalf("the resolved thread never reached the agent: %#v", got.Comments)
	}

	// The page posts back what it holds; the thread has been delivered, so now it goes.
	postComments(t, s, `[{"id":"`+id+`","text":"needs work","timestamp":"2026-01-01T00:00:00Z","author":"human","status":"resolved"}]`)

	if left := s.readFeedbackDoc().Comments; len(left) != 0 {
		t.Fatalf("the resolved thread survived a second round: %#v", left)
	}
}

// postComments submits a raw comments array, the way the page does.
func postComments(t *testing.T, s *ReviewSession, comments string) {
	t.Helper()
	resp, err := http.Post(s.URL()+"/api/feedback", "application/json",
		strings.NewReader(`{"comments":`+comments+`,"summary":""}`))
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}
	resp.Body.Close()
}

// The agent can raise something the human never commented on, by opening a thread of its own.
func TestSessionReply_OpensAgentThreads(t *testing.T) {
	ctx := t.Context()

	s, err := StartSession(ctx, writeTempSpec(t), 0, true)
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	defer s.Close()

	id := submitComment(t, s, "needs work")

	err = s.Reply(
		[]ReplyInput{{CommentID: id, Reply: "done"}},
		[]AskInput{
			{Quote: "Retry at most three times", Question: "Three retries or five?"},
			{Question: "Should this document cover the CLI as well?"},
		},
		"round 1",
	)
	if err != nil {
		t.Fatalf("Reply failed: %v", err)
	}

	got := s.readFeedbackDoc().Comments
	if len(got) != 3 {
		t.Fatalf("got %d comments, want the human's plus two agent threads: %#v", len(got), got)
	}

	quoted := got[1]
	if quoted.Author != AuthorAgent || quoted.Status != StatusOpen || !quoted.NeedsAnswer {
		t.Errorf("agent thread = %#v, want an open agent question", quoted)
	}
	if quoted.ID == "" {
		t.Error("an agent thread must be addressable, so the server assigns it an id")
	}
	if quoted.Text != "Three retries or five?" || quoted.AnchorQuote != "Retry at most three times" {
		t.Errorf("agent thread = %#v", quoted)
	}
	if !quoted.PendingQuestion() {
		t.Error("an agent thread should be awaiting an answer from the moment it is opened")
	}

	// An empty quote is a question about the document as a whole: no target, and not outdated.
	whole := got[2]
	if whole.AnchorQuote != "" || whole.Anchor != "" || whole.Outdated {
		t.Errorf("document-level thread = %#v, want no target and not outdated", whole)
	}
}

// A quote is never validated against the document: doing so would mean teaching Go the browser's
// spec-element numbering, the duplication AGENTS.md section 4 exists to prevent.
func TestSessionReply_AcceptsAQuoteThatMatchesNothing(t *testing.T) {
	ctx := t.Context()

	s, err := StartSession(ctx, writeTempSpec(t), 0, true)
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	defer s.Close()

	if err := s.Reply(nil, []AskInput{{Quote: "no such passage anywhere", Question: "?"}}, ""); err != nil {
		t.Fatalf("Reply rejected a quote it cannot resolve: %v", err)
	}
}

// One round is one write and one reload. Splitting questions into a tool of their own would make
// it two of each, and the page would paint the state in between.
func TestSessionReply_IsOneReloadForTheWholeRound(t *testing.T) {
	ctx := t.Context()

	s, err := StartSession(ctx, writeTempSpec(t), 0, true)
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	defer s.Close()

	id := submitComment(t, s, "needs work")

	reqCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(reqCtx, http.MethodGet, s.URL()+"/api/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to connect to SSE: %v", err)
	}
	defer resp.Body.Close()

	reloads := make(chan struct{}, 8)
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			if line := scanner.Text(); strings.HasPrefix(line, "data:") && strings.Contains(line, `"kind":"reload"`) {
				reloads <- struct{}{}
			}
		}
	}()
	time.Sleep(150 * time.Millisecond)

	err = s.Reply(
		[]ReplyInput{{CommentID: id, Reply: "done"}},
		[]AskInput{{Question: "and this?"}},
		"round 1",
	)
	if err != nil {
		t.Fatalf("Reply failed: %v", err)
	}

	select {
	case <-reloads:
	case <-time.After(2 * time.Second):
		t.Fatal("no reload was pushed for the round")
	}
	select {
	case <-reloads:
		t.Error("the round pushed a second reload; replies and new threads must land together")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestSessionReply_CannotResolveComment(t *testing.T) {
	ctx := t.Context()

	s, err := StartSession(ctx, writeTempSpec(t), 0, true)
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	defer s.Close()

	id := submitComment(t, s, "needs work")

	if err := s.Reply([]ReplyInput{{CommentID: id, Reply: "done"}}, nil, ""); err != nil {
		t.Fatalf("Reply failed: %v", err)
	}

	// Resolution is the human's decision; replying must never flip it.
	if got := s.readFeedbackDoc().Comments[0].Status; got != StatusOpen {
		t.Fatalf("got status %q, want %q — the agent must not resolve comments", got, StatusOpen)
	}
}

func TestSessionReply_RejectsUnknownCommentID(t *testing.T) {
	ctx := t.Context()

	s, err := StartSession(ctx, writeTempSpec(t), 0, true)
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	defer s.Close()

	submitComment(t, s, "needs work")

	if err := s.Reply([]ReplyInput{{CommentID: "nonexistent", Reply: "done"}}, nil, ""); err == nil {
		t.Fatal("Reply should reject an unknown comment ID")
	}
}

func TestSessionProgress_WritesStatusFile(t *testing.T) {
	ctx := t.Context()

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
	ctx := t.Context()

	s, err := StartSession(ctx, writeTempSpec(t), 0, true)
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	defer s.Close()

	if err := s.Progress("thinking", "hmm"); err == nil {
		t.Fatal("Progress should reject a state outside working|idle")
	}
}

// summariseThreads renders merged comments as "author:text[msg,msg]" lines, so a mismatch reads as
// the thread it is rather than a diff of two structs.
func summariseThreads(comments []Comment) []string {
	out := make([]string, 0, len(comments))
	for _, c := range comments {
		author := c.Author
		if author == "" {
			author = AuthorHuman
		}
		line := author + ":" + c.Text
		if c.Status != "" && c.Status != StatusOpen {
			line += "(" + c.Status + ")"
		}
		msgs := make([]string, 0, len(c.Messages))
		for _, m := range c.Messages {
			line := m.Author + ":" + m.Text
			if m.Declined {
				line += "!declined"
			}
			msgs = append(msgs, line)
		}
		out = append(out, line+"["+strings.Join(msgs, ",")+"]")
	}
	return out
}

func humanThread(id, text string, msgs ...Message) Comment {
	return Comment{ID: id, Text: text, Author: AuthorHuman, Status: StatusOpen, Messages: msgs}
}

func agentThread(id, text string, msgs ...Message) Comment {
	return Comment{ID: id, Text: text, Author: AuthorAgent, Status: StatusOpen, NeedsAnswer: true, Messages: msgs}
}

func humanMsg(text string) Message { return Message{Author: AuthorHuman, Text: text} }
func agentMsg(text string) Message { return Message{Author: AuthorAgent, Text: text} }

// mergeFeedback is what stops a submit from overwriting what the agent wrote while the human was
// still typing. The browser can only ever change its own content — it has no control that edits or
// deletes an agent message, and none that deletes an agent-opened thread — so anything of the
// agent's that is missing from the payload is missing because the page had not reloaded, never
// because the human removed it.
func TestMergeFeedback(t *testing.T) {
	tests := []struct {
		name     string
		stored   []Comment
		incoming []Comment
		want     []string
	}{
		{
			name:     "an agent reply the browser never saw survives the submit",
			stored:   []Comment{humanThread("a", "needs work", agentMsg("fixed it"))},
			incoming: []Comment{humanThread("a", "needs work")},
			want:     []string{"human:needs work[agent:fixed it]"},
		},
		{
			name:     "an agent-opened thread the browser never saw survives the submit",
			stored:   []Comment{humanThread("a", "needs work"), agentThread("b", "which style?")},
			incoming: []Comment{humanThread("a", "needs work")},
			want:     []string{"human:needs work[]", "agent:which style?[]"},
		},
		{
			name:     "deleting the human's own thread drops it along with its agent replies",
			stored:   []Comment{humanThread("a", "needs work", agentMsg("fixed it")), humanThread("b", "and this")},
			incoming: []Comment{humanThread("b", "and this")},
			want:     []string{"human:and this[]"},
		},
		{
			name:     "the human's edit of their own words wins, the agent's reply stays",
			stored:   []Comment{humanThread("a", "old head", humanMsg("old note"), agentMsg("fixed it"))},
			incoming: []Comment{humanThread("a", "new head", humanMsg("new note"), agentMsg("fixed it"))},
			want:     []string{"human:new head[human:new note,agent:fixed it]"},
		},
		{
			name:     "a brand new comment carries no id yet and is kept",
			stored:   []Comment{},
			incoming: []Comment{{Text: "fresh remark", Author: AuthorHuman, Status: StatusOpen}},
			want:     []string{"human:fresh remark[]"},
		},
		{
			name:     "missed agent messages land at the tail in the order the agent wrote them",
			stored:   []Comment{humanThread("a", "needs work", agentMsg("r1"), agentMsg("r2"), agentMsg("r3"))},
			incoming: []Comment{humanThread("a", "needs work", agentMsg("r1"), humanMsg("answer"))},
			want:     []string{"human:needs work[agent:r1,human:answer,agent:r2,agent:r3]"},
		},
		{
			name:     "resolving a thread is the human's word and is carried through",
			stored:   []Comment{humanThread("a", "needs work", agentMsg("fixed it"))},
			incoming: []Comment{{ID: "a", Text: "needs work", Author: AuthorHuman, Status: StatusResolved}},
			want:     []string{"human:needs work(resolved)[agent:fixed it]"},
		},
		{
			// declined is the one agent-authored field the human writes: closing a question without
			// answering it stamps the agent's own message. Sourcing agent messages from the sidecar
			// wholesale would throw that stamp away and leave the question pending forever.
			name:   "the human declining a question sticks to the agent's message",
			stored: []Comment{humanThread("a", "needs work", agentMsg("which style?"))},
			incoming: []Comment{humanThread("a", "needs work",
				Message{Author: AuthorAgent, Text: "which style?", NeedsAnswer: true, Declined: true})},
			want: []string{"human:needs work[agent:which style?!declined]"},
		},
		{
			name:     "a submit carrying nothing still cannot erase the agent's own threads",
			stored:   []Comment{humanThread("a", "needs work"), agentThread("b", "which style?")},
			incoming: []Comment{},
			want:     []string{"agent:which style?[]"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := summariseThreads(mergeFeedback(Feedback{Comments: tt.stored}, Feedback{Comments: tt.incoming}).Comments)
			if strings.Join(got, " | ") != strings.Join(tt.want, " | ") {
				t.Errorf("merged threads\n got: %v\nwant: %v", got, tt.want)
			}
		})
	}
}

// The end-to-end shape of the bug: the human keeps the composer open, so the page defers its
// reload, and the agent answers in the meantime. Submitting used to post the stale array back and
// take the agent's reply with it.
func TestFeedbackPost_KeepsWhatTheAgentWroteWhileTheHumanWasTyping(t *testing.T) {
	ctx := t.Context()

	s, err := StartSession(ctx, writeTempSpec(t), 0, true)
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	defer s.Close()

	id := submitComment(t, s, "needs work")

	// The page is showing this round; the human has not reloaded since.
	stale := s.readFeedbackDoc().Comments

	if err := s.Reply([]ReplyInput{{CommentID: id, Reply: "fixed in section 2"}}, []AskInput{{Question: "which style do you want?"}}, "round 1"); err != nil {
		t.Fatalf("Reply failed: %v", err)
	}

	// The human submits the array the page still holds.
	body, err := json.Marshal(Feedback{Comments: stale, Summary: ""})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(s.URL()+"/api/feedback", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}
	resp.Body.Close()

	got := summariseThreads(s.readFeedbackDoc().Comments)
	want := []string{"human:needs work[agent:fixed in section 2]", "agent:which style do you want?[]"}
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Errorf("after the stale submit\n got: %v\nwant: %v", got, want)
	}
}

// Deleting a thread stays the human's call, even when the agent has answered in it since the page
// last loaded: the alternative is a thread that reappears after it was removed.
func TestFeedbackPost_HonoursDeletingAThreadTheAgentHasSinceAnswered(t *testing.T) {
	ctx := t.Context()

	s, err := StartSession(ctx, writeTempSpec(t), 0, true)
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	defer s.Close()

	id := submitComment(t, s, "needs work")
	if err := s.Reply([]ReplyInput{{CommentID: id, Reply: "fixed in section 2"}}, nil, ""); err != nil {
		t.Fatalf("Reply failed: %v", err)
	}

	resp, err := http.Post(s.URL()+"/api/feedback", "application/json", strings.NewReader(`{"comments":[],"summary":""}`))
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}
	resp.Body.Close()

	if got := summariseThreads(s.readFeedbackDoc().Comments); len(got) != 0 {
		t.Errorf("the deleted thread came back: %v", got)
	}
}
