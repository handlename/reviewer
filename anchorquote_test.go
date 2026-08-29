package reviewer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// An Anchor Quote is copied from the Markdown source, but the page matches it against the
// rendered text of a block (references/template.html, resolveQuoteAnchors). Every quote that
// carries inline Markdown syntax therefore fails to match the very block it was copied from,
// and the thread loses its target.
//
// The quotes here are the ones the reproduction drove through review_reply; want is the text
// the browser reports for the block each was copied from.
func TestNormalizeAnchorQuoteMatchesRenderedText(t *testing.T) {
	tests := []struct {
		name  string
		quote string
		want  string
	}{
		{
			name:  "plain prose is already the rendered text",
			quote: "This is a plain paragraph with no markup at all and it serves as the control case.",
			want:  "This is a plain paragraph with no markup at all and it serves as the control case.",
		},
		{
			name:  "strong emphasis",
			quote: "The retry budget is **at most three attempts** before the request is abandoned.",
			want:  "The retry budget is at most three attempts before the request is abandoned.",
		},
		{
			name:  "code span",
			quote: "The column name is `segment_lock` with no generation suffix.",
			want:  "The column name is segment_lock with no generation suffix.",
		},
		{
			name:  "heading marker",
			quote: "## Fallback Strategy",
			want:  "Fallback Strategy",
		},
		{
			name:  "list marker with strong emphasis",
			quote: "- The queue drains **oldest first** when back-pressure is applied.",
			want:  "The queue drains oldest first when back-pressure is applied.",
		},
		{
			// An inline element contributes no whitespace to textContent, so neither may it here:
			// a space where the browser has none costs the match the period at the end.
			name:  "code span followed immediately by punctuation",
			quote: "> The sandbox environment URL is `https://sandbox.gateway-api.com/v3`.",
			want:  "The sandbox environment URL is https://sandbox.gateway-api.com/v3.",
		},
		{
			// A tr is a comment target of its own, so a quoted row has to resolve. On its own the
			// row is not a table — GFM wants the delimiter row too — and read as a paragraph its
			// pipes survive into the text, which no rendered row ever contains.
			name:  "table row quoted on its own",
			quote: "| `/api/payment/refund` | `POST` | Refund a processed payment. | [Should] | [Inferred] |",
			want:  "/api/payment/refund POST Refund a processed payment. Should Inferred",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeAnchorQuote(tt.quote); got != tt.want {
				t.Errorf("NormalizeAnchorQuote(%q)\n got = %q\nwant = %q", tt.quote, got, tt.want)
			}
		})
	}
}

// The page matches an Anchor Quote against an element's textContent, so the text form has to
// reach it. GET /api/feedback is the page's only source of comments, so that is where it is
// derived — and it is derived on every read rather than stored, which is what lets a sidecar
// written before this existed resolve its threads too.
func TestFeedbackForDisplay_CarriesTheQuoteAsRenderedText(t *testing.T) {
	ctx := t.Context()

	s, err := StartSession(ctx, writeTempSpec(t), 0, true)
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	defer s.Close()

	err = s.Reply(nil, []AskInput{
		{Quote: "The retry budget is **at most three attempts** here.", Question: "why three?"},
		{Question: "a question about the document as a whole"},
	}, "round 1")
	if err != nil {
		t.Fatalf("Reply failed: %v", err)
	}

	var shown Feedback
	if err := json.Unmarshal(s.feedbackForDisplay(), &shown); err != nil {
		t.Fatalf("failed to decode /api/feedback payload: %v", err)
	}
	if len(shown.Comments) != 2 {
		t.Fatalf("got %d comments, want 2: %#v", len(shown.Comments), shown.Comments)
	}

	const want = "The retry budget is at most three attempts here."
	if got := shown.Comments[0].AnchorQuoteText; got != want {
		t.Errorf("AnchorQuoteText = %q, want %q", got, want)
	}
	// A question about the document as a whole has no quote, so it must not gain a text form
	// either — an empty quote is how the page tells "no target" from "target not found".
	if got := shown.Comments[1].AnchorQuoteText; got != "" {
		t.Errorf("document-level thread got AnchorQuoteText = %q, want empty", got)
	}

	// Derived, never stored: the sidecar keeps the quote the agent wrote and nothing else.
	stored := s.readFeedbackDoc().Comments
	if got := stored[0].AnchorQuoteText; got != "" {
		t.Errorf("sidecar stored AnchorQuoteText = %q; it is derived on read, not persisted", got)
	}
}

// A diff review resolves its quotes in Go, against the diff lines themselves, and the page never
// looks at the text form there. Deriving one anyway would put a Markdown reading of a line of
// source code on the wire — meaningless, and an invitation to use it.
func TestFeedbackForDisplay_DerivesNoQuoteTextForADiff(t *testing.T) {
	ctx := t.Context()

	path := filepath.Join(t.TempDir(), "change.diff")
	if err := os.WriteFile(path, []byte(round1Diff), 0644); err != nil {
		t.Fatalf("failed to write temp diff: %v", err)
	}

	s, err := StartSession(ctx, path, 0, true)
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	defer s.Close()

	if err := s.Reply(nil, []AskInput{{Quote: "\treplacement()", Question: "why?"}}, "round 1"); err != nil {
		t.Fatalf("Reply failed: %v", err)
	}

	var shown Feedback
	if err := json.Unmarshal(s.feedbackForDisplay(), &shown); err != nil {
		t.Fatalf("failed to decode /api/feedback payload: %v", err)
	}
	got := shown.Comments[0]
	if got.Anchor == "" {
		t.Fatalf("a diff quote should have been resolved to an anchor by the server: %#v", got)
	}
	if got.AnchorQuoteText != "" {
		t.Errorf("AnchorQuoteText = %q, want empty on a diff review", got.AnchorQuoteText)
	}
}
