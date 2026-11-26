package cmd

import (
	"fmt"
	"net/http"

	"github.com/spf13/cobra"
)

func aiWorkStatusWork(cmd *cobra.Command, args []string, session, apiURL string) (string, error) {
	switch {
	case len(args) == 1 && session != "":
		return "", fmt.Errorf("pass a work id or --session, not both")
	case len(args) == 1:
		return args[0], nil
	case session == "":
		return "", fmt.Errorf("pass a work id, or --session to read the work of this worktree's capture receipt")
	case !captureSessionPattern.MatchString(session):
		return "", fmt.Errorf("--session must be the exact plugin-provided capture key")
	}
	captureDir, err := aiWorkCaptureDir(cmd.Context())
	if err != nil {
		return "", err
	}
	workID, skip, err := aiWorkReceiptWork(captureDir, session, apiURL)
	if err != nil {
		return "", err
	}
	switch skip {
	case aiWorkSkipNoWork:
		return "", fmt.Errorf("this session has no capture receipt in this worktree; pass a work id")
	case aiWorkSkipInvalid:
		return "", fmt.Errorf("this session's capture receipt cannot be read; pass a work id")
	case aiWorkSkipHostMismatch:
		return "", fmt.Errorf("this session's capture receipt belongs to another API host; pass a work id of this host")
	}
	return workID, nil
}

func newAIWorkStatusCmd(f *Factory) *cobra.Command {
	var session string
	cmd := jsonByDefault(&cobra.Command{
		Use:   "status [<work-id>]",
		Short: "Read where a piece of work stands: its stations, whose turn it is and the last signal",
		Long: "Read the status the server derives for a piece of work: nine stations with their state and source, the current station, whose turn it is and why, the last signal, what the sessions reported and the newest facts. " +
			"The status is derived on every read and is never set by a client. A station a session reported stays reported; only CI, a forge or a person in the browser confirms one.\n\n" +
			"Pass the work id, or --session to read the work this worktree's capture receipt names. Only the work's owner reads it through the CLI; everyone else gets the server's not_found. " +
			"Against a server without the status route the command fails with the error code unsupported.",
		Args: aiWorkOptionalIDArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := aiWorkClient(f, cmd)
			if err != nil {
				return err
			}
			workID, err := aiWorkStatusWork(cmd, args, session, c.BaseURL)
			if err != nil {
				return err
			}
			raw, err := readResponse(c.Do(cmd.Context(), http.MethodGet, aiWorkUnitPath(workID)+"/status", nil))
			if err == nil {
				return printJSON(f.Out, raw)
			}
			api, ok := apiErrorOf(err)
			if !ok || (api.status != http.StatusNotFound && api.status != http.StatusMethodNotAllowed) {
				return err
			}
			verdict, probeErr := aiWorkProbeRoute(cmd.Context(), c, workID, true)
			switch {
			case verdict == aiWorkRouteMissing:
				return &codedError{code: aiWorkCodeUnsupported, message: "this server has no work status route yet; update the server to read the status of AI work"}
			case verdict == aiWorkWorkUnavailable && probeErr == nil:
				return &codedError{code: "not_found", message: "work not available: the status of a piece of work is read only by its owner"}
			}
			return probeErr
		},
	})
	cmd.Flags().StringVar(&session, "session", "", "exact capture key supplied by the plugin; reads the work of this worktree's capture receipt instead of <work-id>")
	return cmd
}
