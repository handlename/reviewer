package reviewer

import (
	_ "embed"
)

//go:embed references/skills/review-doc.md
var agentSkillInstructions string

// AgentSkillInstructions returns the instructions for the agent skill this binary ships.
//
// Why not ship them in the distributed SKILL.md instead: a skill file is installed once and then
// frozen, so it drifts away from whichever reviewer binary the agent is actually running. Keeping
// the text in the binary makes the two impossible to disagree.
//
// Why no skill name to select on: reviewer has exactly one agent workflow. Should a second skill
// ever appear, the bare form has to keep meaning review-doc — already-installed skill files call
// it with no argument, and breaking them is the drift this whole design exists to prevent.
func AgentSkillInstructions() string {
	return agentSkillInstructions
}
