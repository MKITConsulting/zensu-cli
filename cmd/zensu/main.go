package main

import (
	"fmt"
	"os"

	"github.com/MKITConsulting/zensu-cli/internal/cmd"
	"github.com/MKITConsulting/zensu-cli/internal/update"
	"github.com/MKITConsulting/zensu-cli/internal/version"
)

func main() {
	root := cmd.NewRootCmd()
	notice, cancel := update.Start(version.Version)
	err := root.Execute()
	update.Finish(notice, os.Stderr)
	cancel()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
