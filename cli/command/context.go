package command

import (
	"context"

	"github.com/handlename/reviewer"
)

type Context struct {
	Ctx context.Context
	App *reviewer.App
}
