package reviewer

import (
	"os"
	"strings"
	"testing"
)

func TestAgentSkillInstructionsMatchTheCanonicalFile(t *testing.T) {
	want, err := os.ReadFile("references/skills/review-doc.md")
	if err != nil {
		t.Fatalf("failed to read canonical instructions: %v", err)
	}

	if AgentSkillInstructions() != string(want) {
		t.Error("instructions do not match references/skills/review-doc.md byte for byte")
	}
}

// The whole point of embedding is that an agent reading the stub SKILL.md still gets the full
// workflow, so the output has to carry the parts the stub no longer describes.
func TestAgentSkillInstructionsCoverTheWorkflowDroppedFromSkillFile(t *testing.T) {
	instructions := AgentSkillInstructions()

	for _, want := range []string{
		"review_start",
		"review_wait",
		"review_reply",
		"review_progress",
		"claude mcp add reviewer",
		"brew install handlename/tap/reviewer",
	} {
		if !strings.Contains(instructions, want) {
			t.Errorf("instructions are missing %q", want)
		}
	}
}
