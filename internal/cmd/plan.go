package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/MKITConsulting/zensu-cli/internal/client"
)

const (
	maxListPages     = 50
	maxWatchFailures = 5
	maxWatchBackoff  = 2 * time.Minute
)

var (
	workPlanStatuses      = []string{"draft", "decomposing", "graph_proposed", "open", "integrating", "held", "verifying", "final_pr_open", "merged", "abandoned"}
	workPlanStatusChoices = strings.Join(workPlanStatuses[:len(workPlanStatuses)-1], ", ") + " or " + workPlanStatuses[len(workPlanStatuses)-1]
)

var planWatchSleep = func(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type workPlanView struct {
	ID                string          `json:"id"`
	Name              string          `json:"name"`
	Status            string          `json:"status"`
	Repository        string          `json:"repository"`
	BaseBranch        string          `json:"base_branch"`
	IntegrationBranch string          `json:"integration_branch"`
	FinalPRURL        *string         `json:"final_pr_url"`
	SourceRef         json.RawMessage `json:"source_ref"`
	Created           *bool           `json:"created"`
	Orders            []workOrderView `json:"orders"`
}

type pushedFeature struct {
	ID               string  `json:"id"`
	ProductID        string  `json:"product_id"`
	ComponentID      *string `json:"component_id"`
	ParentFeatureID  *string `json:"parent_feature_id"`
	ActiveRevisionID *string `json:"active_revision_id"`
	Slug             string  `json:"slug"`
	Title            string  `json:"title"`
	Stage            string  `json:"stage"`
}

func (ft pushedFeature) label() string {
	return fmt.Sprintf("%q (%s)", sanitizeTerminal(ft.Title), sanitizeTerminal(ft.ID))
}

func (ft pushedFeature) openRevision() bool {
	return ft.ActiveRevisionID != nil && ft.Stage != "" && ft.Stage != "shipped" && ft.Stage != "superseded"
}

func (ft pushedFeature) revisionState() string {
	if ft.ActiveRevisionID == nil || ft.Stage == "" {
		return "missing"
	}
	return sanitizeTerminal(ft.Stage)
}

func planPath(id string, rest ...string) string {
	p := "/api/work-plans/" + url.PathEscape(id)
	for _, r := range rest {
		p += "/" + r
	}
	return p
}

func isTerminalPlanStatus(status string) bool {
	return status == "merged" || status == "abandoned"
}

func writePlan(w io.Writer, p workPlanView) error {
	line := fmt.Sprintf("Plan %s %q: %s (%s → %s)", p.ID, p.Name, p.Status, p.IntegrationBranch, p.BaseBranch)
	if p.FinalPRURL != nil {
		line += ", final PR " + *p.FinalPRURL
	}
	if _, err := fmt.Fprintln(w, sanitizeTerminal(line)); err != nil {
		return err
	}
	if len(p.Orders) == 0 {
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ORDER\tSTATUS\tATTEMPT\tPR")
	for _, o := range p.Orders {
		st := o.Status
		if o.BlockedKind != nil {
			st += "(" + *o.BlockedKind + ")"
		}
		fmt.Fprintln(tw, tableRow(o.ID, st, strconv.Itoa(o.Attempt), strValue(o.PRURL)))
	}
	return tw.Flush()
}

func printPlan(f *Factory, p workPlanView) error {
	return writePlan(f.Out, p)
}

func writeJSON(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return printJSON(w, b)
}

func gitPlanPath(file string) (string, bool) {
	cmd := exec.Command("git", "rev-parse", "--show-prefix")
	cmd.Dir = filepath.Dir(file)
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return path.Join(strings.TrimRight(string(out), "\r\n"), filepath.Base(file)), true
}

func planSourcePath(file string) string {
	if rel, ok := gitPlanPath(file); ok {
		return rel
	}
	clean := filepath.Clean(file)
	if filepath.IsAbs(clean) {
		if wd, err := os.Getwd(); err == nil {
			if rel, err := filepath.Rel(wd, clean); err == nil && !strings.HasPrefix(rel, "..") {
				return filepath.ToSlash(rel)
			}
		}
		return filepath.Base(clean)
	}
	if strings.HasPrefix(clean, "..") {
		return filepath.Base(clean)
	}
	return filepath.ToSlash(clean)
}

func NewPlanCmd(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Push plan files and follow work plans",
		Long: "A work plan groups the work orders of one pushed plan file. `zensu plan push` creates the plan,\n" +
			"its items and any missing features or revisions. Approve, finalize, abandon, confirm-merge and\n" +
			"follow-up decisions need the browser login of `zensu auth login`; API keys get 403.",
	}
	cmd.AddCommand(
		newPlanPushCmd(f),
		newPlanStatusCmd(f),
		newPlanListCmd(f),
		newPlanApproveCmd(f),
		newPlanFinalizeCmd(f),
		newPlanAbandonCmd(f),
		newPlanConfirmMergeCmd(f),
		newPlanFollowupsCmd(f),
		newPlanFollowupCmd(f),
	)
	return cmd
}

func eachPage(ctx context.Context, f *Factory, path string, visit func(json.RawMessage) (bool, error)) error {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	for page := 1; page <= maxListPages; page++ {
		raw, err := f.request(ctx, http.MethodGet, path+sep+"per_page=100&page="+strconv.Itoa(page), nil)
		if err != nil {
			return err
		}
		var env struct {
			Data    []json.RawMessage `json:"data"`
			Total   int               `json:"total"`
			PerPage int               `json:"perPage"`
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			return fmt.Errorf("invalid list response: %w", err)
		}
		for _, item := range env.Data {
			done, err := visit(item)
			if err != nil || done {
				return err
			}
		}
		per := env.PerPage
		if per <= 0 {
			per = len(env.Data)
		}
		if len(env.Data) == 0 || page*per >= env.Total {
			return nil
		}
	}
	return fmt.Errorf("%s has more than %d pages; refusing to decide on a partial list", path, maxListPages)
}

type planSourceRef struct {
	ContentHash string `json:"content_hash"`
	Path        string `json:"path"`
}

func (p workPlanView) source() planSourceRef {
	var src planSourceRef
	_ = json.Unmarshal(p.SourceRef, &src)
	return src
}

func (p workPlanView) label() string {
	return fmt.Sprintf("%s (%q, %s)", sanitizeTerminal(p.ID), sanitizeTerminal(p.Name), sanitizeTerminal(p.Status))
}

func productPlans(ctx context.Context, f *Factory, product, hash string) (*workPlanView, []workPlanView, error) {
	var found *workPlanView
	live := []workPlanView{}
	err := eachPage(ctx, f, "/api/products/"+url.PathEscape(product)+"/work-plans", func(item json.RawMessage) (bool, error) {
		var p workPlanView
		if err := json.Unmarshal(item, &p); err != nil {
			return false, fmt.Errorf("invalid work plan list: %w", err)
		}
		if isTerminalPlanStatus(p.Status) {
			return false, nil
		}
		if p.source().ContentHash == hash {
			found = &p
			return true, nil
		}
		live = append(live, p)
		return false, nil
	})
	return found, live, err
}

func planFeatureRef(ref string) string {
	ref = strings.TrimSpace(ref)
	if key, ok := canonicalFeatureKey(ref); ok {
		return key
	}
	return ref
}

func getFeature(ctx context.Context, f *Factory, ref string) (pushedFeature, error) {
	path := "/api/features/" + url.PathEscape(ref)
	if key, ok := canonicalFeatureKey(ref); ok {
		path = "/api/features/resolve?ref=" + url.QueryEscape(key)
	}
	raw, err := f.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return pushedFeature{}, fmt.Errorf("feature %s: %w", ref, err)
	}
	var ft pushedFeature
	if err := json.Unmarshal(raw, &ft); err != nil || ft.ID == "" {
		return pushedFeature{}, fmt.Errorf("feature %s: unexpected response", ref)
	}
	return ft, nil
}

