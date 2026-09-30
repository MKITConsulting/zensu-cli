package cmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"unicode"

	"go.yaml.in/yaml/v3"
)

const (
	maxPlanFileBytes        = 1 << 20
	maxPlanItems            = 50
	maxPlanNameBytes        = 255
	maxPlanItemPaths        = 100
	maxPlanPaths            = 1000
	maxPlanItemRequirements = 200
	maxPlanRequirements     = 1000
	maxPlanPathBytes        = 512
	maxFeatureDescription   = 10000
	planPathPunctuation     = "._-/+@~()[]{}=, "
)

var (
	requirementIDPattern  = regexp.MustCompile(`^(AC|FR|IF)-[0-9]{3,6}$`)
	requirementTagPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	planUUIDPattern       = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	planHarnessPattern    = regexp.MustCompile(`^[a-z0-9._-]{1,32}$`)
	planBranchPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,254}$`)
	planSlugPattern       = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	planRepositoryPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?(:[0-9]{1,5})?(/[A-Za-z0-9_.-]+){2,}$`)
	planRequirementKeys   = map[string]bool{"id": true, "text": true, "tags": true}
)

func normalizePlanRepository(raw string) (string, error) {
	r := strings.TrimSpace(raw)
	lower := strings.ToLower(r)
	switch {
	case strings.HasPrefix(lower, "https://"):
		r = r[len("https://"):]
	case strings.HasPrefix(lower, "http://"):
		r = r[len("http://"):]
	case strings.HasPrefix(lower, "ssh://"):
		r = strings.TrimPrefix(r[len("ssh://"):], "git@")
		if slash := strings.Index(r, "/"); slash > 0 {
			if colon := strings.LastIndex(r[:slash], ":"); colon >= 0 {
				r = r[:colon] + r[slash:]
			}
		}
	case strings.HasPrefix(lower, "git@"):
		r = strings.Replace(r[len("git@"):], ":", "/", 1)
	}
	if strings.Contains(r, "@") {
		return "", errors.New("front matter: repository must not carry credentials")
	}
	r = strings.ToLower(strings.TrimSuffix(strings.TrimSuffix(r, "/"), ".git"))
	if strings.Index(r, "/") <= 0 || len(r) > 255 || !planRepositoryPattern.MatchString(r) || strings.Contains(r, "..") {
		return "", errors.New("front matter: repository must have the form host/owner/name")
	}
	return r, nil
}

func validPlanPath(p string) bool {
	if p == "" || p == "." || len(p) > maxPlanPathBytes || strings.TrimSpace(p) != p || path.Clean(p) != p {
		return false
	}
	if strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\*?\n\r") {
		return false
	}
	if len(p) >= 2 && p[1] == ':' && unicode.IsLetter(rune(p[0])) {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return false
		}
	}
	for _, r := range p {
		if unicode.IsControl(r) || !(unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune(planPathPunctuation, r)) {
			return false
		}
	}
	return true
}

func validPlanBranch(ref string) bool {
	if !planBranchPattern.MatchString(ref) || strings.Contains(ref, "..") || strings.Contains(ref, "//") ||
		strings.HasSuffix(ref, "/") || strings.HasSuffix(ref, ".") {
		return false
	}
	for _, component := range strings.Split(ref, "/") {
		if strings.HasPrefix(component, ".") || strings.HasSuffix(component, ".lock") {
			return false
		}
	}
	return true
}

type planFile struct {
	Product           string         `yaml:"product"`
	Repository        string         `yaml:"repository"`
	BaseBranch        string         `yaml:"base_branch"`
	IntegrationBranch string         `yaml:"integration_branch"`
	Name              string         `yaml:"name"`
	Harness           string         `yaml:"harness"`
	Task              string         `yaml:"task"`
	Items             []planFileItem `yaml:"items"`

	contentHash  string
	repository   string
	requirements map[string]string
}

type planFileItem struct {
	Feature      string                `yaml:"feature"`
	Revision     string                `yaml:"revision"`
	Title        string                `yaml:"title"`
	Component    string                `yaml:"component"`
	Slug         string                `yaml:"slug"`
	ScopeSummary string                `yaml:"scope_summary"`
	NewRevision  bool                  `yaml:"new_revision"`
	Requirements []planFileRequirement `yaml:"requirements"`
	Paths        []string              `yaml:"paths"`
}

type planFileRequirement struct {
	ID   string   `yaml:"id"`
	Text string   `yaml:"text"`
	Tags []string `yaml:"tags"`
}

