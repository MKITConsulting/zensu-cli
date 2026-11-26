package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const workStatusCasesEnv = "ZENSU_WORK_STATUS_CASES"

type vocabularyCopy struct {
	FixtureVersion int                        `json:"fixtureVersion"`
	SourceCommit   string                     `json:"sourceCommit"`
	Vocabulary     map[string]json.RawMessage `json:"vocabulary"`
}

func readVocabularyCopy(t *testing.T) vocabularyCopy {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "work-status-vocabulary.json"))
	if err != nil {
		t.Fatalf("read the vocabulary copy: %v", err)
	}
	var copied vocabularyCopy
	if err := json.Unmarshal(raw, &copied); err != nil {
		t.Fatalf("decode the vocabulary copy: %v", err)
	}
	if copied.FixtureVersion < 1 || len(copied.SourceCommit) != 40 || len(copied.Vocabulary) == 0 {
		t.Fatalf("the vocabulary copy names no fixture version, source commit or vocabulary: version %d, commit %q, %d lists", copied.FixtureVersion, copied.SourceCommit, len(copied.Vocabulary))
	}
	return copied
}

func vocabularyList(t *testing.T, copied vocabularyCopy, key string) []string {
	t.Helper()
	raw, listed := copied.Vocabulary[key]
	if !listed {
		t.Fatalf("the vocabulary copy has no list %q", key)
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("vocabulary list %q: %v", key, err)
	}
	return list
}

