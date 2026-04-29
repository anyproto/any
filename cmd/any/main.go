package main

import (
	"os"

	"github.com/anyproto/any/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
