package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/MKITConsulting/zensu-cli/internal/cmd"
)

const (
	schemaVersion  = 1
	generator      = "go run ./internal/tools/cmdtree"
	argsProbeLimit = 64
)

type contract struct {
	SchemaVersion int                    `json:"schemaVersion"`
	Generator     string                 `json:"generator"`
	Root          string                 `json:"root"`
	Commands      map[string]commandSpec `json:"commands"`
}

type commandSpec struct {
	Use         string              `json:"use"`
	Aliases     []string            `json:"aliases"`
	Subcommands []string            `json:"subcommands"`
	Runnable    bool                `json:"runnable"`
	Hidden      bool                `json:"hidden"`
	Deprecated  bool                `json:"deprecated"`
	Args        argCounts           `json:"args"`
	Flags       map[string]flagSpec `json:"flags"`
}

type argCounts struct {
	Min int  `json:"min"`
	Max *int `json:"max"`
}

type flagSpec struct {
	Shorthand  string `json:"shorthand"`
	TakesValue bool   `json:"takesValue"`
	Required   bool   `json:"required"`
	Persistent bool   `json:"persistent"`
	Hidden     bool   `json:"hidden"`
	Deprecated bool   `json:"deprecated"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(argv []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("cmdtree", flag.ContinueOnError)
	out := flags.String("out", filepath.Join("docs", "commands.json"), "file to write the command tree to")
	if err := flags.Parse(argv); err != nil {
		return err
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("unexpected arguments %q; usage: cmdtree [-out path]", flags.Args())
	}
	data, err := generate()
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "wrote %s\n", *out)
	return err
}

func generate() ([]byte, error) {
	tree, err := describe(cmd.NewRootCmd())
	if err != nil {
		return nil, err
	}
	return encode(tree)
}

func describe(root *cobra.Command) (contract, error) {
	root.InitDefaultHelpCmd()
	tree := contract{
		SchemaVersion: schemaVersion,
		Generator:     generator,
		Root:          root.CommandPath(),
		Commands:      map[string]commandSpec{},
	}
	var errs []error
	cmd.VisitCommands(root, func(c *cobra.Command) {
		c.InitDefaultHelpFlag()
		c.InitDefaultVersionFlag()
		counts, err := acceptedArgs(c)
		if err != nil {
			errs = append(errs, err)
			return
		}
		tree.Commands[c.CommandPath()] = commandSpec{
			Use:         c.Use,
			Aliases:     sortedCopy(c.Aliases),
			Subcommands: subcommandNames(c),
			Runnable:    c.Runnable(),
			Hidden:      c.Hidden,
			Deprecated:  c.Deprecated != "",
			Args:        counts,
			Flags:       describeFlags(c),
		}
	})
	return tree, errors.Join(errs...)
}

func acceptedArgs(c *cobra.Command) (argCounts, error) {
	if !c.Runnable() {
		none := 0
		return argCounts{Max: &none}, nil
	}
	var accepted []int
	for n := 0; n <= argsProbeLimit; n++ {
		if c.ValidateArgs(slices.Repeat([]string{cmd.PlaceholderArg}, n)) == nil {
			accepted = append(accepted, n)
		}
	}
	if len(accepted) == 0 {
		return argCounts{}, fmt.Errorf("%q accepts no positional argument count from 0 to %d when every argument is %q", c.CommandPath(), argsProbeLimit, cmd.PlaceholderArg)
	}
	lowest, highest := accepted[0], accepted[len(accepted)-1]
	if highest-lowest+1 != len(accepted) {
		return argCounts{}, fmt.Errorf("%q accepts the positional argument counts %v, which commands.json cannot express as a range", c.CommandPath(), accepted)
	}
	if highest == argsProbeLimit {
		return argCounts{Min: lowest}, nil
	}
	return argCounts{Min: lowest, Max: &highest}, nil
}

func describeFlags(c *cobra.Command) map[string]flagSpec {
	flags := map[string]flagSpec{}
	c.LocalFlags().VisitAll(func(f *pflag.Flag) {
		flags[f.Name] = describeFlag(f, c.PersistentFlags().Lookup(f.Name) == f)
	})
	c.InheritedFlags().VisitAll(func(f *pflag.Flag) {
		flags[f.Name] = describeFlag(f, true)
	})
	return flags
}

func describeFlag(f *pflag.Flag, persistent bool) flagSpec {
	return flagSpec{
		Shorthand:  f.Shorthand,
		TakesValue: f.NoOptDefVal == "",
		Required:   cmd.IsRequiredFlag(f),
		Persistent: persistent,
		Hidden:     f.Hidden,
		Deprecated: f.Deprecated != "",
	}
}

func subcommandNames(c *cobra.Command) []string {
	names := []string{}
	for _, child := range c.Commands() {
		names = append(names, child.Name())
	}
	slices.Sort(names)
	return names
}

func sortedCopy(values []string) []string {
	sorted := append([]string{}, values...)
	slices.Sort(sorted)
	return sorted
}

func encode(tree contract) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(tree); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
