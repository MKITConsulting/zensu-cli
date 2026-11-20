package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	progressMaxBatchFacts = 100
	progressMaxBatchBytes = 256 << 10
	progressMaxProseRunes = 120
	progressMaxFindings   = 20
	progressMaxStepTotal  = 500
	progressMaxRound      = 50

	progressMaxBranchRunes   = 200
	progressMaxWorktreeBytes = 100
	progressMaxVersionBytes  = 32
	progressMaxRepoBytes     = 200
	progressMaxURLBytes      = 500
	progressMaxFileCount     = 1_000_000
	progressMaxFindingCount  = 10_000
	progressMaxCheckCount    = 10_000_000

	progressKindSignal = "signal"
)

var (
	progressFactKinds = []string{
		"session_started", "session_ended", "turn_started", "turn_ended", "segment_started", "segment_ended",
		"step_progress", "wait_requested", "wait_resolved", "review_round_started", "review_round_ended",
		"change_request_linked", "workspace_state", "evidence_reported", "blocked", "resumed", progressKindSignal, "work_ended",
	}
	progressHarnesses           = []string{"claude_code", "codex", "copilot", "kiro", "antigravity", "other"}
	progressSessionEndReasons   = []string{"ended", "interrupted", "unknown"}
	progressPhases              = []string{"plan", "implement", "review", "fix", "verify"}
	progressSegmentOutcomes     = []string{"completed", "interrupted", "failed"}
	progressWaitSubjects        = []string{"plan_approval", "question", "permission", "input"}
	progressWaitSubjectOutcomes = map[string][]string{
		"plan_approval": {"approved", "rejected", "cancelled"},
		"question":      {"answered", "cancelled"},
		"permission":    {"granted", "denied", "cancelled"},
		"input":         {"answered", "cancelled"},
	}
	progressSeverities          = []string{"critical", "high", "medium", "low", "info"}
	progressFindingStates       = []string{"open", "in_progress", "fixed", "dismissed"}
	progressChangeRequestStates = []string{"open", "merged", "closed"}
	progressEvidenceChecks      = []string{"tests", "typecheck", "coverage", "lint", "build"}
	progressEvidenceOutcomes    = []string{"passed", "failed", "skipped"}
	progressBlockKinds          = []string{"review_limit", "worker_error", "precondition", "other"}
	progressActivities          = []string{"editing", "testing", "reviewing", "committing", "delegating", "idle"}
	progressWorkEndReasons      = []string{"finished", "abandoned", "handed_off"}

	progressPersonOnlyWorkEndReason = "closed_by_person"
)

type progressFact struct {
	Kind    string
	Payload json.RawMessage
}

type progressPayload interface {
	check() error
}

type progressEmptyPayload struct{}

type progressSessionStarted struct {
	Harness       string `json:"harness"`
	ClientVersion string `json:"clientVersion,omitempty"`
	Branch        string `json:"branch,omitempty"`
	Worktree      string `json:"worktree,omitempty"`
}

type progressSessionEnded struct {
	Reason string `json:"reason"`
}

type progressSegmentStarted struct {
	Phase string `json:"phase"`
}

type progressSegmentEnded struct {
	Phase   string `json:"phase"`
	Outcome string `json:"outcome"`
}

type progressStepProgress struct {
	Done  int    `json:"done"`
	Total int    `json:"total"`
	Label string `json:"label,omitempty"`
}

type progressWaitRequested struct {
	Subject string `json:"subject"`
}

type progressWaitResolved struct {
	Subject string `json:"subject"`
	Outcome string `json:"outcome"`
}

type progressReviewRoundStarted struct {
	Round int `json:"round"`
}

type progressFindingCounts struct {
	Open     int `json:"open"`
	Fixed    int `json:"fixed"`
	Blocking int `json:"blocking"`
}

type progressFindingItem struct {
	Severity string `json:"severity"`
	State    string `json:"state"`
	Title    string `json:"title"`
}

