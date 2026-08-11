# reviewer

reviewer is a spec-to-readable HTML compiler and review server.

## Features

- Compiling Markdown to styled HTML documents
- Interactive local review server
- Gutter commenting on specific block elements
- Inline comment editing (with keyboard accessibility) and deletion before submission
- A built-in MCP server, so AI agents drive the review loop through standard tool calls

## Synopsis

```console
$ reviewer build <input.md> -o <output.html>
$ reviewer serve <input.md> -p <port>
$ reviewer mcp
$ reviewer agent-skill explain
```

## Installation

Using Homebrew:

```console
$ brew install handlename/tap/reviewer
```

Or using `go install`:

```console
$ go install github.com/handlename/reviewer/cmd/reviewer@latest
```

## For AI agents

`reviewer mcp` runs reviewer as an [MCP](https://modelcontextprotocol.io/) server over stdio.
It is the agent-facing entry point, and it owns the whole review: it renders the document, serves
it, opens the browser, and exposes four tools.

| Tool | Purpose |
| --- | --- |
| `review_start` | Open a document for review. Returns the review URL. |
| `review_wait` | Block until the human submits. Returns their comments. |
| `review_reply` | Write a reply under each comment, plus a summary of the round. |
| `review_progress` | Report the agent's current activity, live, on the review page. |

The loop is: `review_start`, then `review_wait`, edit the document, `review_reply`, and back to
`review_wait`. `review_wait` reports `submitted`, `timeout` (nothing yet — call again), or
`session_ended` (the human clicked **End Review**). A timeout is an ordinary result, not a
failure, so `--wait-timeout` can be tuned freely.

Marking a comment resolved is the human's decision alone; no tool can do it.

This repository also ships the `review-doc` agent skill, which is simply the workflow written
down. The workflow itself is compiled into the `reviewer` binary and printed by
`reviewer agent-skill explain`; the distributed skill file at
[`skills/review-doc/`](skills/review-doc/) does nothing but point at that command. An installed
skill file is frozen at install time, so keeping the instructions in the binary is what stops
an agent from following a workflow that its `reviewer` no longer implements. The canonical text
lives in [`references/skills/review-doc.md`](references/skills/review-doc.md).

Either way, the `reviewer` CLI must be on your `PATH` — install it first (see
[Installation](#installation)).

### Claude Code

This repository is also a [Claude Code plugin marketplace](https://code.claude.com/docs/en/plugin-marketplaces).
Add the marketplace once, then install the plugin. The plugin registers the MCP server for you:

```console
$ claude plugin marketplace add handlename/reviewer
$ claude plugin install reviewer@reviewer
```

### Other AI agents

Register the MCP server with your client. For Claude Code without the plugin:

```console
$ claude mcp add reviewer -- reviewer mcp
```

Agents supporting `gh skill` (GitHub Copilot, Gemini CLI, Cursor, and others) can install the
workflow description with the GitHub CLI, then register the server the way that client expects:

```console
$ gh skill install handlename/reviewer review-doc
```

## License

MIT

## Author

@handlename