func (r *planFileRequirement) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		r.ID = strings.TrimSpace(node.Value)
		return nil
	}
	if node.Kind == yaml.MappingNode {
		for i := 0; i < len(node.Content); i += 2 {
			if key := node.Content[i]; !planRequirementKeys[key.Value] {
				return fmt.Errorf("line %d: unknown requirement key %q; a requirement takes id, text and tags", key.Line, key.Value)
			}
		}
	}
	type plain planFileRequirement
	var p plain
	if err := node.Decode(&p); err != nil {
		return err
	}
	*r = planFileRequirement(p)
	r.ID = strings.TrimSpace(r.ID)
	return nil
}

func normalizePlanText(raw []byte) []byte {
	return bytes.ReplaceAll(bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf}), []byte("\r\n"), []byte("\n"))
}

func splitFrontMatter(text []byte) ([]byte, []byte, error) {
	if !bytes.HasPrefix(text, []byte("---\n")) {
		return nil, nil, errors.New("the plan file must start with a YAML front-matter block between two --- lines")
	}
	rest := text[len("---\n"):]
	end := bytes.Index(rest, []byte("\n---\n"))
	if end < 0 {
		if bytes.HasSuffix(rest, []byte("\n---")) {
			return rest[:len(rest)-len("\n---")], nil, nil
		}
		return nil, nil, errors.New("the front-matter block is not closed by a --- line")
	}
	return rest[:end+1], rest[end+len("\n---\n"):], nil
}

func markdownFence(line string) (string, bool) {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 || !strings.HasPrefix(trimmed, "```") && !strings.HasPrefix(trimmed, "~~~") {
		return "", false
	}
	n := 0
	for n < len(trimmed) && trimmed[n] == trimmed[0] {
		n++
	}
	if trimmed[0] == '`' && strings.Contains(trimmed[n:], "`") {
		return "", false
	}
	return trimmed[:n], true
}

func markdownLines(body []byte) []string {
	lines := strings.Split(string(body), "\n")
	open := ""
	for i, line := range lines {
		marker, fence := markdownFence(line)
		switch {
		case open != "":
			if fence && marker[0] == open[0] && len(marker) >= len(open) && strings.TrimSpace(strings.TrimLeft(line, " ")[len(marker):]) == "" {
				open = ""
			}
			lines[i] = ""
		case fence:
			open = marker
			lines[i] = ""
		}
	}
	return lines
}

func markdownHeading(line string) (int, string, bool) {
	trimmed := strings.TrimLeft(line, " ")
	level := len(trimmed) - len(strings.TrimLeft(trimmed, "#"))
	if len(line)-len(trimmed) > 3 || level == 0 || level > 6 {
		return 0, "", false
	}
	rest := trimmed[level:]
	if rest != "" && rest[0] != ' ' && rest[0] != '\t' {
		return 0, "", false
	}
	title := strings.TrimSpace(rest)
	if open := strings.TrimRight(title, "#"); open == "" || strings.HasSuffix(open, " ") || strings.HasSuffix(open, "\t") {
		title = strings.TrimSpace(open)
	}
	return level, title, true
}

func splitTableRow(row string) []string {
	var cells []string
	var cell strings.Builder
	closed := false
	for i := 0; i < len(row); i++ {
		closed = false
		switch {
		case row[i] == '\\' && i+1 < len(row):
			if row[i+1] != '|' {
				cell.WriteByte('\\')
			}
			cell.WriteByte(row[i+1])
			i++
		case row[i] == '|':
			cells = append(cells, cell.String())
			cell.Reset()
			closed = true
		default:
			cell.WriteByte(row[i])
		}
	}
	if !closed {
		cells = append(cells, cell.String())
	}
	if strings.HasPrefix(row, "|") {
		cells = cells[1:]
	}
	return cells
}

func parseRequirementsTable(body []byte) (map[string]string, error) {
	out := map[string]string{}
	section := 0
	header := true
	for _, line := range markdownLines(body) {
		if level, title, ok := markdownHeading(line); ok {
			if section > 0 && level <= section {
				section = 0
			}
			if section == 0 && strings.EqualFold(title, "requirements") {
				section = level
			}
			header = true
			continue
		}
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "|") {
			header = true
			continue
		}
		if section == 0 {
			continue
		}
		cells := splitTableRow(trimmed)
		if len(cells) < 2 {
			continue
		}
		id := strings.Trim(strings.TrimSpace(cells[0]), "`*")
		text := strings.TrimSpace(cells[1])
		if header {
			header = false
			if !requirementIDPattern.MatchString(id) {
				continue
			}
		}
		if strings.Trim(id, "-: ") == "" {
			continue
		}
		if !requirementIDPattern.MatchString(id) {
			return nil, fmt.Errorf("requirements table: %q is not a requirement ID like AC-001, FR-001 or IF-001", id)
		}
		if _, dup := out[id]; dup {
			return nil, fmt.Errorf("requirements table: duplicate requirement ID %s", id)
		}
		if text == "" {
			return nil, fmt.Errorf("requirements table: %s has no requirement text", id)
		}
		out[id] = text
	}
	return out, nil
}

