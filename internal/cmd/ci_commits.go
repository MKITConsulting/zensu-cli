package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/spf13/cobra"
)

const (
	commitBatchSize = 1000
	commitLogFormat = "%H%x1f%an%x1f%aI%x1f%B"
)

const emptyCommitsAnswer = `{"processed":0,"links":[]}`

var (
	commitSHAPattern     = regexp.MustCompile(`^[0-9a-fA-F]{4,64}$`)
	fullCommitSHAPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
)

type ciCommit struct {
	Hash      string `json:"hash"`
	Message   string `json:"message"`
	Author    string `json:"author"`
	Timestamp string `json:"timestamp"`
}

type commitsUpload struct {
	ProductID string     `json:"productId"`
	Commits   []ciCommit `json:"commits"`
}

type commitLink struct {
	CommitHash  string   `json:"commitHash"`
	FeatureRefs []string `json:"featureRefs"`
	Linked      float64  `json:"linked"`
	Unresolved  []string `json:"unresolved"`
}

type commitLinkAnswer struct {
	commitLink
	raw json.RawMessage
}

func (l *commitLinkAnswer) UnmarshalJSON(data []byte) error {
	if err := json.Unmarshal(data, &l.commitLink); err != nil {
		return err
	}
	l.raw = append(json.RawMessage(nil), data...)
	return nil
}

type commitsAnswer struct {
	Processed float64            `json:"processed"`
	Links     []commitLinkAnswer `json:"links"`
}

type commitsResult struct {
	Processed float64           `json:"processed"`
	Links     []json.RawMessage `json:"links"`
}

var commitsAnswerFields = []answerField{{"processed", jsonNumber}, {"links", jsonArray}}

func newCICommitsCmd(f *Factory) *cobra.Command {
	var flags ciFlags
	var revisionRange string
	cmd := &cobra.Command{
		Use:   "commits",
		Short: "Link the commits that reference a feature",
		Long: "Upload the commits whose messages reference a feature in brackets, such as [ZEN-42] or " +
			"[zen-invoice-rounding] (a key, a dash and a number or slug, in any case), so Zensu links them to that " +
			"feature. Commits without such a reference never leave the machine.\n\n" +
			"Without --range the command checks the HEAD commit. With --github it checks the commits of the push " +
			"(before..head) or of the pull request (base..head); a base that is not in the checkout leaves only the " +
			"head and warns, and so does a range that reaches the boundary of a shallow checkout, so check out with " +
			"fetch-depth: 0 to cover every commit.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			run, err := flags.start(cmd, f)
			if err != nil {
				return err
			}
			return run.commits(cmd.Context(), revisionRange, cmd.Flags().Changed("range"))
		},
	}
	flags.register(cmd)
	cmd.Flags().StringVar(&revisionRange, "range", "", "git revision range to check, such as main..HEAD (default: the HEAD commit; with --github: the commits of the push or pull request)")
	return cmd
}

func (r *ciRun) commits(ctx context.Context, revisionRange string, rangeSet bool) error {
	selection, err := r.commitRange(ctx, revisionRange, rangeSet)
	if err != nil {
		return err
	}
	out, err := runGit(ctx, commitLogArgs(selection)...)
	if err != nil {
		return err
	}
	records, err := commitRecords(out)
	if err != nil {
		return err
	}
	if err := r.warnAboutShallowHistory(ctx, selection, records); err != nil {
		return err
	}
	commits := referencingCommits(records)
	if len(commits) == 0 {
		if r.flags.asJSON {
			return printJSON(r.report.out, []byte(emptyCommitsAnswer))
		}
		return r.report.say("No commit in %s references a feature.", sanitizeTerminal(selection.revision))
	}
	result := commitsResult{Links: []json.RawMessage{}}
	var links []commitLink
	for start := 0; start < len(commits); start += commitBatchSize {
		batch := commits[start:min(start+commitBatchSize, len(commits))]
		var answer commitsAnswer
		if _, err := r.post(ctx, "/api/ci/commits", "upload commits", commitsUpload{ProductID: r.flags.product, Commits: batch}, commitsAnswerFields, &answer); err != nil {
			return err
		}
		result.Processed += answer.Processed
		for _, link := range answer.Links {
			result.Links = append(result.Links, link.raw)
			links = append(links, link.commitLink)
		}
	}
	if r.flags.asJSON {
		data, err := json.Marshal(result)
		if err != nil {
			return err
		}
		return printJSON(r.report.out, data)
	}
	linked := 0.0
	for _, link := range links {
		linked += link.Linked
	}
	if err := r.report.say("Zensu linked %s feature reference(s) from %d commit(s).", formatJSONNumber(linked), len(commits)); err != nil {
		return err
	}
	for _, link := range links {
		for _, ref := range link.FeatureRefs {
			_, tail, found := strings.Cut(ref, "-")
			if !found {
				tail = ref
			}
			if !slices.Contains(link.Unresolved, tail) {
				continue
			}
			if err := r.report.say("No feature of this product matches %s in commit %s.", sanitizeTerminal(ref), shortHash(sanitizeTerminal(link.CommitHash))); err != nil {
				return err
			}
		}
	}
	return nil
}

