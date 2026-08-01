package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"

	"github.com/alecthomas/kong"
	"github.com/handlename/reviewer"
	"github.com/handlename/reviewer/cli/command"
	"github.com/morikuni/failure/v2"
	"github.com/rs/zerolog/log"
)

type ExitCode int

const (
	ExitCodeOK    ExitCode = 0
	ExitCodeError ExitCode = 1
)

func Run() ExitCode {
	var root command.Root
	ktx := kong.Parse(&root, kong.Vars{"version": fmt.Sprintf("reviewer v%s", reviewer.Version)})

	reviewer.InitLogger(root.LogLevel)

	// TODO: build options for new App

	app := reviewer.New()

	// TODO: build options to run App

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := ktx.Run(&command.Context{Ctx: ctx, App: app}); err != nil {
		if errors.Is(err, context.Canceled) {
			log.Error().Msg("canceled")
		} else {
			handleError(err)
		}

		return ExitCodeError
	}

	return ExitCodeOK
}

// handleError writes the error dump to stderr, never stdout. `reviewer mcp` serves JSON-RPC
// over stdout, so a diagnostic printed there would be parsed as a protocol message and break
// the client's session. Stderr is also simply the right place for diagnostics.
func handleError(err error) {
	fmt.Fprintln(os.Stderr, "======== error ========")

	code := failure.CodeOf(err)
	fmt.Fprintf(os.Stderr, "code = %s\n", code)

	msg := failure.MessageOf(err)
	fmt.Fprintf(os.Stderr, "message = %s\n", msg)

	cs := failure.CallStackOf(err)
	fmt.Fprintf(os.Stderr, "callstack = %s\n", cs)

	fmt.Fprintf(os.Stderr, "cause = %s\n", failure.CauseOf(err))

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "======== detail ========")
	fmt.Fprintf(os.Stderr, "%+v\n", err)
}
