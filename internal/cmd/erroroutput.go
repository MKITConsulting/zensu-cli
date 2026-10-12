package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

type flagParseError struct{ err error }

func (e *flagParseError) Error() string { return e.err.Error() }

func (e *flagParseError) Unwrap() error { return e.err }

func markFlagParseError(_ *cobra.Command, err error) error {
	return &flagParseError{err: err}
}

type errorLine struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Status  int    `json:"status,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

func WriteError(w io.Writer, ran *cobra.Command, args []string, err error) {
	if err == nil {
		return
	}
	text := ErrorText(err)
	fmt.Fprintln(w, "error:", text)
	if !jsonErrorRequested(ran, args, err) {
		return
	}
	detail := errorDetail{Message: text}
	if e, ok := apiErrorOf(err); ok {
		detail.Status = e.status
		detail.Code = e.printableCode()
		detail.Message = strings.ReplaceAll(text, e.text, e.plain)
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(errorLine{Error: detail})
}

func jsonErrorRequested(ran *cobra.Command, args []string, err error) bool {
	if ran == nil {
		return false
	}
	flag := ran.Flags().Lookup("json")
	if flag == nil {
		return false
	}
	if on, _ := strconv.ParseBool(flag.Value.String()); on {
		return true
	}
	var parseErr *flagParseError
	return errors.As(err, &parseErr) && argsRequestJSON(args)
}

func argsRequestJSON(args []string) bool {
	requested := false
	for _, a := range args {
		if a == "--" {
			break
		}
		if a == "--json" {
			requested = true
			continue
		}
		if value, ok := strings.CutPrefix(a, "--json="); ok {
			if on, err := strconv.ParseBool(value); err == nil {
				requested = on
			}
		}
	}
	return requested
}
