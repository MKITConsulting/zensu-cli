package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const (
	aiWorkSkipNoWork       = "no_active_work"
	aiWorkSkipInvalid      = "invalid_receipt"
	aiWorkSkipLegacy       = "legacy_scope"
	aiWorkSkipPaused       = "capture_paused"
	aiWorkSkipHostMismatch = "host_mismatch"

	progressDefaultTimeout = 8 * time.Second
	progressMinTimeout     = time.Second
	progressMaxTimeout     = time.Minute
)

func aiWorkOptionalIDArgs(cmd *cobra.Command, args []string) error {
	if err := cobra.MaximumNArgs(1)(cmd, args); err != nil {
		return err
	}
	if len(args) == 1 {
		return aiWorkUUID(args[0])
	}
	return nil
}

func aiWorkCaptureDir(ctx context.Context) (string, error) {
	gitDir, err := aiWorkGit(ctx, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", errors.New("a Git worktree is required: the capture receipts and the progress queue live in its Git directory")
	}
	return filepath.Join(gitDir, aiWorkCaptureDirName), nil
}

func aiWorkReceiptWork(captureDir, session, apiURL string) (string, string, error) {
	if err := captureDirectory(captureDir, false); err != nil {
		return "", "", err
	}
	raw, err := captureRead(filepath.Join(captureDir, session+".json"))
	if err != nil {
		return "", "", err
	}
	if raw == nil {
		return "", aiWorkSkipNoWork, nil
	}
	info := captureDescribe(raw)
	var receipt struct {
		APIURL string `json:"apiUrl"`
		Paused bool   `json:"capturePaused"`
	}
	switch {
	case info.Reason == "invalid_receipt" || json.Unmarshal(raw, &receipt) != nil:
		return "", aiWorkSkipInvalid, nil
	case receipt.APIURL != apiURL:
		return info.WorkID, aiWorkSkipHostMismatch, nil
	case receipt.Paused:
		return info.WorkID, aiWorkSkipPaused, nil
	case strings.HasPrefix(info.Reason, "legacy_"):
		return info.WorkID, aiWorkSkipLegacy, nil
	}
	return info.WorkID, "", nil
}