type commitSelection struct {
	revision string
	single   bool
}

func (r *ciRun) commitRange(ctx context.Context, revisionRange string, rangeSet bool) (commitSelection, error) {
	if rangeSet {
		switch {
		case revisionRange == "":
			return commitSelection{}, errors.New("--range needs a revision range, such as main..HEAD")
		case strings.HasPrefix(revisionRange, "-"):
			return commitSelection{}, fmt.Errorf("--range %q starts with -; pass a revision range, such as main..HEAD", sanitizeTerminal(revisionRange))
		}
		return commitSelection{revision: revisionRange}, nil
	}
	if !r.flags.github {
		return commitSelection{revision: "HEAD", single: true}, nil
	}
	head, base := os.Getenv("GITHUB_SHA"), ""
	event, err := readGitHubEvent()
	if err != nil {
		return commitSelection{}, err
	}
	if event != nil {
		switch os.Getenv("GITHUB_EVENT_NAME") {
		case "pull_request":
			base = eventString(event, "pull_request", "base", "sha")
			head = eventString(event, "pull_request", "head", "sha")
		case "push":
			base = eventString(event, "before")
		}
	}
	if !hasCommit(ctx, head) {
		out, err := runGit(ctx, "rev-parse", "--verify", "HEAD")
		if err != nil {
			return commitSelection{}, err
		}
		head = strings.TrimSpace(string(out))
	}
	if base != "" && strings.Trim(base, "0") == "" {
		base = ""
	}
	if base != "" && !hasCommit(ctx, base) {
		r.report.warn("Commit %s is not in this checkout, so only %s is checked. Check out with fetch-depth: 0 to cover every commit.", sanitizeTerminal(base), head)
		base = ""
	}
	if base != "" {
		return commitSelection{revision: base + ".." + head}, nil
	}
	return commitSelection{revision: head, single: true}, nil
}

func hasCommit(ctx context.Context, sha string) bool {
	if !commitSHAPattern.MatchString(sha) {
		return false
	}
	_, err := runGit(ctx, "cat-file", "-e", sha+"^{commit}")
	return err == nil
}

func commitLogArgs(selection commitSelection) []string {
	args := []string{"log", "-z", "--no-show-signature"}
	if selection.single {
		args = append(args, "-1")
	}
	return append(args, "--format="+commitLogFormat, "--end-of-options", selection.revision)
}

func commitRecords(log []byte) ([]ciCommit, error) {
	var records []ciCommit
	for _, record := range strings.Split(string(log), "\x00") {
		if strings.TrimSpace(record) == "" {
			continue
		}
		parts := strings.SplitN(record, "\x1f", 4)
		if len(parts) != 4 || !fullCommitSHAPattern.MatchString(parts[0]) {
			return nil, errors.New("git log printed a record that does not start with a commit hash; check the git configuration of this checkout")
		}
		records = append(records, ciCommit{
			Hash:      parts[0],
			Author:    parts[1],
			Timestamp: parts[2],
			Message:   strings.TrimRightFunc(parts[3], unicode.IsSpace),
		})
	}
	return records, nil
}

func referencingCommits(records []ciCommit) []ciCommit {
	var commits []ciCommit
	for _, c := range records {
		if featureReferencePattern.MatchString(c.Message) {
			commits = append(commits, c)
		}
	}
	return commits
}

func (r *ciRun) warnAboutShallowHistory(ctx context.Context, selection commitSelection, records []ciCommit) error {
	if selection.single || len(records) == 0 {
		return nil
	}
	out, err := runGit(ctx, "rev-parse", "--is-shallow-repository")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(out)) != "true" {
		return nil
	}
	out, err = runGit(ctx, "rev-parse", "--git-path", "shallow")
	if err != nil {
		return err
	}
	data, err := os.ReadFile(strings.TrimSpace(string(out)))
	if err != nil {
		return fmt.Errorf("reading the shallow boundary of this checkout: %w", err)
	}
	boundary := strings.Fields(string(data))
	for _, c := range records {
		if slices.Contains(boundary, c.Hash) {
			r.report.warn("This checkout is shallow, so %s may miss the commits before %s. Check out with fetch-depth: 0 to cover every commit.", sanitizeTerminal(selection.revision), shortHash(c.Hash))
			return nil
		}
	}
	return nil
}

func shortHash(hash string) string {
	if utf8.RuneCountInString(hash) <= 7 {
		return hash
	}
	return string([]rune(hash)[:7])
}