func parsePlanFile(raw []byte) (*planFile, error) {
	if len(raw) > maxPlanFileBytes {
		return nil, fmt.Errorf("the plan file exceeds %d bytes", maxPlanFileBytes)
	}
	text := normalizePlanText(raw)
	front, body, err := splitFrontMatter(text)
	if err != nil {
		return nil, err
	}
	var p planFile
	dec := yaml.NewDecoder(bytes.NewReader(front))
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("front matter: %w", err)
	}
	table, err := parseRequirementsTable(body)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(text)
	p.contentHash = hex.EncodeToString(sum[:])
	p.requirements = table
	if strings.TrimSpace(p.Name) == "" {
		p.Name = firstHeading(body)
	}
	if err := p.validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

func firstHeading(body []byte) string {
	for _, line := range markdownLines(body) {
		if level, title, ok := markdownHeading(line); ok && level == 1 && title != "" {
			return title
		}
	}
	return ""
}

func (p *planFile) validate() error {
	if product := strings.TrimSpace(p.Product); product != "" && !planUUIDPattern.MatchString(product) {
		return errors.New("front matter: product must be a product UUID")
	}
	if strings.TrimSpace(p.Repository) == "" {
		return errors.New("front matter: repository is required")
	}
	repository, err := normalizePlanRepository(p.Repository)
	if err != nil {
		return err
	}
	p.repository = repository
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return errors.New("front matter: name is required when the file has no '# ' heading")
	}
	if len(name) > maxPlanNameBytes {
		return fmt.Errorf("front matter: name must be at most %d bytes", maxPlanNameBytes)
	}
	if h := strings.TrimSpace(p.Harness); h != "" && !planHarnessPattern.MatchString(h) {
		return errors.New("front matter: harness must be a short lowercase name like claude-code")
	}
	if t := strings.TrimSpace(p.Task); t != "" && !planUUIDPattern.MatchString(t) {
		return errors.New("front matter: task must be a task UUID")
	}
	if err := p.validateBranches(); err != nil {
		return err
	}
	if len(p.Items) == 0 || len(p.Items) > maxPlanItems {
		return fmt.Errorf("front matter: items must hold between 1 and %d entries", maxPlanItems)
	}
	if strings.TrimSpace(p.Task) != "" && len(p.Items) > 1 {
		return fmt.Errorf("front matter: task links the work order of a one-item plan, and this plan has %d items; drop task or push the item that implements the task as a plan of its own", len(p.Items))
	}
	seen := map[string]int{}
	targets := map[string]int{}
	totalPaths, totalRequirements := 0, 0
	for i, item := range p.Items {
		n := i + 1
		if err := p.validateItem(n, item, seen); err != nil {
			return err
		}
		target := planItemTarget(item)
		if prev, dup := targets[target]; dup {
			return fmt.Errorf("items %d and %d plan the same feature", prev, n)
		}
		targets[target] = n
		totalPaths += len(item.Paths)
		totalRequirements += len(item.Requirements)
	}
	if totalPaths > maxPlanPaths {
		return fmt.Errorf("front matter: a plan names at most %d paths in total", maxPlanPaths)
	}
	if totalRequirements > maxPlanRequirements {
		return fmt.Errorf("front matter: a plan holds at most %d requirements in total", maxPlanRequirements)
	}
	return nil
}

func (p *planFile) validateBranches() error {
	base := strings.TrimSpace(p.BaseBranch)
	if base == "" {
		base = "main"
	}
	if !validPlanBranch(base) {
		return errors.New("front matter: base_branch is not a valid branch name")
	}
	integration := strings.TrimSpace(p.IntegrationBranch)
	if integration == "" {
		return nil
	}
	if len(p.Items) == 1 {
		if integration != base {
			return errors.New("front matter: a one-item plan integrates into its base branch; drop integration_branch")
		}
		return nil
	}
	if !strings.HasPrefix(integration, "plan/") || integration == base || !validPlanBranch(integration) {
		return errors.New("front matter: integration_branch of a plan with several items must be a branch name starting with plan/ that differs from base_branch")
	}
	return nil
}

