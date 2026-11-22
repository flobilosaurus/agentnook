package main

import (
	"os"

	"github.com/flobilosaurus/agentnook/internal/cli"
)

func main() {
	os.Exit(cli.App{}.Run(os.Args[1:]))
}