type progressReviewRoundEnded struct {
	Round        int                   `json:"round"`
	Findings     progressFindingCounts `json:"findings"`
	Items        []progressFindingItem `json:"items,omitempty"`
	ChainClosed  bool                  `json:"chainClosed,omitempty"`
	LimitReached bool                  `json:"limitReached,omitempty"`
}

type progressChangeRequestLinked struct {
	Repository string `json:"repository"`
	Number     int    `json:"number"`
	State      string `json:"state,omitempty"`
	URL        string `json:"url,omitempty"`
}

type progressWorkspaceState struct {
	CommitsAhead     int    `json:"commitsAhead"`
	FilesChanged     int    `json:"filesChanged"`
	UncommittedFiles int    `json:"uncommittedFiles"`
	Head             string `json:"head"`
}

type progressEvidenceReported struct {
	Check   string   `json:"check"`
	Outcome string   `json:"outcome"`
	Passed  *int     `json:"passed,omitempty"`
	Failed  *int     `json:"failed,omitempty"`
	Percent *float64 `json:"percent,omitempty"`
}

type progressBlocked struct {
	Kind   string `json:"kind"`
	Reason string `json:"reason,omitempty"`
}

type progressSignal struct {
	Activity string `json:"activity,omitempty"`
}

type progressWorkEnded struct {
	Reason string `json:"reason"`
}

func newProgressPayload(kind string) progressPayload {
	switch kind {
	case "session_started":
		return &progressSessionStarted{}
	case "session_ended":
		return &progressSessionEnded{}
	case "turn_started", "turn_ended", "resumed":
		return &progressEmptyPayload{}
	case "segment_started":
		return &progressSegmentStarted{}
	case "segment_ended":
		return &progressSegmentEnded{}
	case "step_progress":
		return &progressStepProgress{}
	case "wait_requested":
		return &progressWaitRequested{}
	case "wait_resolved":
		return &progressWaitResolved{}
	case "review_round_started":
		return &progressReviewRoundStarted{}
	case "review_round_ended":
		return &progressReviewRoundEnded{}
	case "change_request_linked":
		return &progressChangeRequestLinked{}
	case "workspace_state":
		return &progressWorkspaceState{}
	case "evidence_reported":
		return &progressEvidenceReported{}
	case "blocked":
		return &progressBlocked{}
	case progressKindSignal:
		return &progressSignal{}
	case "work_ended":
		return &progressWorkEnded{}
	}
	return nil
}

func progressListed(field, value string, list []string) error {
	if !slices.Contains(list, value) {
		return fmt.Errorf("%s must be one of %s", field, strings.Join(list, ", "))
	}
	return nil
}

func (p *progressEmptyPayload) check() error { return nil }

func (p *progressSessionStarted) check() error {
	if err := progressListed("harness", p.Harness, progressHarnesses); err != nil {
		return err
	}
	if p.ClientVersion != "" && (len(p.ClientVersion) > progressMaxVersionBytes || !progressTokenChars(p.ClientVersion, "._+-")) {
		return errors.New("clientVersion must be at most 32 characters of letters, digits, dot, underscore, plus or hyphen")
	}
	if p.Branch != "" && !progressValidBranch(p.Branch) {
		return errors.New("branch must be a git branch name of at most 200 characters")
	}
	if p.Worktree != "" && (len(p.Worktree) > progressMaxWorktreeBytes || !progressTokenChars(p.Worktree, "._+-")) {
		return errors.New("worktree must be a label of at most 100 letters, digits, dots, underscores, plus signs or hyphens, never a path")
	}
	return nil
}

func (p *progressSessionEnded) check() error {
	return progressListed("reason", p.Reason, progressSessionEndReasons)
}

func (p *progressSegmentStarted) check() error {
	return progressListed("phase", p.Phase, progressPhases)
}

