package main

import (
	"os"

	"github.com/handlename/reviewer/cli"
)

func main() {
	os.Exit(int(cli.Run()))
}
