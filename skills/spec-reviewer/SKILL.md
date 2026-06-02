---
name: spec-reviewer
description: Interactive spec document reviewer using the reviewer CLI with browser-based live preview.
allowed-tools: view_file replace_file_content write_to_file run_command
---
# Spec Reviewer Skill

This skill guides the AI agent to conduct interactive reviews of specification documents (Markdown files) using the local `reviewer` tool and a live browser preview.

## When to Use
Use this skill when a user asks to review, refine, or write specification documents in the repository.

## Workflow

### 1. Discover the Target Specification
Locate the specification file to review.
- Search in standard locations like `docs/superpowers/specs/` or look for recently modified `.md` files.
- If multiple spec files are found, ask the user to clarify which file they want to review.

### 2. Launch the Review Server
Run the local `reviewer` server in the background to compile and serve the specification.
- Execute the following command:
  ```bash
  go run ./cmd/reviewer serve <path/to/spec.md>
  ```
- By default, do NOT pass the `--no-open` flag so that the default web browser automatically opens the compiled HTML.
- Inform the user that the review server has started and the browser preview is open (typically at `http://localhost:5500`).

### 3. Conduct Interactive Review
While the user views the spec in the browser, analyze the Markdown content yourself. Focus on:
- **Clarity & Completeness**: Are there any placeholders (e.g., TBD, TODO), missing definitions, or ambiguities?
- **Internal Consistency**: Do the architectural designs align with the feature requirements?
- **Readability**: Is the heading hierarchy logical and clean?
- **Reviewer System Invariants**: Check for nested blocks (e.g., lists inside callouts, nested tables) that might break the rendering or comment anchor logic (as per guidelines in `AGENTS.md`).

### 4. Iterate and Refine
Engage in a structured dialogue with the user to improve the spec:
- Ask clarifying questions or propose improvements one at a time.
- When changes are agreed upon, edit the Markdown file using your file editing tools.
- Remind the user that the server will automatically reflect the changes in their browser upon reload.
- Continue this cycle until the user is fully satisfied with the specification.
