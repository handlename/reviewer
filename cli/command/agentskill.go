package command

import (
	"fmt"
	"os"

	"github.com/handlename/reviewer"
)

type AgentSkill struct {
	Explain AgentSkillExplain `cmd:"" help:"Print the agent skill instructions."`
}

type AgentSkillExplain struct{}

func (e *AgentSkillExplain) Run(c *Context) error {
	fmt.Fprint(os.Stdout, reviewer.AgentSkillInstructions())

	return nil
}