func (p *progressSegmentEnded) check() error {
	if err := progressListed("phase", p.Phase, progressPhases); err != nil {
		return err
	}
	return progressListed("outcome", p.Outcome, progressSegmentOutcomes)
}

func (p *progressStepProgress) check() error {
	if p.Total < 1 || p.Total > progressMaxStepTotal {
		return errors.New("total must be between 1 and 500")
	}
	if p.Done < 0 || p.Done > p.Total {
		return errors.New("done must be between 0 and total")
	}
	label, ok := progressCleanLine(p.Label, progressMaxProseRunes)
	if !ok {
		return errors.New("label must be a single line of at most 120 characters")
	}
	p.Label = label
	return nil
}

func (p *progressWaitRequested) check() error {
	return progressListed("subject", p.Subject, progressWaitSubjects)
}

func (p *progressWaitResolved) check() error {
	if err := progressListed("subject", p.Subject, progressWaitSubjects); err != nil {
		return err
	}
	if outcomes := progressWaitSubjectOutcomes[p.Subject]; !slices.Contains(outcomes, p.Outcome) {
		return fmt.Errorf("outcome of a %s wait must be one of %s", p.Subject, strings.Join(outcomes, ", "))
	}
	return nil
}

func progressRound(round int) error {
	if round < 1 || round > progressMaxRound {
		return errors.New("round must be between 1 and 50")
	}
	return nil
}

func (p *progressReviewRoundStarted) check() error { return progressRound(p.Round) }

func (p *progressReviewRoundEnded) check() error {
	if err := progressRound(p.Round); err != nil {
		return err
	}
	for _, n := range []int{p.Findings.Open, p.Findings.Fixed, p.Findings.Blocking} {
		if n < 0 || n > progressMaxFindingCount {
			return errors.New("findings must hold counts between 0 and 10000")
		}
	}
	if p.Findings.Blocking > p.Findings.Open {
		return errors.New("findings.blocking must not exceed the open findings")
	}
	if len(p.Items) > progressMaxFindings {
		return errors.New("items must hold at most 20 findings")
	}
	for i := range p.Items {
		item := &p.Items[i]
		if err := progressListed("items.severity", item.Severity, progressSeverities); err != nil {
			return err
		}
		if err := progressListed("items.state", item.State, progressFindingStates); err != nil {
			return err
		}
		title, ok := progressCleanLine(item.Title, progressMaxProseRunes)
		if !ok || title == "" {
			return errors.New("items.title must be a single line of 1 to 120 characters")
		}
		item.Title = title
	}
	if len(p.Items) == 0 {
		p.Items = nil
	}
	return nil
}

func (p *progressChangeRequestLinked) check() error {
	if !progressValidRepository(p.Repository) {
		return errors.New("repository must be an owner and name of at most 200 characters")
	}
	if p.Number < 1 || p.Number > math.MaxInt32 {
		return errors.New("number must be a positive change request number")
	}
	if p.State != "" {
		if err := progressListed("state", p.State, progressChangeRequestStates); err != nil {
			return err
		}
	}
	if p.URL != "" && !progressValidLinkURL(p.URL) {
		return errors.New("url must be an https address of at most 500 characters without credentials, query, fragment or markup characters")
	}
	return nil
}

func (p *progressWorkspaceState) check() error {
	for _, n := range []int{p.CommitsAhead, p.FilesChanged, p.UncommittedFiles} {
		if n < 0 || n > progressMaxFileCount {
			return errors.New("commitsAhead, filesChanged and uncommittedFiles must be between 0 and 1000000")
		}
	}
	head := strings.ToLower(p.Head)
	if len(head) < 7 || len(head) > 64 || strings.Trim(head, "0123456789abcdef") != "" {
		return errors.New("head must be 7 to 64 hexadecimal characters")
	}
	p.Head = head
	return nil
}

