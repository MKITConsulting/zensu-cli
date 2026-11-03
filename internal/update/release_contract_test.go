package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

type goreleaserConfig struct {
	ProjectName string `yaml:"project_name"`
	Builds      []struct {
		Binary string   `yaml:"binary"`
		Goos   []string `yaml:"goos"`
		Goarch []string `yaml:"goarch"`
		Ignore []struct {
			Goos   string `yaml:"goos"`
			Goarch string `yaml:"goarch"`
		} `yaml:"ignore"`
	} `yaml:"builds"`
	Archives []struct {
		NameTemplate    string   `yaml:"name_template"`
		Formats         []string `yaml:"formats"`
		FormatOverrides []struct {
			Goos    string   `yaml:"goos"`
			Formats []string `yaml:"formats"`
		} `yaml:"format_overrides"`
	} `yaml:"archives"`
	Checksum struct {
		NameTemplate string `yaml:"name_template"`
		Algorithm    string `yaml:"algorithm"`
	} `yaml:"checksum"`
	Signs []struct {
		Artifacts string `yaml:"artifacts"`
		Signature string `yaml:"signature"`
	} `yaml:"signs"`
}

func readRepoFile(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func renderName(t *testing.T, tmpl, project, ver, goos, goarch string) string {
	t.Helper()
	name := strings.NewReplacer("{{ .ProjectName }}", project, "{{ .Version }}", ver, "{{ .Os }}", goos, "{{ .Arch }}", goarch).Replace(tmpl)
	if strings.Contains(name, "{{") {
		t.Fatalf("name template %q uses fields this contract test cannot render", tmpl)
	}
	return name
}

func TestReleaseAssetNamesMatchTheGoreleaserConfig(t *testing.T) {
	var cfg goreleaserConfig
	if err := yaml.Unmarshal(readRepoFile(t, ".goreleaser.yaml"), &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Builds) != 1 || len(cfg.Archives) != 1 {
		t.Fatalf("want one build and one archive in .goreleaser.yaml, got %d and %d", len(cfg.Builds), len(cfg.Archives))
	}
	build, archive := cfg.Builds[0], cfg.Archives[0]
	if build.Binary != "zensu" {
		t.Errorf("build binary = %q, but zensu update extracts zensu%s", build.Binary, exeSuffix())
	}
	const ver = "1.2.3"
	if got, want := checksumsName(ver), renderName(t, cfg.Checksum.NameTemplate, cfg.ProjectName, ver, "", ""); got != want {
		t.Errorf("checksums name = %q, goreleaser publishes %q", got, want)
	}
	if cfg.Checksum.Algorithm != "sha256" {
		t.Errorf("checksum algorithm = %q, but zensu update verifies SHA-256", cfg.Checksum.Algorithm)
	}
	if len(cfg.Signs) != 1 || cfg.Signs[0].Artifacts != "checksum" || cfg.Signs[0].Signature != "" {
		t.Errorf("signs = %+v, but zensu update downloads the checksums file plus goreleaser's default .sig name", cfg.Signs)
	}
	format := func(goos string) string {
		for _, o := range archive.FormatOverrides {
			if o.Goos == goos && len(o.Formats) == 1 {
				return o.Formats[0]
			}
		}
		if len(archive.Formats) != 1 {
			t.Fatalf("archive formats = %v, want exactly one default format", archive.Formats)
		}
		return archive.Formats[0]
	}
	ignored := func(goos, goarch string) bool {
		for _, i := range build.Ignore {
			if i.Goos == goos && i.Goarch == goarch {
				return true
			}
		}
		return false
	}
	published := map[string]bool{}
	for _, goos := range build.Goos {
		for _, goarch := range build.Goarch {
			if ignored(goos, goarch) {
				continue
			}
			want := renderName(t, archive.NameTemplate, cfg.ProjectName, ver, goos, goarch) + "." + format(goos)
			published[want] = true
			if got, err := archiveName(ver, goos, goarch); err != nil || got != want {
				t.Errorf("archiveName(%s/%s) = %q, %v; goreleaser publishes %q", goos, goarch, got, err, want)
			}
		}
	}
	for _, goos := range build.Goos {
		for _, goarch := range build.Goarch {
			if !ignored(goos, goarch) {
				continue
			}
			if got, err := archiveName(ver, goos, goarch); err == nil && !published[got] {
				t.Errorf("archiveName(%s/%s) = %q, which the release does not publish", goos, goarch, got)
			}
		}
	}
}

func TestImageDeclaresTheInstallMethodZensuReads(t *testing.T) {
	lines := map[string]bool{}
	for _, line := range strings.Split(string(readRepoFile(t, "Dockerfile")), "\n") {
		lines[strings.TrimSpace(line)] = true
	}
	for _, want := range []string{
		"COPY zensu " + imageExecutable,
		"ENV " + installMethodEnv + "=" + string(MethodDocker),
		`ENTRYPOINT ["` + imageExecutable + `"]`,
	} {
		if !lines[want] {
			t.Errorf("Dockerfile lacks %q, which zensu update needs to recognise the official image", want)
		}
	}
}