func sameMembers(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

func TestProgressVocabulary_EqualsTheContractCopy(t *testing.T) {
	copied := readVocabularyCopy(t)
	for key, cli := range map[string][]string{
		"factKinds":           progressFactKinds,
		"harnesses":           progressHarnesses,
		"sessionEndReasons":   progressSessionEndReasons,
		"phases":              progressPhases,
		"segmentOutcomes":     progressSegmentOutcomes,
		"waitSubjects":        progressWaitSubjects,
		"severities":          progressSeverities,
		"findingStates":       progressFindingStates,
		"changeRequestStates": progressChangeRequestStates,
		"evidenceChecks":      progressEvidenceChecks,
		"evidenceOutcomes":    progressEvidenceOutcomes,
		"blockKinds":          progressBlockKinds,
		"activities":          progressActivities,
		"workEndReasons":      append(slices.Clone(progressWorkEndReasons), progressPersonOnlyWorkEndReason),
	} {
		if contract := vocabularyList(t, copied, key); !sameMembers(cli, contract) {
			t.Errorf("%s: the CLI knows %q, the contract %q", key, cli, contract)
		}
	}

	var outcomes map[string][]string
	if err := json.Unmarshal(copied.Vocabulary["waitSubjectOutcomes"], &outcomes); err != nil || len(outcomes) == 0 {
		t.Fatalf("waitSubjectOutcomes of the vocabulary copy: %v", err)
	}
	if len(outcomes) != len(progressWaitSubjectOutcomes) {
		t.Errorf("wait subjects with outcomes: the CLI knows %d, the contract %d", len(progressWaitSubjectOutcomes), len(outcomes))
	}
	var every []string
	for subject, contract := range outcomes {
		if !sameMembers(progressWaitSubjectOutcomes[subject], contract) {
			t.Errorf("outcomes of a %s wait: the CLI knows %q, the contract %q", subject, progressWaitSubjectOutcomes[subject], contract)
		}
		for _, outcome := range progressWaitSubjectOutcomes[subject] {
			if !slices.Contains(every, outcome) {
				every = append(every, outcome)
			}
		}
	}
	if contract := vocabularyList(t, copied, "waitOutcomes"); !sameMembers(every, contract) {
		t.Errorf("waitOutcomes: the CLI's subjects use %q, the contract lists %q", every, contract)
	}
	if slices.Contains(progressWorkEndReasons, progressPersonOnlyWorkEndReason) {
		t.Error("a session must not be able to report closed_by_person")
	}
}

var docCodeTokenRe = regexp.MustCompile("`([a-z_]+)`")

func aiWorkReferenceSection(t *testing.T) string {
	t.Helper()
	_, section, found := strings.Cut(readSkillDoc(t, "reference.md"), "\n### ai-work\n")
	if !found {
		t.Fatal("reference.md has no ai-work section")
	}
	section, _, _ = strings.Cut(section, "\n### ")
	return section
}

func TestSkillDocs_ReferenceListsEveryProgressKindWithItsFlags(t *testing.T) {
	bullets := map[string]string{}
	var kinds []string
	for _, line := range strings.Split(aiWorkReferenceSection(t), "\n") {
		switch {
		case strings.HasPrefix(line, "- `"):
			kinds = nil
			head, _, _ := strings.Cut(line, ":")
			for _, match := range docCodeTokenRe.FindAllStringSubmatch(head, -1) {
				kinds = append(kinds, match[1])
			}
		case !strings.HasPrefix(line, "  "):
			kinds = nil
		}
		for _, kind := range kinds {
			bullets[kind] += line + "\n"
		}
	}
	for _, kind := range progressFactKinds {
		text, documented := bullets[kind]
		if !documented {
			t.Errorf("reference.md has no list entry for the kind %s", kind)
			continue
		}
		for _, name := range progressPayloadFlagNames() {
			shown := regexp.MustCompile("--" + regexp.QuoteMeta(name) + `\b`).MatchString(text)
			if taken := slices.Contains(progressKindFlags[kind], name); shown != taken {
				t.Errorf("kind %s and --%s: documented %t, taken by the command %t", kind, name, shown, taken)
			}
		}
	}
}

func TestSkillDocs_ReferenceNamesEveryProgressValue(t *testing.T) {
	doc := readSkillDoc(t, "reference.md")
	waits := slices.Clone(progressWaitSubjects)
	for _, outcomes := range progressWaitSubjectOutcomes {
		waits = append(waits, outcomes...)
	}
	for label, values := range map[string][]string{
		"Progress fact kind (`ai-work report`)":  progressFactKinds,
		"Progress harness":                       progressHarnesses,
		"Progress session end reason":            progressSessionEndReasons,
		"Progress phase":                         progressPhases,
		"Progress segment outcome":               progressSegmentOutcomes,
		"Progress wait subject and its outcomes": waits,
		"Progress finding severity / state":      append(slices.Clone(progressSeverities), progressFindingStates...),
		"Progress change request state":          progressChangeRequestStates,
		"Progress evidence check / outcome":      append(slices.Clone(progressEvidenceChecks), progressEvidenceOutcomes...),
		"Progress block kind":                    progressBlockKinds,
		"Progress signal activity":               progressActivities,
		"Progress work end reason":               progressWorkEndReasons,
	} {
		row := regexp.MustCompile(`(?m)^\| ` + regexp.QuoteMeta(label) + ` \| (.+) \|$`).FindStringSubmatch(doc)
		if row == nil {
			t.Errorf("the enum table of reference.md has no row %q", label)
			continue
		}
		var documented []string
		for _, match := range docCodeTokenRe.FindAllStringSubmatch(row[1], -1) {
			documented = append(documented, match[1])
		}
		slices.Sort(documented)
		values = slices.Clone(values)
		slices.Sort(values)
		if documented, values = slices.Compact(documented), slices.Compact(values); !slices.Equal(documented, values) {
			t.Errorf("row %q names %q, the command takes %q", label, documented, values)
		}
	}
}

func TestProgressVocabularyCopy_EqualsTheContractFixture(t *testing.T) {
	path := os.Getenv(workStatusCasesEnv)
	if path == "" {
		t.Skipf("set %s to the path of the backend's work-status contract fixture to compare the vocabulary copy with its source", workStatusCasesEnv)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the contract fixture: %v", err)
	}
	var fixture struct {
		Version    int            `json:"version"`
		Vocabulary map[string]any `json:"vocabulary"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil || len(fixture.Vocabulary) == 0 {
		t.Fatalf("decode the contract fixture: %v (%d vocabulary lists)", err, len(fixture.Vocabulary))
	}
	copied := readVocabularyCopy(t)
	if fixture.Version != copied.FixtureVersion {
		t.Errorf("the fixture is version %d, the copy version %d", fixture.Version, copied.FixtureVersion)
	}
	for key, value := range fixture.Vocabulary {
		rawCopy, listed := copied.Vocabulary[key]
		if !listed {
			t.Errorf("the copy lacks the fixture's list %q", key)
			continue
		}
		var inCopy any
		if err := json.Unmarshal(rawCopy, &inCopy); err != nil {
			t.Fatalf("copy list %q: %v", key, err)
		}
		if !reflect.DeepEqual(inCopy, value) {
			t.Errorf("list %q differs: the copy holds %v, the fixture %v", key, inCopy, value)
		}
	}
	for key := range copied.Vocabulary {
		if _, listed := fixture.Vocabulary[key]; !listed {
			t.Errorf("the copy holds a list %q that the fixture no longer has", key)
		}
	}
}