type planPushResult struct {
	DryRun        bool            `json:"dry_run"`
	AlreadyPushed bool            `json:"already_pushed"`
	Notes         []string        `json:"notes"`
	Request       map[string]any  `json:"request,omitempty"`
	Plan          json.RawMessage `json:"plan,omitempty"`
}

type planPusher struct {
	f          *Factory
	ctx        context.Context
	plan       *planFile
	product    string
	sourcePath string
	dryRun     bool
	asJSON     bool
	notes      []string
	printed    int
	created    []string
	features   []pushedFeature
	components map[string]bool
	live       []workPlanView
}

type planTarget struct {
	n           int
	item        planFileItem
	ref         string
	existing    string
	revision    string
	newFeature  bool
	newRevision bool
}

func (p *planPusher) note(format string, args ...any) {
	p.notes = append(p.notes, fmt.Sprintf(format, args...))
}

func (p *planPusher) flush() error {
	if p.asJSON {
		return nil
	}
	for ; p.printed < len(p.notes); p.printed++ {
		if _, err := fmt.Fprintln(p.f.Out, sanitizeTerminal(p.notes[p.printed])); err != nil {
			return err
		}
	}
	return nil
}

func (p *planPusher) emit(res planPushResult) error {
	res.DryRun = p.dryRun
	res.Notes = append([]string{}, p.notes...)
	return writeJSON(p.f.Out, res)
}

func (p *planPusher) failed(err error) error {
	if len(p.created) == 0 {
		return err
	}
	return fmt.Errorf("%w; created before the failure: %s; pushing the plan again reuses them", err, strings.Join(p.created, ", "))
}

func (p *planPusher) alreadyPushed(existing *workPlanView) error {
	if !p.asJSON {
		_, err := fmt.Fprintln(p.f.Out, sanitizeTerminal(fmt.Sprintf("Already pushed: plan %s (%s) holds this file's content; nothing was created.", existing.ID, existing.Status)))
		return err
	}
	raw, err := p.f.request(p.ctx, http.MethodGet, planPath(existing.ID), nil)
	if err != nil {
		return err
	}
	var detail workPlanView
	if err := json.Unmarshal(raw, &detail); err != nil {
		return fmt.Errorf("invalid work plan response: %w", err)
	}
	return p.emit(planPushResult{AlreadyPushed: true, Plan: raw})
}

