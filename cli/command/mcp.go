package command

import (
	"time"

	"github.com/handlename/reviewer"
)

// MCP runs reviewer as an MCP server over stdio. This is the agent-facing entry point:
// the agent drives reviews entirely through tool calls instead of watching files.
type MCP struct {
	Port        int           `short:"p" default:"5500" help:"Preferred port for the review server (falls back to a free port if busy)."`
	WaitTimeout time.Duration `name:"wait-timeout" default:"15m" help:"How long review_wait blocks before reporting outcome=timeout. Keep below the MCP client's idle timeout."`
	NoOpen      bool          `name:"no-open" help:"Do not automatically open the default web browser."`

	ExperimentalReplyNotification bool `name:"experimental-reply-notification" env:"REVIEWER_EXPERIMENTAL_REPLY_NOTIFICATION" hidden:"" help:"Raise a desktop notification when an agent reply lands. Experimental."`
}

func (m *MCP) Run(c *Context) error {
	return reviewer.RunMCPServer(c.Ctx, reviewer.MCPOptions{
		WaitTimeout: m.WaitTimeout,
		NoOpen:      m.NoOpen,
		Port:        m.Port,

		ReplyNotification: m.ExperimentalReplyNotification,
	})
}
