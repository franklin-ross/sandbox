package main

import (
	"github.com/franklin-ross/sandbox/cmd"
	_ "github.com/franklin-ross/sandbox/cmd/commands"
)

func main() {
	cmd.Execute()
}
