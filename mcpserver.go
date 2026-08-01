package reviewer

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// DefaultWaitTimeout bounds one review_wait call. It sits well under the 30-minute idle
// timeout Claude Code applies to stdio MCP calls, and is deliberately configurable: delivery
// of a result from a call that ran past two minutes relies on the client backgrounding the
// call, which is documented but unverified here. Lowering this below two minutes must keep
// the loop working.
const DefaultWaitTimeout = 15 * time.Minute

// MCPOptions configures the MCP server process.
type MCPOptions struct {
	WaitTimeout time.Duration
	NoOpen      bool
	Port        int
}

// sessionHolder owns the one review session an MCP process may have at a time.
type sessionHolder struct {
	mu      sync.Mutex
	current *ReviewSession
}

func newSessionHolder() *sessionHolder { return &sessionHolder{} }

// live reports the current session if it exists and has not ended. Callers must hold mu.
func (h *sessionHolder) live() *ReviewSession {
	if h.current == nil {
		return nil
	}
	select {
	case <-h.current.Done():
		return nil
	default:
		return h.current
	}
}

func (h *sessionHolder) closeCurrent() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.current != nil {
		_ = h.current.Close()
		h.current = nil
	}
}

type startInput struct {
	Path string `json:"path" jsonschema:"path to the Markdown document to review"`
}

type startOutput struct {
	URL  string `json:"url"`
	Path string `json:"path"`
}

func (h *sessionHolder) start(ctx context.Context, in startInput, opts MCPOptions) (startOutput, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if s := h.live(); s != nil {
		return startOutput{}, fmt.Errorf("a review of %s is already open at %s; end it before starting another", s.InputPath(), s.URL())
	}
	if h.current != nil {
		// The previous session ended; drop it so this one can take its place.
		h.current = nil
	}

	s, err := StartSession(ctx, in.Path, opts.Port, opts.NoOpen)
	if err != nil {
		return startOutput{}, fmt.Errorf("failed to start review of %s: %w", in.Path, err)
	}
	h.current = s
	return startOutput{URL: s.URL(), Path: s.InputPath()}, nil
}

type waitOutput struct {
	Outcome  string    `json:"outcome"`
	Comments []Comment `json:"comments"`
	Summary  string    `json:"summary,omitempty"`
}

// wait returns an error only when there is no session to wait on — an agent mistake. Every
// genuine outcome of waiting (submitted, timeout, session_ended) is a successful result.
func (h *sessionHolder) wait(ctx context.Context, timeout time.Duration) (waitOutput, error) {
	h.mu.Lock()
	s := h.live()
	h.mu.Unlock()
	if s == nil {
		return waitOutput{}, fmt.Errorf("no review is open; call review_start first")
	}

	res := s.Wait(ctx, timeout)
	return waitOutput{
		Outcome:  string(res.Outcome),
		Comments: res.Comments,
		Summary:  res.Summary,
	}, nil
}

type replyInputArgs struct {
	Replies []ReplyInput `json:"replies" jsonschema:"one entry per comment being answered"`
	Summary string       `json:"summary" jsonschema:"short description of this round's changes, shown at the top of the review panel"`
}

type okOutput struct {
	OK bool `json:"ok"`
}

func (h *sessionHolder) reply(in replyInputArgs) (okOutput, error) {
	h.mu.Lock()
	s := h.live()
	h.mu.Unlock()
	if s == nil {
		return okOutput{}, fmt.Errorf("no review is open; call review_start first")
	}
	if err := s.Reply(in.Replies, in.Summary); err != nil {
		return okOutput{}, err
	}
	return okOutput{OK: true}, nil
}

type progressInputArgs struct {
	State   string `json:"state" jsonschema:"working while the round is in progress, idle when it is finished"`
	Message string `json:"message" jsonschema:"short activity line shown on the review page"`
}

func (h *sessionHolder) progress(in progressInputArgs) (okOutput, error) {
	h.mu.Lock()
	s := h.live()
	h.mu.Unlock()
	if s == nil {
		return okOutput{}, fmt.Errorf("no review is open; call review_start first")
	}
	if err := s.Progress(in.State, in.Message); err != nil {
		return okOutput{}, err
	}
	return okOutput{OK: true}, nil
}

// newMCPServer registers the four tools that make up the agent-facing review contract.
func newMCPServer(holder *sessionHolder, opts MCPOptions) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "reviewer", Version: Version}, nil)

	mcp.AddTool(server,
		&mcp.Tool{
			Name:        "review_start",
			Description: "Open a Markdown document for interactive review. Renders it, serves it, and opens the browser. Returns the review URL.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, in startInput) (*mcp.CallToolResult, startOutput, error) {
			out, err := holder.start(ctx, in, opts)
			return nil, out, err
		})

	mcp.AddTool(server,
		&mcp.Tool{
			Name: "review_wait",
			Description: "Block until the human submits their comments. Returns outcome=submitted with the comments, " +
				"outcome=timeout if the wait expired (call again), or outcome=session_ended once the human ends the review. " +
				"A timeout is normal, not a failure.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, waitOutput, error) {
			out, err := holder.wait(ctx, opts.WaitTimeout)
			return nil, out, err
		})

	mcp.AddTool(server,
		&mcp.Tool{
			Name: "review_reply",
			Description: "Write a reply under each comment you addressed, plus a summary of this round's changes. " +
				"Comments are addressed by the id returned from review_wait. Resolving a comment is the human's decision and is not possible here.",
		},
		func(_ context.Context, _ *mcp.CallToolRequest, in replyInputArgs) (*mcp.CallToolResult, okOutput, error) {
			out, err := holder.reply(in)
			return nil, out, err
		})

	mcp.AddTool(server,
		&mcp.Tool{
			Name:        "review_progress",
			Description: "Report what you are doing right now so the human can watch progress on the review page without a reload.",
		},
		func(_ context.Context, _ *mcp.CallToolRequest, in progressInputArgs) (*mcp.CallToolResult, okOutput, error) {
			out, err := holder.progress(in)
			return nil, out, err
		})

	return server
}

// RunMCPServer serves the review tools over stdio until the client disconnects.
//
// Nothing in this process may write to stdout: it is the JSON-RPC transport. zerolog is
// configured to write to stderr in InitLogger.
func RunMCPServer(ctx context.Context, opts MCPOptions) error {
	if opts.WaitTimeout <= 0 {
		opts.WaitTimeout = DefaultWaitTimeout
	}
	holder := newSessionHolder()
	defer holder.closeCurrent()

	return newMCPServer(holder, opts).Run(ctx, &mcp.StdioTransport{})
}