func newAIWorkReportCmd(f *Factory) *cobra.Command {
	var session, kind string
	var batch bool
	var timeout time.Duration
	var values progressFlagValues
	cmd := jsonByDefault(&cobra.Command{
		Use:   "report [<work-id>]",
		Short: "Report progress facts for a piece of work; never accepts or decides anything",
		Long: "Report closed-vocabulary progress facts for a piece of work: one fact from --kind and its flags, or up to 100 facts as JSON lines on stdin with --batch. " +
			"Without <work-id> the work is the one this worktree's capture receipt of --session names; then nothing is reported while capture is paused or no goal is active.\n\n" +
			"A queue in the worktree's Git directory owns the ids, sends one request at a time per work, repeats an unanswered batch unchanged and keeps what it could not send for the next call; a call that delivered its own facts also sends what another work of the worktree left behind. " +
			"The result is JSON with status sent, queued, merged, unsupported or skipped, all with exit code 0; unsupported means the server has no progress route yet. " +
			"Exit code 1 is left to invalid input, a missing personal login, a local failure such as a missing Git worktree and a refusal of the whole work.\n\n" +
			"A fact is a session's statement and confirms nothing. Only the flags' values leave the workstation: no prompt, answer, question text, command, code, diff or file path has a field, and the capture key is never sent.",
		Args: aiWorkOptionalIDArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !captureSessionPattern.MatchString(session) {
				return fmt.Errorf("--session must be the exact plugin-provided capture key")
			}
			if timeout < progressMinTimeout || timeout > progressMaxTimeout {
				return fmt.Errorf("--timeout must be between 1s and 60s")
			}
			var facts []progressFact
			if batch {
				if cmd.Flags().Changed("kind") {
					return fmt.Errorf("--batch reads its facts from stdin; drop --kind")
				}
				for _, name := range progressPayloadFlagNames() {
					if cmd.Flags().Changed(name) {
						return fmt.Errorf("--batch reads its facts from stdin; drop --%s", name)
					}
				}
				read, err := readProgressBatch(cmd.InOrStdin())
				if err != nil {
					return err
				}
				facts = read
			} else {
				if kind == "" {
					return fmt.Errorf("--kind is required unless --batch reads the facts from stdin")
				}
				fact, err := progressFactFromFlags(kind, &values, cmd.Flags().Changed)
				if err != nil {
					return err
				}
				facts = []progressFact{fact}
			}
			c, err := aiWorkClient(f, cmd)
			if err != nil {
				return err
			}
			captureDir, err := aiWorkCaptureDir(cmd.Context())
			if err != nil {
				return err
			}
			report := progressReport{}
			if len(args) == 1 {
				report.WorkID = args[0]
			} else {
				workID, skip, err := aiWorkReceiptWork(captureDir, session, c.BaseURL)
				if err != nil {
					return err
				}
				report.WorkID = workID
				if skip != "" {
					report.Status, report.Reason = progressStatusSkipped, skip
					return printProgressReport(f, report)
				}
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			report, err = runProgressReport(ctx, c, newProgressQueue(captureDir, report.WorkID, c.BaseURL), progressSessionRef(session), facts)
			if err != nil {
				return err
			}
			return printProgressReport(f, report)
		},
	})
	flags := cmd.Flags()
	flags.StringVar(&session, "session", "", "exact capture key supplied by the plugin; names the reporting session and is never sent (required)")
	flags.StringVar(&kind, "kind", "", "fact kind: "+strings.Join(progressFactKinds, ", ")+" (required unless --batch)")
	flags.BoolVar(&batch, "batch", false, `read up to 100 facts from stdin, one JSON object per line: {"kind":"...","payload":{...}} (max 256 KiB)`)
	flags.DurationVar(&timeout, "timeout", progressDefaultTimeout, "time budget for sending, 1s to 60s; what is not sent in time stays queued")
	flags.StringVar(&values.harness, "harness", "", "session_started: "+strings.Join(progressHarnesses, ", "))
	flags.StringVar(&values.clientVersion, "client-version", "", "session_started: reporter version, at most 32 letters, digits, dots, underscores, plus signs or hyphens")
	flags.StringVar(&values.branch, "branch", "", "session_started: git branch name")
	flags.StringVar(&values.worktree, "worktree", "", "session_started: worktree label of at most 100 letters, digits, dots, underscores, plus signs or hyphens; never a path")
	flags.StringVar(&values.reason, "reason", "", "session_ended: "+strings.Join(progressSessionEndReasons, ", ")+"; work_ended: "+strings.Join(progressWorkEndReasons, ", ")+"; blocked: one line of at most 120 characters (plain text)")
	flags.StringVar(&values.phase, "phase", "", "segment_started and segment_ended: "+strings.Join(progressPhases, ", "))
	flags.StringVar(&values.outcome, "outcome", "", "segment_ended: "+strings.Join(progressSegmentOutcomes, ", ")+"; wait_resolved: the outcome of its subject; evidence_reported: "+strings.Join(progressEvidenceOutcomes, ", "))
	flags.IntVar(&values.done, "done", 0, "step_progress: steps done, 0 to --total")
	flags.IntVar(&values.total, "total", 0, "step_progress: steps in all, 1 to 500")
	flags.StringVar(&values.label, "label", "", "step_progress: name of the current step, one line of at most 120 characters (plain text)")
	flags.StringVar(&values.subject, "subject", "", "wait_requested and wait_resolved: "+strings.Join(progressWaitSubjects, ", "))
	flags.IntVar(&values.round, "round", 0, "review_round_started and review_round_ended: review round, 1 to 50")
	flags.IntVar(&values.findingsOpen, "findings-open", 0, "review_round_ended: findings still open, 0 when left out")
	flags.IntVar(&values.findingsFixed, "findings-fixed", 0, "review_round_ended: findings fixed, 0 when left out")
	flags.IntVar(&values.findingsBlocking, "findings-blocking", 0, "review_round_ended: open findings that block, at most --findings-open, 0 when left out")
	flags.StringArrayVar(&values.findings, "finding", nil, "review_round_ended: one finding as <severity>:<state>:<title>, repeatable up to 20 times; the title is one line of at most 120 characters (plain text)")
	flags.BoolVar(&values.chainClosed, "chain-closed", false, "review_round_ended: the review chain closed with this round")
	flags.BoolVar(&values.limitReached, "limit-reached", false, "review_round_ended: the round limit was reached")
	flags.StringVar(&values.repository, "repository", "", "change_request_linked: owner and name, for example owner/repo")
	flags.IntVar(&values.number, "number", 0, "change_request_linked: change request number")
	flags.StringVar(&values.state, "state", "", "change_request_linked: "+strings.Join(progressChangeRequestStates, ", ")+" (open when left out)")
	flags.StringVar(&values.url, "url", "", "change_request_linked: https address without credentials, query or fragment")
	flags.IntVar(&values.commitsAhead, "commits-ahead", 0, "workspace_state: commits ahead of the base, 0 when left out")
	flags.IntVar(&values.filesChanged, "files-changed", 0, "workspace_state: files changed, 0 when left out")
	flags.IntVar(&values.uncommittedFiles, "uncommitted-files", 0, "workspace_state: files with uncommitted changes, 0 when left out")
	flags.StringVar(&values.head, "head", "", "workspace_state: commit hash of the head, 7 to 64 hexadecimal characters")
	flags.StringVar(&values.check, "check", "", "evidence_reported: "+strings.Join(progressEvidenceChecks, ", "))
	flags.IntVar(&values.passed, "passed", 0, "evidence_reported: passed count")
	flags.IntVar(&values.failed, "failed", 0, "evidence_reported: failed count")
	flags.Float64Var(&values.percent, "percent", 0, "evidence_reported: percentage, 0 to 100")
	flags.StringVar(&values.blockKind, "block-kind", "", "blocked: "+strings.Join(progressBlockKinds, ", "))
	flags.StringVar(&values.activity, "activity", "", "signal: "+strings.Join(progressActivities, ", "))
	return cmd
}

func printProgressReport(f *Factory, report progressReport) error {
	raw, err := json.Marshal(report)
	if err != nil {
		return err
	}
	return printJSON(f.Out, raw)
}
