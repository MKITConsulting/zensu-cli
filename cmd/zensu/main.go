package main

import (
	"fmt"
	"os"

	"github.com/MKITConsulting/zensu-cli/internal/cmd"
)

func main() {
	if err := cmd.NewRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