func (p *progressEvidenceReported) check() error {
	if err := progressListed("check", p.Check, progressEvidenceChecks); err != nil {
		return err
	}
	if err := progressListed("outcome", p.Outcome, progressEvidenceOutcomes); err != nil {
		return err
	}
	for _, n := range []*int{p.Passed, p.Failed} {
		if n != nil && (*n < 0 || *n > progressMaxCheckCount) {
			return errors.New("passed and failed must be between 0 and 10000000")
		}
	}
	if p.Percent != nil && (math.IsNaN(*p.Percent) || *p.Percent < 0 || *p.Percent > 100) {
		return errors.New("percent must be between 0 and 100")
	}
	return nil
}

func (p *progressBlocked) check() error {
	if err := progressListed("kind", p.Kind, progressBlockKinds); err != nil {
		return err
	}
	reason, ok := progressCleanLine(p.Reason, progressMaxProseRunes)
	if !ok {
		return errors.New("reason must be a single line of at most 120 characters")
	}
	p.Reason = reason
	return nil
}

func (p *progressSignal) check() error {
	if p.Activity == "" {
		return nil
	}
	return progressListed("activity", p.Activity, progressActivities)
}

func (p *progressWorkEnded) check() error {
	if p.Reason == progressPersonOnlyWorkEndReason {
		return errors.New("reason closed_by_person is recorded only by the close action in the browser")
	}
	return progressListed("reason", p.Reason, progressWorkEndReasons)
}

func progressTokenChars(s, extra string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.IndexByte(extra, c) >= 0:
		default:
			return false
		}
	}
	return true
}

func progressIsJoiner(r rune) bool { return r == 0x200C || r == 0x200D }

func progressCleanLine(s string, maxRunes int) (string, bool) {
	if !utf8.ValidString(s) {
		return "", false
	}
	for _, r := range s {
		if !unicode.IsPrint(r) && !unicode.Is(unicode.Zs, r) && !progressIsJoiner(r) {
			return "", false
		}
	}
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > maxRunes {
		return "", false
	}
	if s != "" && strings.TrimFunc(s, func(r rune) bool { return progressIsJoiner(r) || unicode.IsSpace(r) }) == "" {
		return "", false
	}
	return s, true
}

func progressValidBranch(s string) bool {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > progressMaxBranchRunes {
		return false
	}
	for _, r := range s {
		if !unicode.IsPrint(r) || unicode.IsSpace(r) || strings.ContainsRune(`\~^:?*[`, r) {
			return false
		}
	}
	if s == "@" || strings.HasPrefix(s, "-") || strings.HasSuffix(s, ".") || strings.Contains(s, "..") || strings.Contains(s, "@{") {
		return false
	}
	for _, component := range strings.Split(s, "/") {
		if component == "" || strings.HasPrefix(component, ".") || strings.HasSuffix(component, ".lock") {
			return false
		}
	}
	return true
}

func progressValidRepository(s string) bool {
	if s == "" || len(s) > progressMaxRepoBytes || !progressTokenChars(s, "._-/") {
		return false
	}
	segments := strings.Split(s, "/")
	if len(segments) < 2 {
		return false
	}
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func progressValidLinkURL(s string) bool {
	if len(s) > progressMaxURLBytes {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] <= 0x20 || s[i] >= 0x7f || strings.IndexByte("\"'<>`\\{}|^()?#", s[i]) >= 0 {
			return false
		}
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Opaque != "" {
		return false
	}
	afterScheme := s[len("https://"):]
	path := strings.IndexByte(afterScheme, '/')
	return path < 0 || !strings.ContainsAny(afterScheme[path:], "[]")
}

func sealProgressPayload(kind string, payload progressPayload) (progressFact, error) {
	if err := payload.check(); err != nil {
		return progressFact{}, fmt.Errorf("%s: %w", kind, err)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return progressFact{}, fmt.Errorf("%s: payload cannot be encoded", kind)
	}
	if bytes.Equal(raw, []byte("{}")) {
		raw = nil
	}
	return progressFact{Kind: kind, Payload: raw}, nil
}

func decodeProgressStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("unexpected data after the JSON value")
	}
	return nil
}

