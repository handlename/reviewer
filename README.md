# reviewer

reviewer is a spec-to-readable HTML compiler and review server.

## Features

- Compiling Markdown to styled HTML documents
- Interactive local review server
- Gutter commenting on specific block elements
- Inline comment editing (with keyboard accessibility) and deletion before submission

## Synopsis

```console
$ reviewer build <input.md> -o <output.html>
$ reviewer serve <input.md> -p <port>
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

## Agent Skill

This repository ships the `review-doc` agent skill, which drives `reviewer serve` to run an
interactive review loop: the human leaves gutter comments on the rendered page, the agent edits
the document and replies inline, and the page reloads automatically.

The skill lives in [`skills/review-doc/`](skills/review-doc/) and is shared by both installation
paths below. It requires the `reviewer` CLI on your `PATH` — install it first (see
[Installation](#installation)).

### Claude Code

This repository is also a [Claude Code plugin marketplace](https://code.claude.com/docs/en/plugin-marketplaces).
Add the marketplace once, then install the plugin:

```console
$ claude plugin marketplace add handlename/reviewer
$ claude plugin install reviewer@reviewer
```

### Other AI agents

For agents supporting `gh skill` (GitHub Copilot, Gemini CLI, Cursor, and others), install the
skill with the GitHub CLI:

```console
$ gh skill install handlename/reviewer review-doc
```

## License

MIT

## Author

@handlename
