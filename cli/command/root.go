package command

import "github.com/alecthomas/kong"

type Root struct {
	LogLevel string           `help:"Set log level. (trace|debug|info|warn|error|panic)" default:"info"`
	Version  kong.VersionFlag `help:"Show version."`
}

func (r *Root) Run(c *Context) error {
	return c.App.Run(c.Ctx)
}
