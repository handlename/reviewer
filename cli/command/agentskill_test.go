package command

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/alecthomas/kong"
	"github.com/handlename/reviewer"
)

// The distributed SKILL.md is a stub that runs exactly this, with no arguments. Skill files are
// frozen at install time, so the bare form has to keep parsing even if more skills are added.
func TestAgentSkillExplainParsesWithoutArguments(t *testing.T) {
	var root Root

	parser, err := kong.New(&root,
		kong.Vars{"version": "reviewer vtest"},
		kong.Writers(io.Discard, io.Discard),
		kong.Exit(func(int) {}),
	)
	if err != nil {
		t.Fatalf("failed to build parser: %v", err)
	}

	ktx, err := parser.Parse([]string{"agent-skill", "explain"})
	if err != nil {
		t.Fatalf("failed to parse: %v", err)
	}

	if got := ktx.Command(); got != "agent-skill explain" {
		t.Errorf("unexpected command selected: %q", got)
	}
}

// The MCP server speaks JSON-RPC over stdout, so the CLI has a standing rule that only real
// output goes there. Explain is the one command whose payload IS stdout.
func TestAgentSkillExplainRunWritesInstructionsToStdout(t *testing.T) {
	original := os.Stdout

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	os.Stdout = w

	e := &AgentSkillExplain{}
	runErr := e.Run(&Context{Ctx: context.Background(), App: reviewer.New()})

	w.Close()
	os.Stdout = original

	captured, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("failed to read captured stdout: %v", err)
	}

	if runErr != nil {
		t.Fatalf("Run failed: %v", runErr)
	}

	if !strings.Contains(string(captured), "# Review Doc Skill") {
		t.Errorf("stdout does not carry the skill instructions, got %q", string(captured))
	}
}
