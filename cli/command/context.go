package command

import (
	"context"

	myapp "github.com/handlename/my-golang-template"
)

type Context struct {
	Ctx context.Context
	App *myapp.App
}