func (p *planPusher) productFeatures() ([]pushedFeature, error) {
	if p.features != nil {
		return p.features, nil
	}
	features := []pushedFeature{}
	err := eachPage(p.ctx, p.f, "/api/features?productId="+url.QueryEscape(p.product), func(item json.RawMessage) (bool, error) {
		var ft pushedFeature
		if err := json.Unmarshal(item, &ft); err != nil {
			return false, fmt.Errorf("invalid feature list: %w", err)
		}
		features = append(features, ft)
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	p.features = features
	return features, nil
}

func (p *planPusher) productComponents() (map[string]bool, error) {
	if p.components != nil {
		return p.components, nil
	}
	components := map[string]bool{}
	err := eachPage(p.ctx, p.f, "/api/products/"+url.PathEscape(p.product)+"/components", func(item json.RawMessage) (bool, error) {
		var c struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(item, &c); err != nil {
			return false, fmt.Errorf("invalid component list: %w", err)
		}
		components[strings.ToLower(c.ID)] = true
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	p.components = components
	return components, nil
}

func (p *planPusher) checkRepository() error {
	raw, err := p.f.request(p.ctx, http.MethodGet, "/api/products/"+url.PathEscape(p.product)+"/repositories", nil)
	if err != nil {
		return fmt.Errorf("repositories of product %s: %w", p.product, err)
	}
	var env struct {
		Data []struct {
			Repository string `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("invalid repository list: %w", err)
	}
	for _, r := range env.Data {
		if r.Repository == p.plan.repository {
			return nil
		}
	}
	return fmt.Errorf("front matter: repository %s is not registered for product %s; register it with `zensu work repositories add` before pushing the plan", p.plan.repository, p.product)
}

func (p *planPusher) lookupAll() ([]planTarget, error) {
	if err := p.checkRepository(); err != nil {
		return nil, err
	}
	if task := strings.TrimSpace(p.plan.Task); task != "" {
		if _, err := p.f.request(p.ctx, http.MethodGet, "/api/tasks/"+url.PathEscape(task), nil); err != nil {
			return nil, fmt.Errorf("front matter: task %s: %w", task, err)
		}
	}
	targets := make([]planTarget, 0, len(p.plan.Items))
	owners := map[string]int{}
	for i, item := range p.plan.Items {
		t, err := p.lookup(i+1, item)
		if err != nil {
			return nil, err
		}
		if t.existing != "" {
			key := strings.ToLower(t.existing)
			if prev, dup := owners[key]; dup {
				return nil, fmt.Errorf("items %d and %d plan the same feature %s", prev, t.n, sanitizeTerminal(t.existing))
			}
			owners[key] = t.n
		}
		targets = append(targets, t)
	}
	return targets, nil
}

func (p *planPusher) lookup(n int, item planFileItem) (planTarget, error) {
	t := planTarget{n: n, item: item}
	switch {
	case item.Revision != "":
		return t, p.lookupRevision(&t)
	case item.Title != "":
		return t, p.lookupTitle(&t)
	}
	return t, p.lookupFeature(&t)
}

func (p *planPusher) lookupRevision(t *planTarget) error {
	features, err := p.productFeatures()
	if err != nil {
		return fmt.Errorf("item %d: %w", t.n, err)
	}
	for _, ft := range features {
		if ft.ActiveRevisionID == nil || !strings.EqualFold(*ft.ActiveRevisionID, t.item.Revision) {
			continue
		}
		switch {
		case ft.ParentFeatureID != nil:
			return fmt.Errorf("item %d: revision %s belongs to subfeature %s; plan its parent feature", t.n, t.item.Revision, ft.label())
		case !ft.openRevision():
			return fmt.Errorf("item %d: revision %s of feature %s is %s; plan the feature with new_revision and a scope_summary", t.n, t.item.Revision, ft.label(), ft.revisionState())
		}
		t.existing = ft.ID
		t.revision = *ft.ActiveRevisionID
		p.ignoredTexts(t, "revision "+t.item.Revision)
		return nil
	}
	return fmt.Errorf("item %d: revision %s is not the active revision of a feature of this product", t.n, t.item.Revision)
}

func (p *planPusher) lookupTitle(t *planTarget) error {
	features, err := p.productFeatures()
	if err != nil {
		return fmt.Errorf("item %d: %w", t.n, err)
	}
	slug := planItemSlug(t.item)
	for _, ft := range features {
		if ft.Slug != slug {
			continue
		}
		switch {
		case ft.ParentFeatureID != nil:
			return fmt.Errorf("item %d: slug %s belongs to subfeature %s; set slug to name a new feature", t.n, slug, ft.label())
		case !strings.EqualFold(strValue(ft.ComponentID), t.item.Component):
			return fmt.Errorf("item %d: slug %s belongs to feature %s in another component; set slug to name a new feature, or plan that feature by its ID", t.n, slug, ft.label())
		case !ft.openRevision():
			return fmt.Errorf("item %d: slug %s belongs to feature %s, whose active revision is %s; plan that feature with new_revision and a scope_summary, or set slug to name a new feature", t.n, slug, ft.label(), ft.revisionState())
		}
		t.existing = ft.ID
		t.revision = *ft.ActiveRevisionID
		p.note("item %d: reusing feature %s (%s)", t.n, slug, sanitizeTerminal(ft.ID))
		p.ignoredTexts(t, "feature "+slug)
		return nil
	}
	components, err := p.productComponents()
	if err != nil {
		return fmt.Errorf("item %d: %w", t.n, err)
	}
	if !components[strings.ToLower(t.item.Component)] {
		return fmt.Errorf("item %d: component %s is not a component of product %s", t.n, t.item.Component, p.product)
	}
	if n := utf8.RuneCountInString(p.plan.featureDescription(t.item)); n > maxFeatureDescription {
		return fmt.Errorf("item %d: the feature description built from scope_summary and the requirement texts has %d characters; Zensu accepts at most %d", t.n, n, maxFeatureDescription)
	}
	t.newFeature = true
	return nil
}

func (p *planPusher) lookupFeature(t *planTarget) error {
	t.ref = planFeatureRef(t.item.Feature)
	ft, err := getFeature(p.ctx, p.f, t.ref)
	if err != nil {
		return fmt.Errorf("item %d: %w", t.n, err)
	}
	switch {
	case ft.ProductID != "" && !strings.EqualFold(ft.ProductID, p.product):
		return fmt.Errorf("item %d: feature %s belongs to another product", t.n, t.ref)
	case ft.ParentFeatureID != nil:
		return fmt.Errorf("item %d: %s is a subfeature; plan its parent feature", t.n, t.ref)
	case !t.item.NewRevision && !ft.openRevision():
		return fmt.Errorf("item %d: the active revision of %s is %s; set new_revision with a scope_summary to plan a new revision", t.n, t.ref, ft.revisionState())
	}
	t.existing = ft.ID
	switch {
	case !t.item.NewRevision:
		t.revision = *ft.ActiveRevisionID
		p.ignoredTexts(t, t.ref)
	case ft.openRevision():
		t.revision = *ft.ActiveRevisionID
		p.note("item %d: %s already has an open revision (%s); the plan targets it", t.n, t.ref, ft.revisionState())
		p.ignoredTexts(t, "the open revision of "+t.ref)
	default:
		t.newRevision = true
	}
	return nil
}

func (p *planPusher) ignoredTexts(t *planTarget, subject string) {
	var ids []string
	for _, r := range t.item.Requirements {
		if p.plan.requirementText(r) != "" {
			ids = append(ids, r.ID)
		}
	}
	if len(ids) > 0 {
		p.note("item %d: %s already exists, so the texts of %s are ignored; the plan records only requirement IDs and tags", t.n, subject, strings.Join(ids, ", "))
	}
}

func (p *planPusher) liveHolders(targets []planTarget) (map[string]workPlanView, error) {
	wanted := map[string]bool{}
	for _, t := range targets {
		if t.revision != "" {
			wanted[strings.ToLower(t.revision)] = true
		}
	}
	holders := map[string]workPlanView{}
	if len(wanted) == 0 {
		return holders, nil
	}
	for _, lp := range p.live {
		raw, err := p.f.request(p.ctx, http.MethodGet, planPath(lp.ID), nil)
		if err != nil {
			return nil, fmt.Errorf("live plan %s: %w", sanitizeTerminal(lp.ID), err)
		}
		var detail struct {
			Items []struct {
				FeatureRevisionID string  `json:"feature_revision_id"`
				ClosedAt          *string `json:"closed_at"`
			} `json:"items"`
		}
		if err := json.Unmarshal(raw, &detail); err != nil {
			return nil, fmt.Errorf("invalid work plan response: %w", err)
		}
		for _, it := range detail.Items {
			rev := strings.ToLower(it.FeatureRevisionID)
			if _, taken := holders[rev]; it.ClosedAt == nil && wanted[rev] && !taken {
				holders[rev] = lp
			}
		}
	}
	return holders, nil
}

func (p *planPusher) checkLivePlans(targets []planTarget) error {
	holders, err := p.liveHolders(targets)
	if err != nil {
		return err
	}
	var conflicts []string
	for _, t := range targets {
		if lp, held := holders[strings.ToLower(t.revision)]; held && t.revision != "" {
			conflicts = append(conflicts, fmt.Sprintf("item %d: revision %s is an open item of live plan %s; a revision sits in one live plan at a time, so abandon that plan first with `zensu plan abandon %s`", t.n, sanitizeTerminal(t.revision), lp.label(), sanitizeTerminal(lp.ID)))
		}
	}
	var edited []workPlanView
	for _, lp := range p.live {
		if src := lp.source(); src.Path == p.sourcePath && src.ContentHash != p.plan.contentHash {
			edited = append(edited, lp)
		}
	}
	if len(conflicts) > 0 {
		for _, lp := range edited {
			conflicts = append(conflicts, p.editedFile(lp))
		}
		return errors.New(strings.Join(conflicts, "; "))
	}
	for _, lp := range edited {
		p.note("%s; abandon it with `zensu plan abandon %s` if this push replaces it", p.editedFile(lp), sanitizeTerminal(lp.ID))
	}
	return nil
}

func (p *planPusher) editedFile(lp workPlanView) string {
	return fmt.Sprintf("live plan %s was pushed from %s with other content, so this push re-pushes an edited file", lp.label(), p.sourcePath)
}

func (p *planPusher) checkFeatureAllowance(targets []planTarget) error {
	creates := 0
	for _, t := range targets {
		if t.newFeature {
			creates++
		}
	}
	if creates == 0 {
		return nil
	}
	raw, err := p.f.request(p.ctx, http.MethodGet, "/api/billing/usage", nil)
	if e, ok := apiErrorOf(err); ok && (e.status == http.StatusForbidden || e.status == http.StatusNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("feature allowance: %w", err)
	}
	var usage struct {
		Limits struct {
			MaxFeatures *int `json:"maxFeatures"`
		} `json:"limits"`
		Usage struct {
			Features int `json:"features"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &usage); err != nil {
		return fmt.Errorf("invalid billing usage response: %w", err)
	}
	limit := usage.Limits.MaxFeatures
	if limit == nil || *limit < 0 || usage.Usage.Features+creates <= *limit {
		return nil
	}
	return fmt.Errorf("the push would create %d feature(s), but the organization's plan allows %d features and %d exist already; nothing was created", creates, *limit, usage.Usage.Features)
}

func (p *planPusher) apply(t planTarget) (map[string]any, error) {
	paths := t.item.Paths
	if paths == nil {
		paths = []string{}
	}
	out := map[string]any{"requirements": requirementPayload(t.item), "paths": paths}
	switch {
	case t.item.Revision != "":
		out["featureRevisionId"] = t.item.Revision
	case t.newFeature:
		id, err := p.createFeature(t)
		if err != nil {
			return nil, err
		}
		out["featureId"] = id
	case t.newRevision:
		id, err := p.createRevision(t)
		if err != nil {
			return nil, err
		}
		if id == "" {
			out["featureId"] = t.existing
		} else {
			out["featureRevisionId"] = id
		}
	default:
		out["featureId"] = t.existing
	}
	return out, nil
}

func (p *planPusher) createRevision(t planTarget) (string, error) {
	if p.dryRun {
		p.note("item %d: would create a new revision of %s", t.n, t.ref)
		return "", nil
	}
	raw, err := p.f.postJSON(p.ctx, "/api/features/"+url.PathEscape(t.existing)+"/revisions", map[string]any{
		"scopeSummary":       strings.TrimSpace(t.item.ScopeSummary),
		"acceptanceCriteria": p.plan.acceptanceCriteria(t.item),
	})
	if err != nil {
		return "", fmt.Errorf("item %d: create a revision of %s: %w", t.n, t.ref, err)
	}
	var rev struct {
		ID      string `json:"id"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &rev); err != nil || rev.ID == "" {
		return "", fmt.Errorf("item %d: unexpected revision response", t.n)
	}
	version := sanitizeTerminal(rev.Version)
	p.created = append(p.created, fmt.Sprintf("revision %s of %s", version, t.ref))
	p.note("item %d: created revision %s of %s", t.n, version, t.ref)
	return rev.ID, nil
}

func (p *planPusher) createFeature(t planTarget) (string, error) {
	slug := planItemSlug(t.item)
	title := strings.TrimSpace(t.item.Title)
	if p.dryRun {
		p.note("item %d: would create feature %q (%s)", t.n, title, slug)
		return "", nil
	}
	payload := map[string]any{
		"productId":   p.product,
		"componentId": t.item.Component,
		"slug":        slug,
		"title":       title,
		"createdBy":   "api",
	}
	if d := p.plan.featureDescription(t.item); d != "" {
		payload["description"] = d
	}
	raw, err := p.f.postJSON(p.ctx, "/api/features", payload)
	if err != nil {
		return "", fmt.Errorf("item %d: create feature %q: %w", t.n, title, err)
	}
	var ft struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &ft); err != nil || ft.ID == "" {
		return "", fmt.Errorf("item %d: unexpected feature response", t.n)
	}
	id := sanitizeTerminal(ft.ID)
	p.created = append(p.created, fmt.Sprintf("feature %q (%s)", title, id))
	p.note("item %d: created feature %q (%s)", t.n, title, id)
	return ft.ID, nil
}

func (p *planPusher) request(sourcePath string, items []map[string]any) map[string]any {
	payload := map[string]any{
		"name":        p.plan.Name,
		"repository":  p.plan.Repository,
		"sourceKind":  "plan_file",
		"sourcePath":  sourcePath,
		"contentHash": p.plan.contentHash,
		"createdVia":  "cli",
		"items":       items,
	}
	for k, v := range map[string]string{"baseBranch": p.plan.BaseBranch, "integrationBranch": p.plan.IntegrationBranch, "preferredHarness": p.plan.Harness, "taskId": p.plan.Task} {
		if strings.TrimSpace(v) != "" {
			payload[k] = strings.TrimSpace(v)
		}
	}
	return payload
}

func runPlanPush(ctx context.Context, f *Factory, file, product string, dryRun, asJSON bool) error {
	raw, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	plan, err := parsePlanFile(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	sourcePath := planSourcePath(file)
	if !validPlanPath(sourcePath) {
		return fmt.Errorf("%s: rename the plan file; Zensu records its path %q, which must use only letters, digits, spaces and ._-+@~()[]{}=, characters", file, sourcePath)
	}
	if product != "" {
		if err := requireUUIDFlag("product", product); err != nil {
			return err
		}
	} else if product = strings.TrimSpace(plan.Product); product == "" {
		return fmt.Errorf("the product must be a product UUID (front matter product or --product)")
	}
	if !dryRun {
		c, err := f.NewClient(ctx)
		if err != nil {
			return err
		}
		if mode := c.AuthMode(); mode == client.AuthModeAPIKey || mode == client.AuthModeSessionToken {
			return fmt.Errorf("zensu plan push needs the browser login of `zensu auth login`; API keys and session tokens cannot create work plans, so nothing was created")
		}
	}
	p := &planPusher{f: f, ctx: ctx, plan: plan, product: product, sourcePath: sourcePath, dryRun: dryRun, asJSON: asJSON}
	existing, live, err := productPlans(ctx, f, product, plan.contentHash)
	if err != nil {
		return err
	}
	if existing != nil {
		return p.alreadyPushed(existing)
	}
	p.live = live
	targets, err := p.lookupAll()
	if err != nil {
		return err
	}
	if err := p.checkLivePlans(targets); err != nil {
		return err
	}
	if err := p.checkFeatureAllowance(targets); err != nil {
		return err
	}
	if err := p.flush(); err != nil {
		return err
	}
	items := make([]map[string]any, 0, len(targets))
	for _, t := range targets {
		resolved, err := p.apply(t)
		if err != nil {
			return p.failed(err)
		}
		items = append(items, resolved)
		if err := p.flush(); err != nil {
			return err
		}
	}
	payload := p.request(sourcePath, items)
	if dryRun && asJSON {
		return p.emit(planPushResult{Request: payload})
	}
	if dryRun {
		return writeJSON(f.Out, payload)
	}
	resp, err := f.postJSON(ctx, "/api/products/"+url.PathEscape(product)+"/work-plans", payload)
	if err != nil {
		return p.failed(err)
	}
	var created workPlanView
	if err := json.Unmarshal(resp, &created); err != nil {
		return fmt.Errorf("invalid work plan response: %w", err)
	}
	already := created.Created != nil && !*created.Created
	if asJSON {
		return p.emit(planPushResult{AlreadyPushed: already, Plan: resp})
	}
	verb := "Pushed"
	if already {
		verb = "Already pushed"
	}
	if _, err := fmt.Fprintln(f.Out, sanitizeTerminal(fmt.Sprintf("%s plan %s with %d work order(s).", verb, created.ID, len(created.Orders)))); err != nil {
		return err
	}
	return printPlan(f, created)
}

func newPlanPushCmd(f *Factory) *cobra.Command {
	var product string
	var dryRun, asJSON bool
	cmd := &cobra.Command{
		Use:   "push <plan-file>",
		Short: "Create a work plan from a Markdown plan file",
		Long: "Create a work plan from a Markdown plan file. The file starts with a YAML front-matter block:\n\n" +
			"  ---\n" +
			"  product: <product UUID>          # or --product\n" +
			"  repository: https://github.com/acme/app   # registered for the product\n" +
			"  base_branch: main\n" +
			"  name: Checkout hardening         # default: the first '# ' heading\n" +
			"  items:\n" +
			"    - feature: ZEN-42              # KEY-N or UUID; the plan targets its active revision\n" +
			"      requirements: [AC-001, {id: AC-002, tags: [payment]}]\n" +
			"      paths: [backend/internal/billing/refunds.go]   # files, no wildcards\n" +
			"    - feature: ZEN-7               # shipped feature: plan a new revision\n" +
			"      new_revision: true\n" +
			"      scope_summary: Refund partial captures\n" +
			"      requirements: [FR-001]\n" +
			"    - title: Refund webhook        # new feature\n" +
			"      component: <component UUID>\n" +
			"      scope_summary: Accept refund webhooks\n" +
			"      requirements: [AC-003]\n" +
			"  ---\n\n" +
			"Requirement texts come from the '## Requirements' table (| ID | Requirement | ... |) or from 'text' in the\n" +
			"front matter. Requirement IDs must be unique across the plan. scope_summary belongs to new features (title)\n" +
			"and new revisions (new_revision: true), and task (a task UUID) only to a one-item plan. The push is idempotent\n" +
			"on the file's SHA-256: pushing the same content again returns the live plan without creating anything.\n" +
			"Before its first write it refuses a revision that is an open item of another live plan (abandon that plan\n" +
			"first with `zensu plan abandon <plan id>`) and new features beyond the organization's feature allowance.\n" +
			"Needs the browser login of `zensu auth login`.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPlanPush(cmd.Context(), f, args[0], product, dryRun, asJSON)
		},
	}
	cmd.Flags().StringVar(&product, "product", "", "product UUID (overrides the front matter)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "validate and resolve the file, print the request, create nothing")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func transientPollError(err error) bool {
	if e, ok := apiErrorOf(err); ok {
		return e.status == http.StatusTooManyRequests || e.status >= 500
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	for _, errno := range transientErrnos {
		if errors.Is(err, errno) {
			return true
		}
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func watchBackoff(interval time.Duration, failures int) time.Duration {
	limit := max(interval, maxWatchBackoff)
	wait := interval
	for i := 1; i < failures && wait < limit; i++ {
		wait *= 2
	}
	return min(wait, limit)
}

func showPlanChange(f *Factory, raw []byte, last *string, asJSON bool) (bool, error) {
	var p workPlanView
	if err := json.Unmarshal(raw, &p); err != nil {
		return false, fmt.Errorf("invalid work plan response: %w", err)
	}
	done := isTerminalPlanStatus(p.Status)
	var view bytes.Buffer
	_ = writePlan(&view, p)
	if view.String() == *last {
		return done, nil
	}
	*last = view.String()
	if asJSON {
		return done, printJSON(f.Out, raw)
	}
	_, err := f.Out.Write(view.Bytes())
	return done, err
}

func watchPlan(ctx context.Context, f *Factory, stderr io.Writer, id string, interval time.Duration, watch, asJSON bool) error {
	last := ""
	failures := 0
	for {
		raw, err := f.request(ctx, http.MethodGet, planPath(id), nil)
		wait := interval
		switch {
		case err == nil:
			failures = 0
			done, err := showPlanChange(f, raw, &last, asJSON)
			if err != nil || !watch || done {
				return err
			}
		case watch && errors.Is(ctx.Err(), context.Canceled):
			return nil
		case !watch || ctx.Err() != nil || !transientPollError(err):
			return err
		default:
			failures++
			if failures == maxWatchFailures {
				return fmt.Errorf("stopped watching after %d failed polls in a row: %w", failures, err)
			}
			wait = watchBackoff(interval, failures)
			fmt.Fprintf(stderr, "poll %d of %d failed: %s; retrying in %s\n", failures, maxWatchFailures, sanitizeTerminal(err.Error()), wait)
		}
		if err := planWatchSleep(ctx, wait); err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}
	}
}

func newPlanStatusCmd(f *Factory) *cobra.Command {
	var watch, asJSON bool
	var interval time.Duration
	cmd := &cobra.Command{
		Use:   "status <plan-id>",
		Short: "Show a work plan and its orders; --watch follows it until it is merged or abandoned",
		Long: "Show a work plan and its orders. With --watch the command polls until the plan is merged or abandoned and\n" +
			"prints every change. A poll that times out, meets a refused or reset connection, loses the response\n" +
			"mid-body or gets 429 or 5xx is retried with backoff, and each retry prints one line to stderr; five failed\n" +
			"polls in a row end the watch. Any other error ends it at once, among them TLS and certificate failures,\n" +
			"a refused redirect and an invalid API URL.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if interval < time.Second {
				return fmt.Errorf("--interval must be at least 1s")
			}
			return watchPlan(cmd.Context(), f, cmd.ErrOrStderr(), args[0], interval, watch, asJSON)
		},
	}
	cmd.Flags().BoolVar(&watch, "watch", false, "poll until the plan is merged or abandoned, printing every change; transient failures are retried with backoff")
	cmd.Flags().DurationVar(&interval, "interval", 10*time.Second, "poll interval for --watch")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newPlanListCmd(f *Factory) *cobra.Command {
	var product, status string
	var page, perPage int
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "list",
		Short:        "List the work plans of a product",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireWorkProduct(product); err != nil {
				return err
			}
			if status != "" && !slices.Contains(workPlanStatuses, status) {
				return fmt.Errorf("--status must be one of %s, got %q", workPlanStatusChoices, sanitizeTerminal(status))
			}
			q := url.Values{}
			if status != "" {
				q.Set("status", status)
			}
			if page > 0 {
				q.Set("page", strconv.Itoa(page))
			}
			if perPage > 0 {
				q.Set("per_page", strconv.Itoa(perPage))
			}
			path := "/api/products/" + url.PathEscape(product) + "/work-plans"
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
				Data  []workPlanView `json:"data"`
				Total int            `json:"total"`
			}
			if err := json.Unmarshal(raw, &env); err != nil {
				return fmt.Errorf("invalid work plan list: %w", err)
			}
			tw := tabwriter.NewWriter(f.Out, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tSTATUS\tNAME\tREPOSITORY")
			for _, p := range env.Data {
				fmt.Fprintln(tw, tableRow(p.ID, p.Status, p.Name, p.Repository))
			}
			fmt.Fprintf(tw, "%d of %d\n", len(env.Data), env.Total)
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&product, "product", "", "product ID (required)")
	cmd.Flags().StringVar(&status, "status", "", "filter by status: "+workPlanStatusChoices)
	cmd.Flags().IntVar(&page, "page", 0, "page number")
	cmd.Flags().IntVar(&perPage, "per-page", 0, "items per page")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func planDecision(f *Factory, cmd *cobra.Command, id, action string, payload map[string]any, asJSON bool) error {
	raw, err := f.postJSON(cmd.Context(), planPath(id, action), payload)
	if err != nil {
		return err
	}
	if asJSON {
		return printJSON(f.Out, raw)
	}
	var p workPlanView
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("invalid work plan response: %w", err)
	}
	return printPlan(f, p)
}

func newPlanApproveCmd(f *Factory) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "approve <plan-id>",
		Short:        "Approve a draft work plan and queue its draft orders",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return planDecision(f, cmd, args[0], "approve", nil, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newPlanAbandonCmd(f *Factory) *cobra.Command {
	var confirmPRClosed, asJSON bool
	cmd := &cobra.Command{
		Use:          "abandon <plan-id>",
		Short:        "Abandon a work plan and cancel its open orders",
		Long:         "Abandon a work plan and cancel its open orders. When orders have open PRs the backend refuses until --confirm-pr-closed confirms that they will be closed.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return planDecision(f, cmd, args[0], "abandon", map[string]any{"confirmPrClosed": confirmPRClosed}, asJSON)
		},
	}
	cmd.Flags().BoolVar(&confirmPRClosed, "confirm-pr-closed", false, "confirm that the plan's open PRs will be closed")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newPlanConfirmMergeCmd(f *Factory) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "confirm-merge <plan-id>",
		Short:        "Confirm that the plan's final PR was merged (manual merge mode)",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return planDecision(f, cmd, args[0], "confirm-merge", nil, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newPlanFinalizeCmd(f *Factory) *cobra.Command {
	var prURL, headSHA string
	var prNumber int
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "finalize <plan-id>",
		Short:        "Record the final PR of a plan, or print its compare link",
		Long:         "Finalize a work plan once its packages are merged into the integration branch. Without --pr-url the response carries the compare link for opening the final PR; with --pr-url, --pr-number and --head-sha it records the PR.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			payload := map[string]any{}
			if prURL != "" {
				payload["prUrl"] = prURL
			}
			if cmd.Flags().Changed("pr-number") {
				payload["prNumber"] = prNumber
			}
			if headSHA != "" {
				payload["headSha"] = headSHA
			}
			raw, err := f.postJSON(cmd.Context(), planPath(args[0], "finalize"), payload)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(f.Out, raw)
			}
			var res struct {
				Plan         workPlanView `json:"plan"`
				CompareURL   string       `json:"compare_url"`
				RecordedPR   bool         `json:"recorded_pr"`
				OpenOrders   int          `json:"open_orders"`
				MergedOrders int          `json:"merged_orders"`
			}
			if err := json.Unmarshal(raw, &res); err != nil {
				return fmt.Errorf("invalid finalize response: %w", err)
			}
			line := fmt.Sprintf("Plan %s: %s; %d merged, %d open order(s)", res.Plan.ID, res.Plan.Status, res.MergedOrders, res.OpenOrders)
			if res.RecordedPR {
				line += "; recorded final PR " + strValue(res.Plan.FinalPRURL)
			} else if res.CompareURL != "" {
				line += "; open the final PR at " + res.CompareURL
			}
			_, err = fmt.Fprintln(f.Out, sanitizeTerminal(line))
			return err
		},
	}
	cmd.Flags().StringVar(&prURL, "pr-url", "", "link of the final PR")
	cmd.Flags().IntVar(&prNumber, "pr-number", 0, "number of the final PR")
	cmd.Flags().StringVar(&headSHA, "head-sha", "", "full head SHA of the final PR")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
	return cmd
}

func newPlanFollowupsCmd(f *Factory) *cobra.Command {
	var page, perPage int
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "followups <plan-id>",
		Short:        "List the follow-ups the plan's sessions reported",
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
			path := planPath(args[0], "followups")
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
					ID       string `json:"id"`
					Title    string `json:"title"`
					Severity string `json:"severity"`
					State    string `json:"state"`
				} `json:"data"`
				Total int `json:"total"`
			}
			if err := json.Unmarshal(raw, &env); err != nil {
				return fmt.Errorf("invalid follow-up list: %w", err)
			}
			tw := tabwriter.NewWriter(f.Out, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tSTATE\tSEVERITY\tTITLE")
			for _, fu := range env.Data {
				fmt.Fprintln(tw, tableRow(fu.ID, fu.State, fu.Severity, fu.Title))
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

func newPlanFollowupCmd(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "followup",
		Short: "Accept or dismiss a reported follow-up",
	}
	for _, decision := range []string{"accept", "dismiss"} {
		action := decision
		var asJSON bool
		sub := &cobra.Command{
			Use:          action + " <plan-id> <followup-id>",
			Short:        strings.ToUpper(action[:1]) + action[1:] + " a follow-up of a work plan",
			Args:         cobra.ExactArgs(2),
			SilenceUsage: true,
			RunE: func(cmd *cobra.Command, args []string) error {
				raw, err := f.request(cmd.Context(), http.MethodPost, planPath(args[0], "followups", url.PathEscape(args[1]), action), nil)
				if err != nil {
					return err
				}
				if asJSON {
					return printJSON(f.Out, raw)
				}
				var fu struct {
					ID    string `json:"id"`
					State string `json:"state"`
				}
				if err := json.Unmarshal(raw, &fu); err != nil {
					return fmt.Errorf("invalid follow-up response: %w", err)
				}
				_, err = fmt.Fprintln(f.Out, sanitizeTerminal(fmt.Sprintf("Follow-up %s is %s", fu.ID, fu.State)))
				return err
			},
		}
		sub.Flags().BoolVar(&asJSON, "json", false, "output raw JSON")
		cmd.AddCommand(sub)
	}
	return cmd
}
