package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/MKITConsulting/zensu-cli/internal/client"
	"github.com/MKITConsulting/zensu-cli/internal/version"
)

const (
	workClientName    = "zensu-cli"
	maxFollowupPaths  = 20
	maxWorkClaimWait  = 50
	allowAgentKeyHint = "zensu work policy set --product <product id> --add-allowed-key <key id>"
)

var followupSeverities = map[string]bool{"low": true, "medium": true, "high": true, "critical": true}

var workSessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{8,128}$`)

var workClaimTimeoutMargin = 15 * time.Second

type workCredential int

const (
	agentKeyCredential workCredential = iota
	sessionTokenCredential
)

type workOrderView struct {
	ID              string  `json:"id"`
	WorkPlanID      string  `json:"work_plan_id"`
	Repository      string  `json:"repository"`
	Kind            string  `json:"kind"`
	Status          string  `json:"status"`
	BlockedKind     *string `json:"blocked_kind"`
	BlockedReason   *string `json:"blocked_reason"`
	Attempt         int     `json:"attempt"`
	ClientSessionID *string `json:"client_session_id"`
	PRURL           *string `json:"pr_url"`
}

type workQuestionView struct {
	ID          string  `json:"id"`
	WorkOrderID *string `json:"work_order_id"`
	Category    string  `json:"category"`
	Question    string  `json:"question"`
	Blocking    bool    `json:"blocking"`
	Status      string  `json:"status"`
	Answer      *string `json:"answer"`
	Scope       *string `json:"scope"`
}

func strValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func newClientEventID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "cli-" + hex.EncodeToString(b), nil
}

func eventIdentity(clientEventID string) (string, error) {
	if clientEventID != "" {
		return clientEventID, nil
	}
	return newClientEventID()
}

func workPath(id string, rest ...string) string {
	p := "/api/work-orders/" + url.PathEscape(id)
	for _, r := range rest {
		p += "/" + r
	}
	return p
}

func workBody(payload map[string]any) ([]byte, error) {
	return json.Marshal(payload)
}

func (f *Factory) postJSON(ctx context.Context, path string, payload map[string]any) ([]byte, error) {
	var body []byte
	if payload != nil {
		b, err := workBody(payload)
		if err != nil {
			return nil, err
		}
		body = b
	}
	return f.request(ctx, http.MethodPost, path, body)
}

func (f *Factory) workClient(ctx context.Context, verb string, want workCredential) (*client.Client, error) {
	c, err := f.NewClient(ctx)
	if err != nil {
		return nil, err
	}
	session := c.AuthMode() == client.AuthModeSessionToken
	if want == sessionTokenCredential && !session {
		return nil, fmt.Errorf("zensu %s runs inside a work order session; set %s to the session token of its claim", verb, sessionTokenEnv)
	}
	if want == agentKeyCredential && session {
		return nil, fmt.Errorf("zensu %s needs the agent key; unset %s, which replaces the stored login", verb, sessionTokenEnv)
	}
	return c, nil
}

func sendWorkJSON(ctx context.Context, c *client.Client, method, path string, payload map[string]any) ([]byte, error) {
	if payload == nil {
		return readResponse(c.Do(ctx, method, path, nil))
	}
	body, err := workBody(payload)
	if err != nil {
		return nil, err
	}
	return readResponse(c.Do(ctx, method, path, body))
}

func workClaimTimeout(wait int) time.Duration {
	return time.Duration(wait)*time.Second + workClaimTimeoutMargin
}

func workClientTimedOut(ctx context.Context, err error) bool {
	var netErr net.Error
	return ctx.Err() == nil && errors.As(err, &netErr) && netErr.Timeout()
}

func requireWorkSessionID(sessionID string) error {
	if sessionID == "" {
		return fmt.Errorf("--session-id is required")
	}
	if !workSessionIDPattern.MatchString(sessionID) {
		return fmt.Errorf("--session-id must be 8 to 128 characters of letters, digits, dot, underscore, colon or dash, got %q", sanitizeTerminal(sessionID))
	}
	return nil
}

type workUUIDFlag struct {
	name, value string
}

func requireWorkUUIDs(flags ...workUUIDFlag) error {
	for _, fl := range flags {
		if fl.value == "" {
			continue
		}
		if err := requireUUIDFlag(fl.name, fl.value); err != nil {
			return err
		}
	}
	return nil
}

func requireWorkProduct(product string) error {
	if product == "" {
		return fmt.Errorf("--product is required")
	}
	return requireUUIDFlag("product", product)
}

func decodeOrder(raw []byte, key string) (workOrderView, error) {
	var o workOrderView
	if key == "" {
		return o, json.Unmarshal(raw, &o)
	}
	var env map[string]json.RawMessage
	if err := json.Unmarshal(raw, &env); err != nil {
		return o, err
	}
	return o, json.Unmarshal(env[key], &o)
}

func printOrderStatus(f *Factory, raw []byte, key, verb string) error {
	o, err := decodeOrder(raw, key)
	if err != nil {
		return fmt.Errorf("invalid work order response: %w", err)
	}
	line := fmt.Sprintf("%s work order %s: %s", verb, o.ID, o.Status)
	if o.BlockedKind != nil {
		line += " (" + *o.BlockedKind + ")"
	}
	_, err = fmt.Fprintln(f.Out, sanitizeTerminal(line))
	return err
}

func resolveFeatureRef(ctx context.Context, f *Factory, ref string) (string, error) {
	key, ok := canonicalFeatureKey(ref)
	if !ok {
		if err := requireUUIDFlag("feature", ref); err != nil {
			return "", fmt.Errorf("--feature must be a KEY-N reference or a UUID, got %q", sanitizeTerminal(ref))
		}
		return ref, nil
	}
	raw, err := f.request(ctx, http.MethodGet, "/api/features/resolve?ref="+url.QueryEscape(key), nil)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", key, err)
	}
	var feature struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &feature); err != nil || feature.ID == "" {
		return "", fmt.Errorf("resolve %s: unexpected response", key)
	}
	return feature.ID, nil
}

func NewWorkCmd(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "work",
		Short: "Dispatch, run and decide work orders",
		Long: "Work orders hand a feature revision to an autonomous coding session.\n\n" +
			"Human verbs (create, approve, requeue, cancel, confirm-merge, answer, overturn, policy set,\n" +
			"repositories) need the browser login of `zensu auth login`; API keys get 403.\n" +
			"Worker verbs (claim, confirm, release, usage) need an agent key and refuse to run while\n" +
			"ZENSU_SESSION_TOKEN is set. Session verbs (heartbeat, event, ask, followup) authenticate\n" +
			"with ZENSU_SESSION_TOKEN, which overrides any stored login, and refuse to run without it.",
	}
	cmd.AddCommand(
		newWorkCreateCmd(f),
		newWorkListCmd(f),
		newWorkGetCmd(f),
		newWorkEventsCmd(f),
		newWorkHumanCmd(f, "approve", "Approve a draft work order (or the implementation plan it posted)", "approve", "Approved"),
		newWorkHumanCmd(f, "requeue", "Requeue a blocked work order", "requeue", "Requeued"),
		newWorkCancelCmd(f),
		newWorkConfirmMergeCmd(f),
		newWorkClaimCmd(f),
		newWorkConfirmCmd(f),
		newWorkReleaseCmd(f),
		newWorkUsageCmd(f),
		newWorkHeartbeatCmd(f),
		newWorkEventCmd(f),
		newWorkAskCmd(f),
		newWorkFollowupCmd(f),
		newWorkAnswerCmd(f, "answer", "Answer a question a session asked", false),
		newWorkAnswerCmd(f, "overturn", "Replace an automatic answer and revoke the category's autonomy for the plan", true),
		newWorkQuestionsCmd(f),
		newWorkPolicyCmd(f),
		newWorkRepositoriesCmd(f),
	)
	return cmd
}

func newWorkCreateCmd(f *Factory) *cobra.Command {
	var product, feature, revision, repository, baseBranch, harness, task string
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "create",
		Short:        "Create a one-item plan with one draft work order for a feature or revision",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if product == "" || repository == "" || (feature == "") == (revision == "") {
				return fmt.Errorf("--product, --repository and exactly one of --feature or --revision are required")
			}
			if err := requireWorkUUIDs(workUUIDFlag{"product", product}, workUUIDFlag{"revision", revision}, workUUIDFlag{"task", task}); err != nil {
				return err
			}
			payload := map[string]any{"repository": repository, "createdVia": "cli"}
			if feature != "" {
				id, err := resolveFeatureRef(cmd.Context(), f, feature)
				if err != nil {
					return err
				}
				payload["featureId"] = id
			} else {
				payload["revisionId"] = revision
			}
			if baseBranch != "" {
				payload["baseBranch"] = baseBranch
			}
			if harness != "" {
				payload["preferredHarness"] = harness
			}
			if task != "" {
				payload["taskId"] = task
			}
			raw, err := f.postJSON(cmd.Context(), "/api/products/"+url.PathEscape(product)+"/work-orders", payload)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var plan struct {
				ID      string          `json:"id"`
				Status  string          `json:"status"`
				Created bool            `json:"created"`
				Orders  []workOrderView `json:"orders"`
			}
			if err := json.Unmarshal(raw, &plan); err != nil {
				return fmt.Errorf("invalid work plan response: %w", err)
			}
			verb := "Created"
			if !plan.Created {
				verb = "Reused live"
			}
			for _, o := range plan.Orders {
				if _, err := fmt.Fprintln(f.Out, sanitizeTerminal(fmt.Sprintf("%s plan %s with work order %s (%s)", verb, plan.ID, o.ID, o.Status))); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&product, "product", "", "product ID (required)")
	cmd.Flags().StringVar(&feature, "feature", "", "feature ID or KEY-N reference; the order targets its active revision")
	cmd.Flags().StringVar(&revision, "revision", "", "feature revision ID")
	cmd.Flags().StringVar(&repository, "repository", "", "registered repository URL (required)")
	cmd.Flags().StringVar(&baseBranch, "base-branch", "", "base branch (default main)")
	cmd.Flags().StringVar(&harness, "harness", "", "preferred harness, e.g. claude")
	cmd.Flags().StringVar(&task, "task", "", "task ID to link")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newWorkListCmd(f *Factory) *cobra.Command {
	var product, plan, feature, status string
	var page, perPage int
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "list",
		Short:        "List work orders",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireWorkUUIDs(workUUIDFlag{"product", product}, workUUIDFlag{"plan", plan}); err != nil {
				return err
			}
			q := url.Values{}
			for k, v := range map[string]string{"product": product, "plan": plan, "status": status} {
				if v != "" {
					q.Set(k, v)
				}
			}
			if feature != "" {
				id, err := resolveFeatureRef(cmd.Context(), f, feature)
				if err != nil {
					return err
				}
				q.Set("feature", id)
			}
			if page > 0 {
				q.Set("page", strconv.Itoa(page))
			}
			if perPage > 0 {
				q.Set("per_page", strconv.Itoa(perPage))
			}
			path := "/api/work-orders"
			if len(q) > 0 {
				path += "?" + q.Encode()
			}
			raw, err := f.request(cmd.Context(), http.MethodGet, path, nil)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var env struct {
				Data  []workOrderView `json:"data"`
				Total int             `json:"total"`
			}
			if err := json.Unmarshal(raw, &env); err != nil {
				return fmt.Errorf("invalid work order list: %w", err)
			}
			tw := tabwriter.NewWriter(f.Out, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tSTATUS\tATTEMPT\tREPOSITORY\tPR")
			for _, o := range env.Data {
				st := o.Status
				if o.BlockedKind != nil {
					st += "(" + *o.BlockedKind + ")"
				}
				fmt.Fprintln(tw, tableRow(o.ID, st, strconv.Itoa(o.Attempt), o.Repository, strValue(o.PRURL)))
			}
			fmt.Fprintf(tw, "%d of %d\n", len(env.Data), env.Total)
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&product, "product", "", "filter by product ID")
	cmd.Flags().StringVar(&plan, "plan", "", "filter by work plan ID")
	cmd.Flags().StringVar(&feature, "feature", "", "filter by feature ID or KEY-N reference")
	cmd.Flags().StringVar(&status, "status", "", "filter by status (draft|queued|claimed|implementing|pr_open|ready_for_merge|blocked|merged|done|cancelled|...)")
	cmd.Flags().IntVar(&page, "page", 0, "page number")
	cmd.Flags().IntVar(&perPage, "per-page", 0, "items per page")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newWorkGetCmd(f *Factory) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "get <work-order-id>",
		Short:        "Show a work order with its spec, plan, policy, questions and lane",
		Long:         "Show a work order. Inside a session (ZENSU_SESSION_TOKEN) only the session's own order is readable.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := f.request(cmd.Context(), http.MethodGet, workPath(args[0]), nil)
			if err != nil {
				return err
			}
			return printJSON(f.Out, raw)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON (the default for this command)")
	return cmd
}

func newWorkEventsCmd(f *Factory) *cobra.Command {
	var page, perPage int
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "events <work-order-id>",
		Short:        "Show the event timeline of a work order",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			q := url.Values{}
			if page > 0 {
				q.Set("page", strconv.Itoa(page))
			}
			if perPage > 0 {
				q.Set("per_page", strconv.Itoa(perPage))
			}
			path := workPath(args[0], "events")
			if len(q) > 0 {
				path += "?" + q.Encode()
			}
			raw, err := f.request(cmd.Context(), http.MethodGet, path, nil)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var env struct {
				Data []struct {
					EventType  string  `json:"event_type"`
					FromStatus *string `json:"from_status"`
					ToStatus   *string `json:"to_status"`
					ActorKind  string  `json:"actor_kind"`
					Attempt    *int    `json:"attempt"`
					OccurredAt string  `json:"occurred_at"`
				} `json:"data"`
				Total int `json:"total"`
			}
			if err := json.Unmarshal(raw, &env); err != nil {
				return fmt.Errorf("invalid event list: %w", err)
			}
			tw := tabwriter.NewWriter(f.Out, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "OCCURRED\tTYPE\tFROM\tTO\tACTOR\tATTEMPT")
			for _, e := range env.Data {
				attempt := ""
				if e.Attempt != nil {
					attempt = strconv.Itoa(*e.Attempt)
				}
				fmt.Fprintln(tw, tableRow(e.OccurredAt, e.EventType, strValue(e.FromStatus), strValue(e.ToStatus), e.ActorKind, attempt))
			}
			fmt.Fprintf(tw, "%d of %d\n", len(env.Data), env.Total)
			return tw.Flush()
		},
	}
	cmd.Flags().IntVar(&page, "page", 0, "page number")
	cmd.Flags().IntVar(&perPage, "per-page", 0, "items per page")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newWorkHumanCmd(f *Factory, use, short, action, verb string) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:          use + " <work-order-id>",
		Short:        short,
		Long:         short + ". Needs the browser login of `zensu auth login`; API keys get 403.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := f.request(cmd.Context(), http.MethodPost, workPath(args[0], action), nil)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			return printOrderStatus(f, raw, "", verb)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newWorkCancelCmd(f *Factory) *cobra.Command {
	var confirmPRClosed, asJSON bool
	cmd := &cobra.Command{
		Use:          "cancel <work-order-id>",
		Short:        "Cancel a work order",
		Long:         "Cancel a work order. When the order has an open PR the backend refuses until --confirm-pr-closed confirms that the PR will be closed. Needs the browser login of `zensu auth login`.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := f.postJSON(cmd.Context(), workPath(args[0], "cancel"), map[string]any{"confirmPrClosed": confirmPRClosed})
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			return printOrderStatus(f, raw, "", "Cancelled")
		},
	}
	cmd.Flags().BoolVar(&confirmPRClosed, "confirm-pr-closed", false, "confirm that the order's open PR will be closed")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newWorkConfirmMergeCmd(f *Factory) *cobra.Command {
	var mergeSHA, headSHA string
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "confirm-merge <work-order-id>",
		Short:        "Confirm that the order's PR was merged (manual merge mode)",
		Long:         "Confirm that the order's PR was merged (manual merge mode). --head-sha names the merged PR head and must match the head the order recorded; --merge-sha records the merge commit when you know it. Needs the browser login of `zensu auth login`.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if headSHA == "" {
				return fmt.Errorf("--head-sha is required")
			}
			payload := map[string]any{"headSha": headSHA}
			if mergeSHA != "" {
				payload["mergeSha"] = mergeSHA
			}
			raw, err := f.postJSON(cmd.Context(), workPath(args[0], "confirm-merge"), payload)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			return printOrderStatus(f, raw, "", "Confirmed merge of")
		},
	}
	cmd.Flags().StringVar(&mergeSHA, "merge-sha", "", "merge commit SHA (optional)")
	cmd.Flags().StringVar(&headSHA, "head-sha", "", "full SHA of the merged PR head (required)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newWorkClaimCmd(f *Factory) *cobra.Command {
	var sessionID, product, clientName, clientVersion string
	var kinds, repositories []string
	var wait int
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "claim",
		Short: "Claim the next eligible work order (agent key)",
		Long: "Claim the next eligible work order. The call waits up to --wait seconds (0 to 50) for an order to become claimable; " +
			"when the connection times out, the claim is retried once with the same --session-id, which hands back the same claim. " +
			"The response carries the order-scoped session token; it is printed only with --json, for the session's environment (ZENSU_SESSION_TOKEN). " +
			"An agent key claims only in products whose automation policy allows it: " + allowAgentKeyHint + ".",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireWorkSessionID(sessionID); err != nil {
				return err
			}
			if len(repositories) == 0 {
				return fmt.Errorf("--repository is required; name every repository this worker can check out")
			}
			if wait < 0 || wait > maxWorkClaimWait {
				return fmt.Errorf("--wait must lie between 0 and %d seconds", maxWorkClaimWait)
			}
			if err := requireWorkUUIDs(workUUIDFlag{"product", product}); err != nil {
				return err
			}
			payload := map[string]any{
				"clientSessionId": sessionID,
				"kinds":           kinds,
				"repositories":    repositories,
				"waitSeconds":     wait,
				"clientName":      clientName,
				"clientVersion":   clientVersion,
			}
			if product != "" {
				payload["productId"] = product
			}
			ctx := cmd.Context()
			c, err := f.workClient(ctx, "work claim", agentKeyCredential)
			if err != nil {
				return err
			}
			c = c.WithTimeout(workClaimTimeout(wait))
			raw, err := sendWorkJSON(ctx, c, http.MethodPost, "/api/work-orders/claim", payload)
			if workClientTimedOut(ctx, err) {
				raw, err = sendWorkJSON(ctx, c, http.MethodPost, "/api/work-orders/claim", payload)
			}
			if err != nil {
				return err
			}
			if len(strings.TrimSpace(string(raw))) == 0 {
				if asJSON {
					return printJSON(f.Out, []byte(`{"order":null}`))
				}
				_, err := fmt.Fprintln(f.Out, "No work order became claimable. An agent key claims only in products whose automation policy allows it: "+allowAgentKeyHint+".")
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var claim struct {
				Order        workOrderView `json:"order"`
				LeaseSeconds int           `json:"lease_seconds"`
				Retried      bool          `json:"retried"`
			}
			if err := json.Unmarshal(raw, &claim); err != nil {
				return fmt.Errorf("invalid claim response: %w", err)
			}
			_, err = fmt.Fprintln(f.Out, sanitizeTerminal(fmt.Sprintf("Claimed work order %s attempt %d for %d s; confirm it once the session started. Use --json to read the session token.", claim.Order.ID, claim.Order.Attempt, claim.LeaseSeconds)))
			return err
		},
	}
	cmd.Flags().StringVar(&sessionID, "session-id", "", "client session ID of the session that will run the order (8-128 of A-Z a-z 0-9 . _ : -; required)")
	cmd.Flags().StringSliceVar(&kinds, "kind", []string{"implement"}, "order kinds this worker runs")
	cmd.Flags().StringSliceVar(&repositories, "repository", nil, "repositories this worker can check out (repeatable; required)")
	cmd.Flags().StringVar(&product, "product", "", "limit the claim to one product")
	cmd.Flags().IntVar(&wait, "wait", 0, "seconds to wait for a claimable order, 0 to 50 (long poll)")
	cmd.Flags().StringVar(&clientName, "client-name", workClientName, "client name reported with the claim")
	cmd.Flags().StringVar(&clientVersion, "client-version", version.Version, "client version reported with the claim")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON, including the session token")
	return cmd
}

func fencingFlags(cmd *cobra.Command, sessionID *string, attempt *int) {
	cmd.Flags().StringVar(sessionID, "session-id", "", "client session ID of the claim (8-128 of A-Z a-z 0-9 . _ : -; required)")
	cmd.Flags().IntVar(attempt, "attempt", 0, "attempt number of the claim (required)")
}

func requireFencing(sessionID string, attempt int) error {
	if sessionID == "" || attempt < 1 {
		return fmt.Errorf("--session-id and --attempt are required")
	}
	return requireWorkSessionID(sessionID)
}

func printLease(f *Factory, raw []byte, verb string) error {
	var lease struct {
		Order        workOrderView     `json:"order"`
		LeaseSeconds int               `json:"lease_seconds"`
		Answers      []json.RawMessage `json:"answers"`
	}
	if err := json.Unmarshal(raw, &lease); err != nil {
		return fmt.Errorf("invalid lease response: %w", err)
	}
	line := fmt.Sprintf("%s work order %s: %s, lease %d s", verb, lease.Order.ID, lease.Order.Status, lease.LeaseSeconds)
	if len(lease.Answers) > 0 {
		line += fmt.Sprintf(", %d answered question(s)", len(lease.Answers))
	}
	_, err := fmt.Fprintln(f.Out, sanitizeTerminal(line))
	return err
}

func newWorkConfirmCmd(f *Factory) *cobra.Command {
	var sessionID string
	var attempt int
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "confirm <work-order-id>",
		Short:        "Confirm a claim once the session started (agent key)",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireFencing(sessionID, attempt); err != nil {
				return err
			}
			c, err := f.workClient(cmd.Context(), "work confirm", agentKeyCredential)
			if err != nil {
				return err
			}
			raw, err := sendWorkJSON(cmd.Context(), c, http.MethodPost, workPath(args[0], "confirm"), map[string]any{"clientSessionId": sessionID, "attempt": attempt})
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			return printLease(f, raw, "Confirmed")
		},
	}
	fencingFlags(cmd, &sessionID, &attempt)
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newWorkReleaseCmd(f *Factory) *cobra.Command {
	var sessionID, outcome, notBefore, reason string
	var attempt int
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "release <work-order-id>",
		Short:        "Release a claim when the session exits (agent key)",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireFencing(sessionID, attempt); err != nil {
				return err
			}
			if outcome == "" {
				return fmt.Errorf("--outcome is required (success|failure|rate_limited|runtime_cap|interrupted)")
			}
			if outcome == "rate_limited" && notBefore == "" {
				return fmt.Errorf("--outcome rate_limited needs --not-before, the RFC 3339 time before which the order must not be claimed again")
			}
			if outcome != "rate_limited" && notBefore != "" {
				return fmt.Errorf("--not-before applies only to --outcome rate_limited")
			}
			payload := map[string]any{"clientSessionId": sessionID, "attempt": attempt, "outcome": outcome}
			if notBefore != "" {
				t, err := time.Parse(time.RFC3339, notBefore)
				if err != nil {
					return fmt.Errorf("--not-before must be an RFC 3339 timestamp: %w", err)
				}
				payload["notBefore"] = t.UTC().Format(time.RFC3339)
			}
			if reason != "" {
				payload["reason"] = reason
			}
			c, err := f.workClient(cmd.Context(), "work release", agentKeyCredential)
			if err != nil {
				return err
			}
			raw, err := sendWorkJSON(cmd.Context(), c, http.MethodPost, workPath(args[0], "release"), payload)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			return printOrderStatus(f, raw, "", "Released")
		},
	}
	fencingFlags(cmd, &sessionID, &attempt)
	cmd.Flags().StringVar(&outcome, "outcome", "", "session outcome: success|failure|rate_limited|runtime_cap|interrupted (required)")
	cmd.Flags().StringVar(&notBefore, "not-before", "", "RFC 3339 time before which the order must not be claimed again (required with --outcome rate_limited, refused with any other outcome)")
	cmd.Flags().StringVar(&reason, "reason", "", "short reason, prose only")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newWorkUsageCmd(f *Factory) *cobra.Command {
	var sessionID, clientEventID string
	var attempt int
	var costUSD float64
	var turns, durationSeconds, inputTokens, outputTokens int64
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "usage <work-order-id>",
		Short:        "Report the cost and token usage of a session (agent key)",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireFencing(sessionID, attempt); err != nil {
				return err
			}
			id, err := eventIdentity(clientEventID)
			if err != nil {
				return err
			}
			detail := map[string]any{}
			if cmd.Flags().Changed("cost-usd") {
				detail["costUsd"] = costUSD
			}
			for _, c := range []struct {
				flag, key string
				value     int64
			}{
				{"turns", "turns", turns},
				{"duration-seconds", "durationSeconds", durationSeconds},
				{"input-tokens", "inputTokens", inputTokens},
				{"output-tokens", "outputTokens", outputTokens},
			} {
				if cmd.Flags().Changed(c.flag) {
					detail[c.key] = c.value
				}
			}
			payload := map[string]any{
				"clientSessionId": sessionID,
				"attempt":         attempt,
				"clientEventId":   id,
				"occurredAt":      time.Now().UTC().Format(time.RFC3339Nano),
				"type":            "usage",
				"clientName":      workClientName,
				"clientVersion":   version.Version,
				"detail":          detail,
			}
			c, err := f.workClient(cmd.Context(), "work usage", agentKeyCredential)
			if err != nil {
				return err
			}
			raw, err := sendWorkJSON(cmd.Context(), c, http.MethodPost, workPath(args[0], "events"), payload)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			_, err = fmt.Fprintln(f.Out, sanitizeTerminal(fmt.Sprintf("Recorded usage for work order %s (event %s)", args[0], id)))
			return err
		},
	}
	fencingFlags(cmd, &sessionID, &attempt)
	cmd.Flags().StringVar(&clientEventID, "client-event-id", "", "idempotency key; a retry with the same id returns the stored event")
	cmd.Flags().Float64Var(&costUSD, "cost-usd", 0, "session cost in USD")
	cmd.Flags().Int64Var(&turns, "turns", 0, "number of model turns")
	cmd.Flags().Int64Var(&durationSeconds, "duration-seconds", 0, "session duration in seconds")
	cmd.Flags().Int64Var(&inputTokens, "input-tokens", 0, "input tokens")
	cmd.Flags().Int64Var(&outputTokens, "output-tokens", 0, "output tokens")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newWorkHeartbeatCmd(f *Factory) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "heartbeat <work-order-id>",
		Short:        "Renew the session's lease (ZENSU_SESSION_TOKEN)",
		Long:         "Renew the session's lease. The response carries the lease length, the recorded head and the answered questions. A 409 stale_attempt means another session holds the order: stop without further forge writes.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := f.workClient(cmd.Context(), "work heartbeat", sessionTokenCredential)
			if err != nil {
				return err
			}
			raw, err := sendWorkJSON(cmd.Context(), c, http.MethodPost, workPath(args[0], "heartbeat"), nil)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			return printLease(f, raw, "Renewed")
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

type workEventFlags struct {
	stage, artifact, blocked                             string
	prURL, branch, head, autopilotStage, runID           string
	prNumber                                             int
	url, path, sha, summary, reason, detail              string
	line                                                 int
	clientEventID, occurredAt, clientName, clientVersion string
}

func (e workEventFlags) payload(cmd *cobra.Command) (map[string]any, error) {
	set := 0
	for _, v := range []string{e.stage, e.artifact, e.blocked} {
		if v != "" {
			set++
		}
	}
	if set != 1 {
		return nil, fmt.Errorf("exactly one of --stage, --artifact or --blocked is required")
	}
	if e.stage != "" && e.detail != "" {
		return nil, fmt.Errorf("--detail does not apply to --stage events; stage events carry only their flags")
	}
	id, err := eventIdentity(e.clientEventID)
	if err != nil {
		return nil, err
	}
	occurred := time.Now().UTC()
	if e.occurredAt != "" {
		occurred, err = time.Parse(time.RFC3339, e.occurredAt)
		if err != nil {
			return nil, fmt.Errorf("--occurred-at must be an RFC 3339 timestamp: %w", err)
		}
	}
	p := map[string]any{
		"clientEventId": id,
		"occurredAt":    occurred.UTC().Format(time.RFC3339Nano),
		"clientName":    e.clientName,
		"clientVersion": e.clientVersion,
	}
	var detail map[string]any
	if e.detail != "" {
		if err := json.Unmarshal([]byte(e.detail), &detail); err != nil || detail == nil {
			return nil, fmt.Errorf("--detail must be a JSON object")
		}
	}
	switch {
	case e.stage != "":
		p["type"] = "stage"
		p["status"] = e.stage
		for k, v := range map[string]string{"prUrl": e.prURL, "branch": e.branch, "headSha": e.head, "autopilotStage": e.autopilotStage, "autopilotRunId": e.runID} {
			if v != "" {
				p[k] = v
			}
		}
		if cmd.Flags().Changed("pr-number") {
			p["prNumber"] = e.prNumber
		}
	case e.artifact != "":
		p["type"] = "artifact"
		if detail == nil {
			detail = map[string]any{}
		}
		detail["kind"] = e.artifact
		for k, v := range map[string]string{"url": e.url, "path": e.path, "sha": e.sha, "summary": e.summary} {
			if v != "" {
				detail[k] = v
			}
		}
		if cmd.Flags().Changed("line") {
			detail["line"] = e.line
		}
	default:
		p["type"] = "blocked"
		p["blockedKind"] = e.blocked
		if detail == nil {
			detail = map[string]any{}
		}
		if e.reason != "" {
			detail["reason"] = e.reason
		}
		if reason, _ := detail["reason"].(string); strings.TrimSpace(reason) == "" {
			return nil, fmt.Errorf("--blocked needs --reason")
		}
	}
	if detail != nil {
		p["detail"] = detail
	}
	return p, nil
}

func newWorkEventCmd(f *Factory) *cobra.Command {
	var e workEventFlags
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "event <work-order-id>",
		Short: "Report a stage transition, an artifact or a block (ZENSU_SESSION_TOKEN)",
		Long: "Report one event of the session. Exactly one of --stage, --artifact or --blocked selects the type:\n\n" +
			"  --stage planning|implementing|pr_open|reviewing|validating|ready_for_merge   with --pr-url, --pr-number, --branch, --head\n" +
			"  --artifact plan|report|evidence|pr|other                                       with --url or --path, --line, --sha, --summary\n" +
			"  --blocked plan_approval|worker_error                                           with --reason (required)\n\n" +
			"--detail adds raw JSON to artifact and blocked events; stage events refuse it. Every event carries a client event id;\n" +
			"pass --client-event-id to make retries idempotent. Only metadata and short prose are accepted, never code, commands,\n" +
			"logs or diffs.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			payload, err := e.payload(cmd)
			if err != nil {
				return err
			}
			c, err := f.workClient(cmd.Context(), "work event", sessionTokenCredential)
			if err != nil {
				return err
			}
			raw, err := sendWorkJSON(cmd.Context(), c, http.MethodPost, workPath(args[0], "events"), payload)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var res struct {
				Replay bool `json:"replay"`
			}
			_ = json.Unmarshal(raw, &res)
			o, err := decodeOrder(raw, "order")
			if err != nil {
				return fmt.Errorf("invalid event response: %w", err)
			}
			line := fmt.Sprintf("Recorded %s event %s; work order %s is %s", payload["type"], payload["clientEventId"], o.ID, o.Status)
			if res.Replay {
				line += " (replayed)"
			}
			_, err = fmt.Fprintln(f.Out, sanitizeTerminal(line))
			return err
		},
	}
	cmd.Flags().StringVar(&e.stage, "stage", "", "stage the order reached")
	cmd.Flags().StringVar(&e.artifact, "artifact", "", "artifact kind: plan|report|evidence|pr|other")
	cmd.Flags().StringVar(&e.blocked, "blocked", "", "block the order: plan_approval|worker_error")
	cmd.Flags().StringVar(&e.prURL, "pr-url", "", "PR link (stage events)")
	cmd.Flags().IntVar(&e.prNumber, "pr-number", 0, "PR number (stage events)")
	cmd.Flags().StringVar(&e.branch, "branch", "", "branch name (stage events)")
	cmd.Flags().StringVar(&e.head, "head", "", "full head SHA (stage events)")
	cmd.Flags().StringVar(&e.autopilotStage, "autopilot-stage", "", "Autopilot stage name (stage events)")
	cmd.Flags().StringVar(&e.runID, "autopilot-run-id", "", "Autopilot run ID (stage events)")
	cmd.Flags().StringVar(&e.url, "url", "", "artifact link")
	cmd.Flags().StringVar(&e.path, "path", "", "artifact path in the repository")
	cmd.Flags().IntVar(&e.line, "line", 0, "artifact line (needs --path)")
	cmd.Flags().StringVar(&e.sha, "sha", "", "artifact commit SHA")
	cmd.Flags().StringVar(&e.summary, "summary", "", "short artifact summary, prose only")
	cmd.Flags().StringVar(&e.reason, "reason", "", "block reason, prose only (required with --blocked)")
	cmd.Flags().StringVar(&e.detail, "detail", "", "raw JSON detail object for artifact and blocked events (not with --stage)")
	cmd.Flags().StringVar(&e.clientEventID, "client-event-id", "", "idempotency key (8-64 of A-Z a-z 0-9 . _ : -); generated when omitted")
	cmd.Flags().StringVar(&e.occurredAt, "occurred-at", "", "RFC 3339 time of the event (default now)")
	cmd.Flags().StringVar(&e.clientName, "client-name", workClientName, "client name reported with the event")
	cmd.Flags().StringVar(&e.clientVersion, "client-version", version.Version, "client version reported with the event")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newWorkAskCmd(f *Factory) *cobra.Command {
	var category, question, defaultOption, clientEventID string
	var options, requirements []string
	var blocking, asJSON bool
	cmd := &cobra.Command{
		Use:          "ask <work-order-id>",
		Short:        "Ask the humans behind the plan a question (ZENSU_SESSION_TOKEN)",
		Long:         "Ask a question instead of guessing. A non-blocking question names the option the session continues with (--default-option). A blocking question ends the session with blocked(question); a human answer requeues the order and the next session reads the answer from its heartbeat or from `zensu work get`.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if category == "" || question == "" {
				return fmt.Errorf("--category and --question are required")
			}
			if !blocking && defaultOption == "" {
				return fmt.Errorf("--default-option is required unless --blocking is set")
			}
			id, err := eventIdentity(clientEventID)
			if err != nil {
				return err
			}
			payload := map[string]any{
				"clientEventId": id,
				"occurredAt":    time.Now().UTC().Format(time.RFC3339Nano),
				"category":      category,
				"question":      question,
				"blocking":      blocking,
				"clientName":    workClientName,
				"clientVersion": version.Version,
			}
			if len(options) > 0 {
				payload["options"] = options
			}
			if defaultOption != "" {
				payload["defaultOption"] = defaultOption
			}
			if len(requirements) > 0 {
				payload["requirementIds"] = requirements
			}
			c, err := f.workClient(cmd.Context(), "work ask", sessionTokenCredential)
			if err != nil {
				return err
			}
			raw, err := sendWorkJSON(cmd.Context(), c, http.MethodPost, workPath(args[0], "questions"), payload)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var res struct {
				Question  workQuestionView `json:"question"`
				Order     workOrderView    `json:"order"`
				Escalated bool             `json:"escalated"`
				Replay    bool             `json:"replay"`
			}
			if err := json.Unmarshal(raw, &res); err != nil {
				return fmt.Errorf("invalid question response: %w", err)
			}
			line := fmt.Sprintf("Asked question %s (%s); work order %s is %s", res.Question.ID, res.Question.Status, res.Order.ID, res.Order.Status)
			if res.Escalated {
				line += "; escalated to a human"
			}
			if res.Order.Status == "blocked" {
				line += "; stop the session"
			}
			_, err = fmt.Fprintln(f.Out, sanitizeTerminal(line))
			return err
		},
	}
	cmd.Flags().StringVar(&category, "category", "", "clarification|technical_choice|product_decision|scope|risk|external (required)")
	cmd.Flags().StringVar(&question, "question", "", "the question, prose only (required)")
	cmd.Flags().StringArrayVar(&options, "option", nil, "an answer option (repeatable)")
	cmd.Flags().StringVar(&defaultOption, "default-option", "", "the option the session continues with (required unless --blocking)")
	cmd.Flags().BoolVar(&blocking, "blocking", false, "the session cannot continue without the answer")
	cmd.Flags().StringSliceVar(&requirements, "requirement", nil, "requirement IDs the question affects, e.g. AC-001")
	cmd.Flags().StringVar(&clientEventID, "client-event-id", "", "idempotency key; generated when omitted")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newWorkFollowupCmd(f *Factory) *cobra.Command {
	var title, rationale, severity, clientEventID string
	var paths []string
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "followup <work-order-id>",
		Short:        "Report work discovered outside the order's scope (ZENSU_SESSION_TOKEN)",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if title == "" || rationale == "" {
				return fmt.Errorf("--title and --rationale are required")
			}
			if !followupSeverities[severity] {
				return fmt.Errorf("--severity must be one of low, medium, high, critical")
			}
			if len(paths) > maxFollowupPaths {
				return fmt.Errorf("at most %d --path values", maxFollowupPaths)
			}
			for _, p := range paths {
				if !validPlanPath(p) {
					return fmt.Errorf("--path %q must be a clean relative file path without wildcards", p)
				}
			}
			id, err := eventIdentity(clientEventID)
			if err != nil {
				return err
			}
			payload := map[string]any{
				"clientEventId": id,
				"occurredAt":    time.Now().UTC().Format(time.RFC3339Nano),
				"title":         title,
				"rationale":     rationale,
				"severity":      severity,
				"clientName":    workClientName,
				"clientVersion": version.Version,
			}
			if len(paths) > 0 {
				payload["paths"] = paths
			}
			c, err := f.workClient(cmd.Context(), "work followup", sessionTokenCredential)
			if err != nil {
				return err
			}
			raw, err := sendWorkJSON(cmd.Context(), c, http.MethodPost, workPath(args[0], "followups"), payload)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			_, err = fmt.Fprintln(f.Out, sanitizeTerminal(fmt.Sprintf("Reported follow-up %q for work order %s (event %s)", title, args[0], id)))
			return err
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "short title, prose only (required)")
	cmd.Flags().StringVar(&rationale, "rationale", "", "why the work matters, prose only (required)")
	cmd.Flags().StringVar(&severity, "severity", "", "low|medium|high|critical (required)")
	cmd.Flags().StringArrayVar(&paths, "path", nil, "an affected repository file (repeatable, at most 20)")
	cmd.Flags().StringVar(&clientEventID, "client-event-id", "", "idempotency key; generated when omitted")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newWorkAnswerCmd(f *Factory, use, short string, overturn bool) *cobra.Command {
	var answer, rationale string
	var planWide, asJSON bool
	action := "answer"
	if overturn {
		action = "overturn"
	}
	cmd := &cobra.Command{
		Use:          use + " <question-id>",
		Short:        short,
		Long:         short + ". Needs the browser login of `zensu auth login`; API keys get 403. Answering a blocking question requeues its order.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if answer == "" {
				return fmt.Errorf("--answer is required")
			}
			payload := map[string]any{"answer": answer}
			if !overturn {
				payload["planWide"] = planWide
			}
			if rationale != "" {
				payload["rationale"] = rationale
			}
			raw, err := f.postJSON(cmd.Context(), "/api/work-questions/"+url.PathEscape(args[0])+"/"+action, payload)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var res struct {
				Question workQuestionView `json:"question"`
				Order    *workOrderView   `json:"order"`
				Requeued bool             `json:"requeued"`
			}
			if err := json.Unmarshal(raw, &res); err != nil {
				return fmt.Errorf("invalid answer response: %w", err)
			}
			line := fmt.Sprintf("Question %s is %s", res.Question.ID, res.Question.Status)
			if res.Order != nil {
				line += fmt.Sprintf("; work order %s is %s", res.Order.ID, res.Order.Status)
			}
			if res.Requeued {
				line += " (requeued)"
			}
			_, err = fmt.Fprintln(f.Out, sanitizeTerminal(line))
			return err
		},
	}
	cmd.Flags().StringVar(&answer, "answer", "", "the answer, prose only (required)")
	cmd.Flags().StringVar(&rationale, "rationale", "", "why, prose only")
	if !overturn {
		cmd.Flags().BoolVar(&planWide, "plan-wide", false, "the answer applies to every package of the plan")
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newWorkQuestionsCmd(f *Factory) *cobra.Command {
	var plan, order, status, scope string
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "questions",
		Short:        "List the questions of a work plan",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if plan == "" {
				return fmt.Errorf("--plan is required")
			}
			if err := requireWorkUUIDs(workUUIDFlag{"plan", plan}, workUUIDFlag{"order", order}); err != nil {
				return err
			}
			q := url.Values{}
			if status != "" {
				q.Set("status", status)
			}
			if scope != "" {
				q.Set("scope", scope)
			}
			path := "/api/work-plans/" + url.PathEscape(plan) + "/questions"
			if len(q) > 0 {
				path += "?" + q.Encode()
			}
			raw, err := f.request(cmd.Context(), http.MethodGet, path, nil)
			if err != nil {
				return err
			}
			if asJSON && order == "" {
				return printJSON(f.Out, raw)
			}
			var env struct {
				Data []json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(raw, &env); err != nil {
				return fmt.Errorf("invalid question list: %w", err)
			}
			kept := make([]json.RawMessage, 0, len(env.Data))
			list := make([]workQuestionView, 0, len(env.Data))
			for _, item := range env.Data {
				var qn workQuestionView
				if err := json.Unmarshal(item, &qn); err != nil {
					return fmt.Errorf("invalid question list: %w", err)
				}
				if order != "" && strValue(qn.WorkOrderID) != order {
					continue
				}
				kept = append(kept, item)
				list = append(list, qn)
			}
			if asJSON {
				b, err := json.Marshal(map[string]any{"data": kept})
				if err != nil {
					return err
				}
				return printJSON(f.Out, b)
			}
			tw := tabwriter.NewWriter(f.Out, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tSTATUS\tCATEGORY\tBLOCKING\tORDER\tQUESTION")
			for _, qn := range list {
				fmt.Fprintln(tw, tableRow(qn.ID, qn.Status, qn.Category, strconv.FormatBool(qn.Blocking), strValue(qn.WorkOrderID), qn.Question))
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&plan, "plan", "", "work plan ID (required)")
	cmd.Flags().StringVar(&order, "order", "", "only questions of this work order")
	cmd.Flags().StringVar(&status, "status", "", "filter by status (open|awaiting_human|answered|auto_answered|...)")
	cmd.Flags().StringVar(&scope, "scope", "", "filter by scope (package|plan)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newWorkPolicyCmd(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "policy",
		Short: "Show or change a product's automation policy",
	}
	cmd.AddCommand(newWorkPolicyGetCmd(f), newWorkPolicySetCmd(f))
	return cmd
}

func newWorkPolicyGetCmd(f *Factory) *cobra.Command {
	var product string
	cmd := &cobra.Command{
		Use:          "get",
		Short:        "Show the effective automation policy of a product",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireWorkProduct(product); err != nil {
				return err
			}
			raw, err := f.request(cmd.Context(), http.MethodGet, "/api/products/"+url.PathEscape(product)+"/automation-policy", nil)
			if err != nil {
				return err
			}
			return printJSON(f.Out, raw)
		},
	}
	cmd.Flags().StringVar(&product, "product", "", "product ID (required)")
	return cmd
}

type automationPolicy struct {
	DispatchMode           string   `json:"dispatch_mode"`
	PlanApproval           string   `json:"plan_approval"`
	MaxRunningOrders       int      `json:"max_running_orders"`
	MaxAttempts            int      `json:"max_attempts"`
	MaxUnconfirmedClaims   int      `json:"max_unconfirmed_claims"`
	LeaseMinutes           int      `json:"lease_minutes"`
	MaxOrderRuntimeMinutes int      `json:"max_order_runtime_minutes"`
	MaxOrderCostUSD        *float64 `json:"max_order_cost_usd"`
	DailyOrderCap          int      `json:"daily_order_cap"`
	AllowedAPIKeyIDs       []string `json:"allowed_api_key_ids"`
	MaxQuestionsPerOrder   int      `json:"max_questions_per_order"`
	QuestionReminderHours  int      `json:"question_reminder_hours"`
}

func editAllowedKeys(current, add, remove []string) []string {
	dropped := map[string]bool{}
	for _, key := range remove {
		dropped[strings.ToLower(key)] = true
	}
	kept := []string{}
	seen := map[string]bool{}
	for _, key := range append(append([]string{}, current...), add...) {
		k := strings.ToLower(key)
		if dropped[k] || seen[k] {
			continue
		}
		seen[k] = true
		kept = append(kept, key)
	}
	return kept
}

func requireAllowedKeyFlags(allowed, add, remove []string) error {
	for _, fl := range []struct {
		name string
		keys []string
	}{{"allowed-key", allowed}, {"add-allowed-key", add}, {"remove-allowed-key", remove}} {
		for _, key := range fl.keys {
			if err := requireUUIDFlag(fl.name, key); err != nil {
				return err
			}
		}
	}
	for _, a := range add {
		for _, r := range remove {
			if strings.EqualFold(a, r) {
				return fmt.Errorf("--add-allowed-key and --remove-allowed-key both name %s; name each key once", a)
			}
		}
	}
	return nil
}

func allowedKeySourceError(err error, changed func(string) bool) error {
	if e, ok := apiErrorOf(err); !ok || e.code != "invalid_agent_keys" {
		return err
	}
	for _, flag := range []string{"allowed-key", "add-allowed-key"} {
		if changed(flag) {
			return fmt.Errorf("%w; the refused keys came from --%s", err, flag)
		}
	}
	return err
}

func newWorkPolicySetCmd(f *Factory) *cobra.Command {
	var product, dispatchMode, planApproval string
	var maxRunning, maxAttempts, maxUnconfirmed, leaseMinutes, maxRuntime, dailyCap, maxQuestions, reminderHours int
	var maxCost float64
	var allowedKeys, addKeys, removeKeys []string
	var clearMaxCost, asJSON bool
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Change a product's automation policy; unset flags keep their current value",
		Long: "Change a product's automation policy. The command reads the effective policy, applies the given flags and writes the full policy back. " +
			"--allowed-key replaces the list of agent keys whose workers may claim; --add-allowed-key and --remove-allowed-key change the list the command just read and cannot be combined with --allowed-key. " +
			"The server drops stored keys that are no longer active and refuses newly named keys that are not active agent keys. " +
			"--clear-max-order-cost removes the cost cap of one order. Needs the browser login of `zensu auth login`.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireWorkProduct(product); err != nil {
				return err
			}
			if err := requireAllowedKeyFlags(allowedKeys, addKeys, removeKeys); err != nil {
				return err
			}
			path := "/api/products/" + url.PathEscape(product) + "/automation-policy"
			raw, err := f.request(cmd.Context(), http.MethodGet, path, nil)
			if err != nil {
				return err
			}
			var p automationPolicy
			if err := json.Unmarshal(raw, &p); err != nil {
				return fmt.Errorf("invalid policy response: %w", err)
			}
			changed := cmd.Flags().Changed
			if changed("dispatch-mode") {
				p.DispatchMode = dispatchMode
			}
			if changed("plan-approval") {
				p.PlanApproval = planApproval
			}
			for flag, dst := range map[string]*int{
				"max-running-orders": &p.MaxRunningOrders, "max-attempts": &p.MaxAttempts,
				"max-unconfirmed-claims": &p.MaxUnconfirmedClaims, "lease-minutes": &p.LeaseMinutes,
				"max-order-runtime-minutes": &p.MaxOrderRuntimeMinutes, "daily-order-cap": &p.DailyOrderCap,
				"max-questions-per-order": &p.MaxQuestionsPerOrder, "question-reminder-hours": &p.QuestionReminderHours,
			} {
				if changed(flag) {
					v, _ := cmd.Flags().GetInt(flag)
					*dst = v
				}
			}
			if changed("max-order-cost-usd") {
				p.MaxOrderCostUSD = &maxCost
			}
			if clearMaxCost {
				p.MaxOrderCostUSD = nil
			}
			switch {
			case changed("allowed-key"):
				p.AllowedAPIKeyIDs = allowedKeys
			case changed("add-allowed-key") || changed("remove-allowed-key"):
				p.AllowedAPIKeyIDs = editAllowedKeys(p.AllowedAPIKeyIDs, addKeys, removeKeys)
			}
			if p.AllowedAPIKeyIDs == nil {
				p.AllowedAPIKeyIDs = []string{}
			}
			body := map[string]any{
				"dispatchMode": p.DispatchMode, "planApproval": p.PlanApproval,
				"maxRunningOrders": p.MaxRunningOrders, "maxAttempts": p.MaxAttempts,
				"maxUnconfirmedClaims": p.MaxUnconfirmedClaims, "leaseMinutes": p.LeaseMinutes,
				"maxOrderRuntimeMinutes": p.MaxOrderRuntimeMinutes, "maxOrderCostUsd": p.MaxOrderCostUSD,
				"dailyOrderCap": p.DailyOrderCap, "allowedApiKeyIds": p.AllowedAPIKeyIDs,
				"maxQuestionsPerOrder": p.MaxQuestionsPerOrder, "questionReminderHours": p.QuestionReminderHours,
			}
			b, err := workBody(body)
			if err != nil {
				return err
			}
			raw, err = f.request(cmd.Context(), http.MethodPut, path, b)
			if err != nil {
				return allowedKeySourceError(err, changed)
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var updated automationPolicy
			if err := json.Unmarshal(raw, &updated); err != nil {
				return fmt.Errorf("invalid policy response: %w", err)
			}
			allowed := "none"
			if len(updated.AllowedAPIKeyIDs) > 0 {
				allowed = strings.Join(updated.AllowedAPIKeyIDs, ", ")
			}
			_, err = fmt.Fprintf(f.Out, "Updated the automation policy of product %s\nAllowed agent keys: %s\n", sanitizeTerminal(product), sanitizeTerminal(allowed))
			return err
		},
	}
	cmd.Flags().StringVar(&product, "product", "", "product ID (required)")
	cmd.Flags().StringVar(&dispatchMode, "dispatch-mode", "", "dispatch mode (manual)")
	cmd.Flags().StringVar(&planApproval, "plan-approval", "", "implementation plan approval: required|auto")
	cmd.Flags().IntVar(&maxRunning, "max-running-orders", 0, "orders running at once")
	cmd.Flags().IntVar(&maxAttempts, "max-attempts", 0, "failed attempts before an order blocks")
	cmd.Flags().IntVar(&maxUnconfirmed, "max-unconfirmed-claims", 0, "unconfirmed claims before an order blocks")
	cmd.Flags().IntVar(&leaseMinutes, "lease-minutes", 0, "lease length after a confirmed claim")
	cmd.Flags().IntVar(&maxRuntime, "max-order-runtime-minutes", 0, "runtime cap of one session")
	cmd.Flags().Float64Var(&maxCost, "max-order-cost-usd", 0, "cost cap of one order in USD")
	cmd.Flags().BoolVar(&clearMaxCost, "clear-max-order-cost", false, "remove the cost cap of one order")
	cmd.Flags().IntVar(&dailyCap, "daily-order-cap", 0, "claims per day")
	cmd.Flags().StringSliceVar(&allowedKeys, "allowed-key", nil, "agent key IDs whose workers may claim (repeatable; replaces the list)")
	cmd.Flags().StringSliceVar(&addKeys, "add-allowed-key", nil, "agent key ID to allow besides the keys already allowed (repeatable)")
	cmd.Flags().StringSliceVar(&removeKeys, "remove-allowed-key", nil, "agent key ID to take off the allowed list (repeatable)")
	cmd.Flags().IntVar(&maxQuestions, "max-questions-per-order", 0, "questions per order before they escalate")
	cmd.Flags().IntVar(&reminderHours, "question-reminder-hours", 0, "age after which an open question sends a reminder")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	cmd.MarkFlagsMutuallyExclusive("max-order-cost-usd", "clear-max-order-cost")
	cmd.MarkFlagsMutuallyExclusive("allowed-key", "add-allowed-key")
	cmd.MarkFlagsMutuallyExclusive("allowed-key", "remove-allowed-key")
	return cmd
}

func newWorkRepositoriesCmd(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "repositories",
		Aliases: []string{"repos"},
		Short:   "Manage the repositories a product's work orders may use",
	}
	cmd.AddCommand(newWorkReposListCmd(f), newWorkReposAddCmd(f), newWorkReposUpdateCmd(f), newWorkReposRemoveCmd(f))
	return cmd
}

func reposPath(product string, rest ...string) string {
	p := "/api/products/" + url.PathEscape(product) + "/repositories"
	for _, r := range rest {
		p += "/" + url.PathEscape(r)
	}
	return p
}

func newWorkReposListCmd(f *Factory) *cobra.Command {
	var product string
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "list",
		Short:        "List registered repositories and their risk paths",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireWorkProduct(product); err != nil {
				return err
			}
			raw, err := f.request(cmd.Context(), http.MethodGet, reposPath(product), nil)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var env struct {
				Data []struct {
					ID         string   `json:"id"`
					Repository string   `json:"repository"`
					RiskPaths  []string `json:"risk_paths"`
				} `json:"data"`
			}
			if err := json.Unmarshal(raw, &env); err != nil {
				return fmt.Errorf("invalid repository list: %w", err)
			}
			tw := tabwriter.NewWriter(f.Out, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tREPOSITORY\tRISK PATHS")
			for _, r := range env.Data {
				fmt.Fprintln(tw, tableRow(r.ID, r.Repository, strings.Join(r.RiskPaths, ", ")))
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&product, "product", "", "product ID (required)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newWorkReposAddCmd(f *Factory) *cobra.Command {
	var product, repository string
	var riskPaths []string
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "add",
		Short:        "Register a repository for a product",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if product == "" || repository == "" {
				return fmt.Errorf("--product and --repository are required")
			}
			if err := requireUUIDFlag("product", product); err != nil {
				return err
			}
			if riskPaths == nil {
				riskPaths = []string{}
			}
			raw, err := f.postJSON(cmd.Context(), reposPath(product), map[string]any{"repository": repository, "riskPaths": riskPaths})
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var repo struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(raw, &repo)
			_, err = fmt.Fprintln(f.Out, sanitizeTerminal(fmt.Sprintf("Registered %s (%s)", repository, repo.ID)))
			return err
		},
	}
	cmd.Flags().StringVar(&product, "product", "", "product ID (required)")
	cmd.Flags().StringVar(&repository, "repository", "", "https repository URL (required)")
	cmd.Flags().StringArrayVar(&riskPaths, "risk-path", nil, "glob of paths whose diffs wait for a human approval (repeatable)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newWorkReposUpdateCmd(f *Factory) *cobra.Command {
	var product string
	var riskPaths []string
	var clearPaths, asJSON bool
	cmd := &cobra.Command{
		Use:          "update <repository-id>",
		Short:        "Replace the risk paths of a registered repository",
		Long:         "Replace the risk paths of a registered repository. Name every risk path the repository keeps with --risk-path, or remove them all with --clear. Needs the browser login of `zensu auth login`.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireWorkProduct(product); err != nil {
				return err
			}
			if len(riskPaths) == 0 && !clearPaths {
				return fmt.Errorf("--risk-path or --clear is required; the update replaces every risk path of the repository")
			}
			paths := riskPaths
			if paths == nil {
				paths = []string{}
			}
			b, err := workBody(map[string]any{"riskPaths": paths})
			if err != nil {
				return err
			}
			raw, err := f.request(cmd.Context(), http.MethodPatch, reposPath(product, args[0]), b)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var repo struct {
				RiskPaths []string `json:"risk_paths"`
			}
			if err := json.Unmarshal(raw, &repo); err != nil {
				return fmt.Errorf("invalid repository response: %w", err)
			}
			kept := "none"
			if len(repo.RiskPaths) > 0 {
				kept = strings.Join(repo.RiskPaths, ", ")
			}
			_, err = fmt.Fprintln(f.Out, sanitizeTerminal(fmt.Sprintf("Updated the risk paths of repository %s: %s", args[0], kept)))
			return err
		},
	}
	cmd.Flags().StringVar(&product, "product", "", "product ID (required)")
	cmd.Flags().StringArrayVar(&riskPaths, "risk-path", nil, "glob of a risk path (repeatable; replaces the list; required unless --clear)")
	cmd.Flags().BoolVar(&clearPaths, "clear", false, "remove every risk path of the repository")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	cmd.MarkFlagsMutuallyExclusive("risk-path", "clear")
	return cmd
}

func newWorkReposRemoveCmd(f *Factory) *cobra.Command {
	var product string
	cmd := &cobra.Command{
		Use:          "remove <repository-id>",
		Short:        "Remove a registered repository (refused while a plan names it)",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireWorkProduct(product); err != nil {
				return err
			}
			if _, err := f.request(cmd.Context(), http.MethodDelete, reposPath(product, args[0]), nil); err != nil {
				return err
			}
			_, err := fmt.Fprintln(f.Out, sanitizeTerminal(fmt.Sprintf("Removed repository %s", args[0])))
			return err
		},
	}
	cmd.Flags().StringVar(&product, "product", "", "product ID (required)")
	return cmd
}
