package command

import "github.com/alecthomas/kong"

type Root struct {
	LogLevel string           `help:"Set log level. (trace|debug|info|warn|error|panic)" default:"info"`
	Version  kong.VersionFlag `help:"Show version."`

	Build Build `cmd:"" help:"Build static HTML spec file."`
	Serve Serve `cmd:"" help:"Serve interactive review server."`
}

func (r *Root) Run(c *Context) error {
	return nil
}
