package main

import (
	"os"

	"github.com/MKITConsulting/zensu-cli/internal/cmd"
	"github.com/MKITConsulting/zensu-cli/internal/update"
	"github.com/MKITConsulting/zensu-cli/internal/version"
)

func main() {
	root := cmd.NewRootCmd()
	notice, cancel := update.Start(version.Version)
	ran, err := root.ExecuteC()
	update.Finish(notice, os.Stderr)
	cancel()
	if err != nil {
		cmd.WriteError(os.Stderr, ran, os.Args[1:], err)
		os.Exit(1)
	}
}