func (p *planFile) validateItem(n int, item planFileItem, seen map[string]int) error {
	kinds := 0
	for _, v := range []string{item.Feature, item.Revision, item.Title} {
		if strings.TrimSpace(v) != "" {
			kinds++
		}
	}
	if kinds != 1 {
		return fmt.Errorf("item %d: name exactly one of feature, revision or title", n)
	}
	if feature := strings.TrimSpace(item.Feature); feature != "" && !looksLikeFeatureKey(feature) && !planUUIDPattern.MatchString(feature) {
		return fmt.Errorf("item %d: feature must be a feature key like ZEN-42 or a feature UUID", n)
	}
	if item.Revision != "" && !planUUIDPattern.MatchString(item.Revision) {
		return fmt.Errorf("item %d: revision must be a revision UUID", n)
	}
	if item.Title != "" {
		if !planUUIDPattern.MatchString(item.Component) {
			return fmt.Errorf("item %d: a new feature needs component, the UUID of its component", n)
		}
		if len([]rune(strings.TrimSpace(item.Title))) > 500 {
			return fmt.Errorf("item %d: title must be at most 500 characters", n)
		}
		if slug := planItemSlug(item); len(slug) > 200 || !planSlugPattern.MatchString(slug) {
			return fmt.Errorf("item %d: slug must be up to 200 lowercase letters, digits, dashes or underscores; set slug when the title yields none", n)
		}
	} else if item.Component != "" || item.Slug != "" {
		return fmt.Errorf("item %d: component and slug belong to new features (title)", n)
	}
	if item.NewRevision {
		if item.Feature == "" {
			return fmt.Errorf("item %d: new_revision needs feature", n)
		}
		if strings.TrimSpace(item.ScopeSummary) == "" {
			return fmt.Errorf("item %d: new_revision needs scope_summary", n)
		}
	} else if item.Title == "" && strings.TrimSpace(item.ScopeSummary) != "" {
		return fmt.Errorf("item %d: scope_summary belongs to new features (title) and new revisions (feature with new_revision: true); an existing revision keeps its scope", n)
	}
	if len(item.Paths) > maxPlanItemPaths {
		return fmt.Errorf("item %d: at most %d paths per item", n, maxPlanItemPaths)
	}
	for _, file := range item.Paths {
		if !validPlanPath(file) {
			return fmt.Errorf("item %d: path %q must be a clean relative file path without wildcards", n, file)
		}
	}
	if len(item.Requirements) > maxPlanItemRequirements {
		return fmt.Errorf("item %d: at most %d requirements per item", n, maxPlanItemRequirements)
	}
	for _, r := range item.Requirements {
		if !requirementIDPattern.MatchString(r.ID) {
			return fmt.Errorf("item %d: %q is not a requirement ID like AC-001, FR-001 or IF-001", n, r.ID)
		}
		if prev, dup := seen[r.ID]; dup {
			return fmt.Errorf("requirement %s appears twice in the plan (items %d and %d)", r.ID, prev, n)
		}
		seen[r.ID] = n
		if len(r.Tags) > 10 {
			return fmt.Errorf("item %d: %s has more than 10 tags", n, r.ID)
		}
		for _, tag := range r.Tags {
			if !requirementTagPattern.MatchString(tag) {
				return fmt.Errorf("item %d: tag %q of %s must be a lowercase word", n, tag, r.ID)
			}
		}
		if p.requirementText(r) == "" && (item.Title != "" || item.NewRevision) {
			return fmt.Errorf("item %d: %s has no text; add it to the ## Requirements table or as text", n, r.ID)
		}
	}
	return nil
}

func planItemSlug(item planFileItem) string {
	if s := strings.TrimSpace(item.Slug); s != "" {
		return s
	}
	return slugify(item.Title)
}

func planItemTarget(item planFileItem) string {
	switch {
	case item.Revision != "":
		return "revision:" + strings.ToLower(item.Revision)
	case item.Title != "":
		return "slug:" + planItemSlug(item)
	}
	return "feature:" + strings.ToUpper(strings.TrimSpace(item.Feature))
}

func (p *planFile) requirementText(r planFileRequirement) string {
	if t := strings.TrimSpace(r.Text); t != "" {
		return t
	}
	return p.requirements[r.ID]
}

func (p *planFile) acceptanceCriteria(item planFileItem) []string {
	out := make([]string, 0, len(item.Requirements))
	for _, r := range item.Requirements {
		out = append(out, r.ID+": "+p.requirementText(r))
	}
	return out
}

func (p *planFile) featureDescription(item planFileItem) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(item.ScopeSummary))
	criteria := p.acceptanceCriteria(item)
	if len(criteria) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("Acceptance criteria:\n")
		for _, c := range criteria {
			b.WriteString("- " + c + "\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func requirementPayload(item planFileItem) []map[string]any {
	out := make([]map[string]any, 0, len(item.Requirements))
	for _, r := range item.Requirements {
		tags := r.Tags
		if tags == nil {
			tags = []string{}
		}
		out = append(out, map[string]any{"id": r.ID, "tags": tags})
	}
	return out
}