func progressDecodeProblem(what string, err error) error {
	var typeErr *json.UnmarshalTypeError
	switch {
	case errors.As(err, &typeErr):
		return fmt.Errorf("%s has a field of the wrong type", what)
	case strings.HasPrefix(err.Error(), "json: unknown field"):
		return fmt.Errorf("%s has an unknown field (%s)", what, strings.TrimPrefix(err.Error(), "json: unknown field "))
	}
	return fmt.Errorf("%s is not valid JSON", what)
}

func decodeProgressBatchLine(line []byte) (progressFact, error) {
	var envelope struct {
		Kind    string          `json:"kind"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := decodeProgressStrict(line, &envelope); err != nil {
		return progressFact{}, progressDecodeProblem("the line", err)
	}
	payload := newProgressPayload(envelope.Kind)
	if payload == nil {
		return progressFact{}, fmt.Errorf("unknown kind %q; the kinds are %s", envelope.Kind, strings.Join(progressFactKinds, ", "))
	}
	trimmed := bytes.TrimSpace(envelope.Payload)
	if len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) {
		if trimmed[0] != '{' {
			return progressFact{}, fmt.Errorf("%s: payload must be an object", envelope.Kind)
		}
		if err := decodeProgressStrict(trimmed, payload); err != nil {
			return progressFact{}, fmt.Errorf("%s: %w", envelope.Kind, progressDecodeProblem("payload", err))
		}
	}
	return sealProgressPayload(envelope.Kind, payload)
}

func readProgressBatch(r io.Reader) ([]progressFact, error) {
	data, err := io.ReadAll(io.LimitReader(r, progressMaxBatchBytes+1))
	if err != nil {
		return nil, fmt.Errorf("cannot read stdin: %w", err)
	}
	if len(data) > progressMaxBatchBytes {
		return nil, fmt.Errorf("--batch input exceeds %s", byteSize(progressMaxBatchBytes))
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	var facts []progressFact
	for number, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		if len(facts) == progressMaxBatchFacts {
			return nil, fmt.Errorf("--batch takes at most %d facts per call", progressMaxBatchFacts)
		}
		fact, err := decodeProgressBatchLine(line)
		if err != nil {
			return nil, fmt.Errorf("--batch line %d: %w", number+1, err)
		}
		facts = append(facts, fact)
	}
	if len(facts) == 0 {
		return nil, errors.New("--batch needs at least one JSON line on stdin")
	}
	return facts, nil
}

type progressFlagValues struct {
	harness, clientVersion, branch, worktree string
	reason, phase, outcome, subject          string
	done, total, round                       int
	label                                    string
	findingsOpen, findingsFixed              int
	findingsBlocking                         int
	findings                                 []string
	chainClosed, limitReached                bool
	repository, state, url                   string
	number                                   int
	commitsAhead, filesChanged               int
	uncommittedFiles                         int
	head, check, blockKind, activity         string
	passed, failed                           int
	percent                                  float64
}

var progressKindFlags = map[string][]string{
	"session_started":       {"harness", "client-version", "branch", "worktree"},
	"session_ended":         {"reason"},
	"turn_started":          {},
	"turn_ended":            {},
	"resumed":               {},
	"segment_started":       {"phase"},
	"segment_ended":         {"phase", "outcome"},
	"step_progress":         {"done", "total", "label"},
	"wait_requested":        {"subject"},
	"wait_resolved":         {"subject", "outcome"},
	"review_round_started":  {"round"},
	"review_round_ended":    {"round", "findings-open", "findings-fixed", "findings-blocking", "finding", "chain-closed", "limit-reached"},
	"change_request_linked": {"repository", "number", "state", "url"},
	"workspace_state":       {"commits-ahead", "files-changed", "uncommitted-files", "head"},
	"evidence_reported":     {"check", "outcome", "passed", "failed", "percent"},
	"blocked":               {"block-kind", "reason"},
	progressKindSignal:      {"activity"},
	"work_ended":            {"reason"},
}

func progressPayloadFlagNames() []string {
	seen := map[string]bool{}
	var names []string
	for _, kind := range progressFactKinds {
		for _, name := range progressKindFlags[kind] {
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	slices.Sort(names)
	return names
}

func progressFactFromFlags(kind string, v *progressFlagValues, changed func(string) bool) (progressFact, error) {
	allowed, known := progressKindFlags[kind]
	if !known {
		return progressFact{}, fmt.Errorf("unknown --kind %q; the kinds are %s", kind, strings.Join(progressFactKinds, ", "))
	}
	for _, name := range progressPayloadFlagNames() {
		if changed(name) && !slices.Contains(allowed, name) {
			return progressFact{}, fmt.Errorf("--%s does not belong to kind %s", name, kind)
		}
	}
	var payload progressPayload
	switch kind {
	case "session_started":
		payload = &progressSessionStarted{Harness: v.harness, ClientVersion: v.clientVersion, Branch: v.branch, Worktree: v.worktree}
	case "session_ended":
		payload = &progressSessionEnded{Reason: v.reason}
	case "segment_started":
		payload = &progressSegmentStarted{Phase: v.phase}
	case "segment_ended":
		payload = &progressSegmentEnded{Phase: v.phase, Outcome: v.outcome}
	case "step_progress":
		payload = &progressStepProgress{Done: v.done, Total: v.total, Label: v.label}
	case "wait_requested":
		payload = &progressWaitRequested{Subject: v.subject}
	case "wait_resolved":
		payload = &progressWaitResolved{Subject: v.subject, Outcome: v.outcome}
	case "review_round_started":
		payload = &progressReviewRoundStarted{Round: v.round}
	case "review_round_ended":
		items, err := progressFindingItems(v.findings)
		if err != nil {
			return progressFact{}, fmt.Errorf("%s: %w", kind, err)
		}
		payload = &progressReviewRoundEnded{Round: v.round, Findings: progressFindingCounts{Open: v.findingsOpen, Fixed: v.findingsFixed, Blocking: v.findingsBlocking}, Items: items, ChainClosed: v.chainClosed, LimitReached: v.limitReached}
	case "change_request_linked":
		payload = &progressChangeRequestLinked{Repository: v.repository, Number: v.number, State: v.state, URL: v.url}
	case "workspace_state":
		payload = &progressWorkspaceState{CommitsAhead: v.commitsAhead, FilesChanged: v.filesChanged, UncommittedFiles: v.uncommittedFiles, Head: v.head}
	case "evidence_reported":
		evidence := &progressEvidenceReported{Check: v.check, Outcome: v.outcome}
		if changed("passed") {
			evidence.Passed = &v.passed
		}
		if changed("failed") {
			evidence.Failed = &v.failed
		}
		if changed("percent") {
			evidence.Percent = &v.percent
		}
		payload = evidence
	case "blocked":
		payload = &progressBlocked{Kind: v.blockKind, Reason: v.reason}
	case progressKindSignal:
		payload = &progressSignal{Activity: v.activity}
	case "work_ended":
		payload = &progressWorkEnded{Reason: v.reason}
	default:
		payload = &progressEmptyPayload{}
	}
	return sealProgressPayload(kind, payload)
}

func progressFindingItems(values []string) ([]progressFindingItem, error) {
	var items []progressFindingItem
	for _, value := range values {
		parts := strings.SplitN(value, ":", 3)
		if len(parts) != 3 {
			return nil, errors.New("--finding takes <severity>:<state>:<title>")
		}
		items = append(items, progressFindingItem{Severity: parts[0], State: parts[1], Title: parts[2]})
	}
	return items, nil
}

func progressSignalActivity(payload json.RawMessage) string {
	var signal progressSignal
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &signal)
	}
	return signal.Activity
}
