package reviewer

import (
	"context"

	errcode "github.com/handlename/reviewer/internal/errorcode"
	"github.com/morikuni/failure/v2"
)

type App struct{}

func New() *App {
	return &App{}
}

func (a *App) Run(ctx context.Context) error {
	return failure.New(errcode.ErrNotImplemented, failure.Message("not implemented yet"))
}
