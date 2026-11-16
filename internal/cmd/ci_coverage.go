package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

const (
	coverageFormatJSONSummary = "json-summary"
	coverageMaxFiles          = 10000
	coverageReportMaxBytes    = 64 << 20
	coverageSummaryShownPaths = 20
)

const emptyCoverageAnswer = `{"mappedFeatures":0,"entries":[],"unmappedPaths":[]}`

var markdownTextEscaper = strings.NewReplacer(
	`\`, `\\`, "`", "\\`", "*", `\*`, "_", `\_`, "[", `\[`, "]", `\]`,
	"<", `\<`, ">", `\>`, "&", `\&`, "|", `\|`, "!", `\!`, "~", `\~`,
)

type coverageFile struct {
	Path            string `json:"path"`
	LinesCovered    int64  `json:"linesCovered"`
	LinesTotal      int64  `json:"linesTotal"`
	BranchesCovered *int64 `json:"branchesCovered,omitempty"`
	BranchesTotal   *int64 `json:"branchesTotal,omitempty"`
}

type coverageUpload struct {
	ProductID string         `json:"productId"`
	Source    string         `json:"source"`
	Files     []coverageFile `json:"files"`
	CIRunURL  string         `json:"ciRunUrl,omitempty"`
}

type coverageEntry struct {
	Slug            string   `json:"slug"`
	CoveragePercent float64  `json:"coveragePercent"`
	LinesCovered    *float64 `json:"linesCovered"`
	LinesTotal      *float64 `json:"linesTotal"`
	BranchCoverage  *float64 `json:"branchCoverage"`
}

type coverageAnswer struct {
	MappedFeatures float64         `json:"mappedFeatures"`
	Entries        []coverageEntry `json:"entries"`
	UnmappedPaths  []string        `json:"unmappedPaths"`
}

var coverageAnswerFields = []answerField{
	{"mappedFeatures", jsonNumber},
	{"entries", jsonArray},
	{"unmappedPaths", jsonArray},
}

type coverageOptions struct {
	file       string
	format     string
	root       string
	runURL     string
	summary    string
	rootSet    bool
	runURLSet  bool
	summarySet bool
}

func newCICoverageCmd(f *Factory) *cobra.Command {
	var flags ciFlags
	var opts coverageOptions
	cmd := &cobra.Command{
		Use:   "coverage",
		Short: "Upload per-file coverage from an Istanbul json-summary report",
		Long: "Upload per-file coverage from an Istanbul json-summary report, such as coverage/coverage-summary.json " +
			"from the json-summary reporter. Every entry except total is sent as path, covered and total lines, plus " +
			"covered and total branches when the file has branches; Zensu sums them per feature of the component " +
			"each path maps to, and an upload replaces each feature's previous sum.\n\n" +
			"Paths are made relative to --root and use forward slashes; a path outside the root is sent unchanged. " +
			"A report without files uploads nothing and exits 0; more than 10000 files fail.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts.rootSet = cmd.Flags().Changed("root")
			opts.runURLSet = cmd.Flags().Changed("ci-run-url")
			opts.summarySet = cmd.Flags().Changed("markdown-summary")
			if opts.file == "" {
				return errors.New("--file is required")
			}
			if opts.format != coverageFormatJSONSummary {
				return fmt.Errorf("coverage format %q is not supported; use json-summary", sanitizeTerminal(opts.format))
			}
			run, err := flags.start(cmd, f)
			if err != nil {
				return err
			}
			return run.coverage(cmd.Context(), opts)
		},
	}
	flags.register(cmd)
	cmd.Flags().StringVar(&opts.file, "file", "", "path of the coverage report (required)")
	cmd.Flags().StringVar(&opts.format, "format", coverageFormatJSONSummary, "report format; only json-summary (Istanbul) is supported")
	cmd.Flags().StringVar(&opts.root, "root", "", "directory the report's paths are made relative to (default: GITHUB_WORKSPACE with --github, else the git top-level, else the working directory)")
	cmd.Flags().StringVar(&opts.runURL, "ci-run-url", "", "URL of the CI run, stored with the coverage (default with --github: the Actions run)")
	cmd.Flags().StringVar(&opts.summary, "markdown-summary", "", "append a Markdown summary of the result to this file (default with --github: GITHUB_STEP_SUMMARY)")
	return cmd
}

func (r *ciRun) coverage(ctx context.Context, opts coverageOptions) error {
	entries, err := readCoverageSummary(opts.file)
	if err != nil {
		return err
	}
	root, err := r.coverageRoot(ctx, opts)
	if err != nil {
		return err
	}
	prefixes := rootPrefixes(root)
	files := make([]coverageFile, 0, len(entries))
	for _, entry := range entries {
		file, err := entry.coverageFile(opts.file)
		if err != nil {
			return err
		}
		file.Path = repositoryPath(entry.path, prefixes)
		files = append(files, file)
	}
	shown := sanitizeTerminal(opts.file)
	if len(files) == 0 {
		r.report.warn("%s lists no files, so no coverage is uploaded.", shown)
		if r.flags.asJSON {
			return printJSON(r.report.out, []byte(emptyCoverageAnswer))
		}
		return nil
	}
	if len(files) > coverageMaxFiles {
		return fmt.Errorf("%s lists %d files; Zensu accepts at most %d per upload", shown, len(files), coverageMaxFiles)
	}
	upload := coverageUpload{ProductID: r.flags.product, Source: "ci", Files: files, CIRunURL: r.coverageRunURL(opts)}
	var answer coverageAnswer
	raw, err := r.post(ctx, "/api/ci/coverage", "upload coverage", upload, coverageAnswerFields, &answer)
	if err != nil {
		return err
	}
	if path := r.summaryPath(opts); path != "" {
		if err := appendCoverageSummary(path, answer); err != nil {
			return err
		}
	}
	if r.flags.asJSON {
		err = printJSON(r.report.out, raw)
	} else {
		err = r.report.say("Zensu recorded coverage for %s feature(s); %d of %d file(s) matched no component.", formatJSONNumber(answer.MappedFeatures), len(answer.UnmappedPaths), len(files))
	}
	if answer.MappedFeatures == 0 {
		r.report.warn("No file in the coverage report matched a component of this product, so no feature coverage changed.")
	}
	return err
}

func (r *ciRun) coverageRoot(ctx context.Context, opts coverageOptions) (string, error) {
	if opts.rootSet && opts.root != "" {
		return filepath.Abs(opts.root)
	}
	if r.flags.github {
		if workspace := os.Getenv("GITHUB_WORKSPACE"); workspace != "" {
			return filepath.Clean(workspace), nil
		}
	}
	if out, err := runGit(ctx, "rev-parse", "--show-toplevel"); err == nil {
		if top := strings.TrimSpace(string(out)); top != "" {
			return top, nil
		}
	}
	return os.Getwd()
}

func (r *ciRun) coverageRunURL(opts coverageOptions) string {
	if opts.runURLSet {
		return opts.runURL
	}
	if !r.flags.github {
		return ""
	}
	repository, id := githubRepositoryURL(), os.Getenv("GITHUB_RUN_ID")
	if repository == "" || id == "" {
		return ""
	}
	return repository + "/actions/runs/" + id
}

func (r *ciRun) summaryPath(opts coverageOptions) string {
	if opts.summarySet {
		return opts.summary
	}
	if r.flags.github {
		return os.Getenv("GITHUB_STEP_SUMMARY")
	}
	return ""
}

func rootPrefixes(root string) []string {
	var prefixes []string
	candidates := []string{root}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		candidates = append(candidates, resolved)
	}
	for _, dir := range candidates {
		prefix := strings.TrimSuffix(filepath.ToSlash(dir), "/") + "/"
		if !slices.Contains(prefixes, prefix) {
			prefixes = append(prefixes, prefix)
		}
	}
	slices.SortStableFunc(prefixes, func(a, b string) int { return len(b) - len(a) })
	return prefixes
}

func repositoryPath(path string, prefixes []string) string {
	slashed := filepath.ToSlash(path)
	for _, prefix := range prefixes {
		if rest, ok := strings.CutPrefix(slashed, prefix); ok {
			return rest
		}
	}
	return slashed
}

type summaryItem struct {
	path  string
	value json.RawMessage
}

func readCoverageSummary(path string) ([]summaryItem, error) {
	shown := sanitizeTerminal(path)
	file, err := openValueFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("coverage file not found: %s", shown)
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", shown, pathCause(err))
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", shown, pathCause(err))
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", shown)
	}
	data, err := io.ReadAll(io.LimitReader(file, coverageReportMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", shown, pathCause(err))
	}
	if len(data) > coverageReportMaxBytes {
		return nil, fmt.Errorf("%s exceeds %s", shown, byteSize(coverageReportMaxBytes))
	}
	items, ok := summaryItems(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")))
	if !ok {
		return nil, fmt.Errorf("%s is not an Istanbul json-summary report", shown)
	}
	return items, nil
}

func summaryItems(data []byte) ([]summaryItem, bool) {
	dec := json.NewDecoder(bytes.NewReader(data))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, false
	}
	seen := map[string]int{}
	var items []summaryItem
	total := false
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, false
		}
		key, ok := tok.(string)
		if !ok {
			return nil, false
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, false
		}
		if key == "total" {
			total = true
			continue
		}
		if i, dup := seen[key]; dup {
			items[i].value = value
			continue
		}
		seen[key] = len(items)
		items = append(items, summaryItem{path: key, value: value})
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') {
		return nil, false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, false
	}
	return items, total
}

func (s summaryItem) coverageFile(report string) (coverageFile, error) {
	bad := func(what string) error {
		return fmt.Errorf("%s is not an Istanbul json-summary report: the entry %s has no whole-number %s", sanitizeTerminal(report), strconv.Quote(sanitizeTerminal(s.path)), what)
	}
	dec := json.NewDecoder(bytes.NewReader(s.value))
	dec.UseNumber()
	var entry map[string]any
	if err := dec.Decode(&entry); err != nil || entry == nil {
		return coverageFile{}, bad("lines.covered and lines.total")
	}
	linesCovered, okCovered := countOf(entry, "lines", "covered")
	linesTotal, okTotal := countOf(entry, "lines", "total")
	if !okCovered || !okTotal {
		return coverageFile{}, bad("lines.covered and lines.total")
	}
	file := coverageFile{LinesCovered: linesCovered, LinesTotal: linesTotal}
	if nestedValue(entry, "branches", "total") == nil {
		return file, nil
	}
	branchesTotal, ok := countOf(entry, "branches", "total")
	if !ok {
		return coverageFile{}, bad("branches.total")
	}
	if branchesTotal == 0 {
		return file, nil
	}
	branchesCovered, ok := countOf(entry, "branches", "covered")
	if !ok {
		return coverageFile{}, bad("branches.covered")
	}
	file.BranchesCovered, file.BranchesTotal = &branchesCovered, &branchesTotal
	return file, nil
}

func countOf(entry map[string]any, keys ...string) (int64, bool) {
	number, ok := nestedValue(entry, keys...).(json.Number)
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(number.String(), 10, 64)
	return n, err == nil && n >= 0
}

func appendCoverageSummary(path string, answer coverageAnswer) error {
	var b strings.Builder
	b.WriteString("## Zensu coverage\n\n")
	if answer.MappedFeatures > 0 {
		b.WriteString("| Feature | Coverage | Lines | Branches |\n|---|---|---|---|\n")
		for _, e := range answer.Entries {
			lines, branches := "n/a", "n/a"
			if e.LinesTotal != nil {
				lines = optionalNumber(e.LinesCovered) + "/" + formatJSONNumber(*e.LinesTotal)
			}
			if e.BranchCoverage != nil {
				branches = formatJSONNumber(*e.BranchCoverage) + "%"
			}
			fmt.Fprintf(&b, "| %s | %s%% | %s | %s |\n", markdownTextEscaper.Replace(sanitizeTerminal(e.Slug)), formatJSONNumber(e.CoveragePercent), lines, branches)
		}
	} else {
		b.WriteString("No feature coverage changed.\n")
	}
	if n := len(answer.UnmappedPaths); n > 0 {
		shown := make([]string, 0, coverageSummaryShownPaths)
		for _, p := range answer.UnmappedPaths[:min(n, coverageSummaryShownPaths)] {
			shown = append(shown, markdownCode(sanitizeTerminal(p)))
		}
		fmt.Fprintf(&b, "\nFiles that matched no component: %s", strings.Join(shown, ", "))
		if n > coverageSummaryShownPaths {
			fmt.Fprintf(&b, " and %d more", n-coverageSummaryShownPaths)
		}
		b.WriteString("\n")
	}
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return fmt.Errorf("writing the Markdown summary: %w", err)
	}
	if _, err := io.WriteString(out, b.String()); err != nil {
		out.Close()
		return fmt.Errorf("writing the Markdown summary: %w", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("writing the Markdown summary: %w", err)
	}
	return nil
}

func markdownCode(s string) string {
	longest, run := 0, 0
	for _, r := range s {
		if r != '`' {
			run = 0
			continue
		}
		run++
		longest = max(longest, run)
	}
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") {
		s = " " + s + " "
	}
	fence := strings.Repeat("`", longest+1)
	return fence + s + fence
}

func optionalNumber(v *float64) string {
	if v == nil {
		return "null"
	}
	return formatJSONNumber(*v)
}
