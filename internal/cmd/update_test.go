package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MKITConsulting/zensu-cli/internal/update"
	"github.com/MKITConsulting/zensu-cli/internal/version"
)

type updateStub struct {
	install       update.Install
	latest        string
	latestErr     error
	selfUpdateErr error
	onSelfUpdate  func(context.Context) error
	externalErr   error
	verifyErr     error
	noBrew        bool
	selfUpdates   [][]string
	external      [][]string
	lookedUp      []string
	verified      [][]string
}

func stubUpdate(t *testing.T, current string, s *updateStub) {
	t.Helper()
	oldDetect, oldLatest, oldSelf, oldVerify, oldLook, oldRun, oldVersion := detectInstall, latestRelease, selfUpdate, verifyInstalled, lookPath, runExternal, version.Version
	t.Cleanup(func() {
		detectInstall, latestRelease, selfUpdate, verifyInstalled, lookPath, runExternal, version.Version = oldDetect, oldLatest, oldSelf, oldVerify, oldLook, oldRun, oldVersion
	})
	version.Version = current
	detectInstall = func(string) (update.Install, error) { return s.install, nil }
	latestRelease = func(context.Context, string) (string, error) { return s.latest, s.latestErr }
	selfUpdate = func(ctx context.Context, current, tag, exe string) (update.Result, error) {
		s.selfUpdates = append(s.selfUpdates, []string{current, tag, exe})
		if s.onSelfUpdate != nil {
			return update.Result{}, s.onSelfUpdate(ctx)
		}
		return update.Result{Previous: current, Version: strings.TrimPrefix(tag, "v"), Executable: exe}, s.selfUpdateErr
	}
	verifyInstalled = func(_ context.Context, path, ver string) error {
		s.verified = append(s.verified, []string{path, ver})
		return s.verifyErr
	}
	lookPath = func(name string) (string, error) {
		s.lookedUp = append(s.lookedUp, name)
		if s.noBrew {
			return "", exec.ErrNotFound
		}
		return name, nil
	}
	runExternal = func(_ context.Context, name string, args []string, _ io.Reader, _, _ io.Writer) error {
		s.external = append(s.external, append([]string{name}, args...))
		return s.externalErr
	}
}

var (
	caskExe  = "/opt/homebrew/Caskroom/zensu/1.2.3/zensu"
	caskBrew = filepath.Join(filepath.FromSlash("/opt/homebrew/bin"), "brew")
	caskLink = filepath.Join(filepath.FromSlash("/opt/homebrew/bin"), "zensu")
)

func runUpdateCmd(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	c := NewUpdateCmd(&Factory{Out: out})
	c.SetErr(errOut)
	c.SetArgs(args)
	err := c.Execute()
	return out.String(), errOut.String(), err
}

func decodeReport(t *testing.T, out string) updateReport {
	t.Helper()
	var rep updateReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("output is not one JSON object: %v\n%s", err, out)
	}
	return rep
}

func TestUpdateCheckJSON(t *testing.T) {
	s := &updateStub{install: update.Install{Method: update.MethodHomebrew, Executable: "/opt/homebrew/Caskroom/zensu/1.2.3/zensu"}, latest: "v1.3.0"}
	stubUpdate(t, "1.2.3", s)

	out, _, err := runUpdateCmd(t, "--check", "--json")
	if err != nil {
		t.Fatal(err)
	}
	want := updateReport{CurrentVersion: "1.2.3", LatestVersion: "1.3.0", UpdateAvailable: true, InstallMethod: "homebrew", UpdateCommand: "zensu update"}
	if got := decodeReport(t, out); got != want {
		t.Errorf("report = %+v, want %+v", got, want)
	}
	for _, key := range []string{"currentVersion", "latestVersion", "updateAvailable", "installMethod", "updateCommand", "updated"} {
		if !strings.Contains(out, `"`+key+`"`) {
			t.Errorf("JSON output lacks %q:\n%s", key, out)
		}
	}
	if len(s.selfUpdates) != 0 || len(s.external) != 0 {
		t.Errorf("--check must change nothing, got self updates %v and commands %v", s.selfUpdates, s.external)
	}
}

func TestUpdateCheckHuman(t *testing.T) {
	stubUpdate(t, "1.2.3", &updateStub{install: update.Install{Method: update.MethodBinary, Executable: "/usr/local/bin/zensu"}, latest: "v1.3.0"})
	out, _, err := runUpdateCmd(t, "--check")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "zensu 1.3.0 is available (you have 1.2.3, installed via binary); run: zensu update") {
		t.Errorf("output = %q", out)
	}
}

func TestUpdateCheckPackageManagerHasNoCommand(t *testing.T) {
	stubUpdate(t, "1.2.3", &updateStub{install: update.Install{Method: update.MethodPackageManager, Executable: "/usr/bin/zensu"}, latest: "v1.3.0"})
	out, _, err := runUpdateCmd(t, "--check")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "update it with the package manager that installed it") {
		t.Errorf("output = %q", out)
	}
}

func TestUpdateUpToDate(t *testing.T) {
	for _, method := range []update.InstallMethod{update.MethodBinary, update.MethodHomebrew, update.MethodDocker} {
		t.Run(string(method), func(t *testing.T) {
			s := &updateStub{install: update.Install{Method: method, Executable: "/usr/local/bin/zensu"}, latest: "v1.2.3"}
			stubUpdate(t, "1.2.3", s)
			out, _, err := runUpdateCmd(t)
			if err != nil {
				t.Fatal(err)
			}
			if out != "zensu 1.2.3 is up to date\n" {
				t.Errorf("output = %q", out)
			}
			if len(s.selfUpdates) != 0 || len(s.external) != 0 {
				t.Errorf("an up-to-date install must not be touched: %v %v", s.selfUpdates, s.external)
			}
		})
	}
}

func TestUpdateBinarySelfUpdates(t *testing.T) {
	s := &updateStub{install: update.Install{Method: update.MethodBinary, Executable: "/usr/local/bin/zensu"}, latest: "v1.3.0"}
	stubUpdate(t, "1.2.3", s)

	out, errOut, err := runUpdateCmd(t)
	if err != nil {
		t.Fatal(err)
	}
	if want := [][]string{{"1.2.3", "v1.3.0", "/usr/local/bin/zensu"}}; !reflect.DeepEqual(s.selfUpdates, want) {
		t.Errorf("self updates = %v, want %v", s.selfUpdates, want)
	}
	if out != "Updated zensu 1.2.3 to 1.3.0\n" {
		t.Errorf("output = %q", out)
	}
	if !strings.Contains(errOut, "Updating zensu 1.2.3 to 1.3.0 (/usr/local/bin/zensu)") {
		t.Errorf("progress = %q", errOut)
	}
}

func TestUpdateBinaryJSON(t *testing.T) {
	stubUpdate(t, "1.2.3", &updateStub{install: update.Install{Method: update.MethodBinary, Executable: "/usr/local/bin/zensu"}, latest: "v1.3.0"})
	out, _, err := runUpdateCmd(t, "--json")
	if err != nil {
		t.Fatal(err)
	}
	if rep := decodeReport(t, out); !rep.Updated || rep.LatestVersion != "1.3.0" {
		t.Errorf("report = %+v, want updated to 1.3.0", rep)
	}
}

func TestUpdateBinaryFailure(t *testing.T) {
	stubUpdate(t, "1.2.3", &updateStub{install: update.Install{Method: update.MethodBinary, Executable: "/usr/local/bin/zensu"}, latest: "v1.3.0", selfUpdateErr: errors.New("release v1.3.0 is not signed")})
	out, _, err := runUpdateCmd(t)
	if err == nil || !strings.Contains(err.Error(), "is not signed") {
		t.Fatalf("error = %v, want the self-update error", err)
	}
	if out != "" {
		t.Errorf("a failed update must not report success: %q", out)
	}
}

func TestUpdateHomebrewRunsBrew(t *testing.T) {
	s := &updateStub{install: update.Install{Method: update.MethodHomebrew, Executable: caskExe}, latest: "v1.3.0"}
	stubUpdate(t, "1.2.3", s)

	out, errOut, err := runUpdateCmd(t, "--json")
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{caskBrew, "update", "--quiet"},
		{caskBrew, "upgrade", "--cask", "mkitconsulting/tap/zensu"},
	}
	if !reflect.DeepEqual(s.external, want) {
		t.Errorf("brew commands = %v, want %v", s.external, want)
	}
	if wantVerify := [][]string{{caskLink, "1.3.0"}}; !reflect.DeepEqual(s.verified, wantVerify) {
		t.Errorf("verified = %v, want %v", s.verified, wantVerify)
	}
	if len(s.selfUpdates) != 0 {
		t.Errorf("a Homebrew install must never be replaced by zensu itself: %v", s.selfUpdates)
	}
	if rep := decodeReport(t, out); !rep.Updated || rep.InstallMethod != "homebrew" {
		t.Errorf("report = %+v", rep)
	}
	if !strings.Contains(errOut, "Running: "+caskBrew+" upgrade --cask mkitconsulting/tap/zensu") {
		t.Errorf("progress = %q", errOut)
	}
}

func TestUpdateHomebrewUsesTheOwningPrefix(t *testing.T) {
	s := &updateStub{install: update.Install{Method: update.MethodHomebrew, Executable: "/usr/local/Caskroom/zensu/1.2.3/zensu"}, latest: "v1.3.0"}
	stubUpdate(t, "1.2.3", s)
	if _, _, err := runUpdateCmd(t); err != nil {
		t.Fatal(err)
	}
	bin := filepath.FromSlash("/usr/local/bin")
	if want := []string{filepath.Join(bin, "brew")}; !reflect.DeepEqual(s.lookedUp, want) {
		t.Errorf("looked up %v, want only the brew of the owning prefix %v", s.lookedUp, want)
	}
	if len(s.external) != 2 || s.external[0][0] != filepath.Join(bin, "brew") {
		t.Errorf("brew commands = %v, want them run with %s", s.external, filepath.Join(bin, "brew"))
	}
	if len(s.verified) != 1 || s.verified[0][0] != filepath.Join(bin, "zensu") {
		t.Errorf("verified = %v, want %s", s.verified, filepath.Join(bin, "zensu"))
	}
}

func TestUpdateHomebrewHuman(t *testing.T) {
	stubUpdate(t, "1.2.3", &updateStub{install: update.Install{Method: update.MethodHomebrew, Executable: caskExe}, latest: "v1.3.0"})
	out, _, err := runUpdateCmd(t)
	if err != nil {
		t.Fatal(err)
	}
	if out != "Updated zensu 1.2.3 to 1.3.0 with Homebrew\n" {
		t.Errorf("output = %q", out)
	}
}

func TestUpdateHomebrewReportsAnUpgradeThatDidNotLand(t *testing.T) {
	s := &updateStub{install: update.Install{Method: update.MethodHomebrew, Executable: caskExe}, latest: "v1.3.0", verifyErr: errors.New(caskLink + ` reports "zensu 1.2.3" instead of zensu 1.3.0`)}
	stubUpdate(t, "1.2.3", s)
	out, _, err := runUpdateCmd(t, "--json")
	if err == nil || !strings.Contains(err.Error(), "brew finished, but zensu 1.3.0 is not installed: "+caskLink+` reports "zensu 1.2.3" instead of zensu 1.3.0; run: brew update --quiet && brew upgrade --cask mkitconsulting/tap/zensu`) {
		t.Fatalf("error = %v", err)
	}
	if out != "" {
		t.Errorf("an unconfirmed upgrade must not report success: %q", out)
	}
}

func TestUpdateHomebrewWithoutBrew(t *testing.T) {
	s := &updateStub{install: update.Install{Method: update.MethodHomebrew, Executable: caskExe}, latest: "v1.3.0", noBrew: true}
	stubUpdate(t, "1.2.3", s)
	_, _, err := runUpdateCmd(t)
	if err == nil || !strings.Contains(err.Error(), "the brew of that installation ("+caskBrew+") was not found; run: brew update --quiet && brew upgrade --cask mkitconsulting/tap/zensu") {
		t.Fatalf("error = %v", err)
	}
	if want := []string{caskBrew}; !reflect.DeepEqual(s.lookedUp, want) {
		t.Errorf("looked up %v, want only %v and never brew on PATH", s.lookedUp, want)
	}
	if len(s.external) != 0 || len(s.selfUpdates) != 0 || len(s.verified) != 0 {
		t.Errorf("nothing may run without brew: %v %v %v", s.external, s.selfUpdates, s.verified)
	}
}

func TestUpdateHomebrewWithoutKnownPrefix(t *testing.T) {
	s := &updateStub{install: update.Install{Method: update.MethodHomebrew}, latest: "v1.3.0"}
	stubUpdate(t, "1.2.3", s)
	_, _, err := runUpdateCmd(t)
	if err == nil || !strings.Contains(err.Error(), "was not found; run: brew update --quiet") {
		t.Fatalf("error = %v", err)
	}
	if len(s.lookedUp) != 0 || len(s.external) != 0 {
		t.Errorf("without the owning prefix nothing may be looked up or run: %v %v", s.lookedUp, s.external)
	}
}

func TestUpdateHomebrewStepFails(t *testing.T) {
	s := &updateStub{install: update.Install{Method: update.MethodHomebrew, Executable: caskExe}, latest: "v1.3.0", externalErr: errors.New("exit status 1")}
	stubUpdate(t, "1.2.3", s)
	out, _, err := runUpdateCmd(t)
	if err == nil || !strings.Contains(err.Error(), caskBrew+" update --quiet failed: exit status 1") {
		t.Fatalf("error = %v", err)
	}
	if len(s.external) != 1 || len(s.verified) != 0 {
		t.Errorf("nothing may run after brew update failed: %v %v", s.external, s.verified)
	}
	if out != "" {
		t.Errorf("a failed update must not report success: %q", out)
	}
}

func TestUpdateInterruptCancelsTheUpdate(t *testing.T) {
	var watched []os.Signal
	var interrupt context.CancelFunc
	old := notifyContext
	notifyContext = func(parent context.Context, sigs ...os.Signal) (context.Context, context.CancelFunc) {
		watched = sigs
		ctx, cancel := context.WithCancel(parent)
		interrupt = cancel
		return ctx, cancel
	}
	t.Cleanup(func() { notifyContext = old })
	s := &updateStub{install: update.Install{Method: update.MethodBinary, Executable: "/usr/local/bin/zensu"}, latest: "v1.3.0"}
	s.onSelfUpdate = func(ctx context.Context) error {
		interrupt()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
			return errors.New("the interrupt did not reach the update")
		}
	}
	stubUpdate(t, "1.2.3", s)
	out, _, err := runUpdateCmd(t)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want the update canceled by the interrupt", err)
	}
	if out != "" {
		t.Errorf("an interrupted update must not report success: %q", out)
	}
	if want := []os.Signal{os.Interrupt, syscall.SIGTERM}; !reflect.DeepEqual(watched, want) {
		t.Errorf("watched signals = %v, want %v", watched, want)
	}
}

func TestUpdateRefusesUnmanagedInstalls(t *testing.T) {
	tests := []struct {
		name    string
		current string
		install update.Install
		wantErr string
	}{
		{"docker", "1.2.3", update.Install{Method: update.MethodDocker, Executable: "/usr/local/bin/zensu"}, "docker pull ghcr.io/mkitconsulting/zensu-cli:latest"},
		{"homebrew formula", "1.2.3", update.Install{Method: update.MethodHomebrewFormula, Executable: "/usr/local/Cellar/zensu/1.2.3/bin/zensu"}, "switch to the cask: brew uninstall --formula zensu && brew install --cask mkitconsulting/tap/zensu"},
		{"package manager", "1.2.3", update.Install{Method: update.MethodPackageManager, Executable: "/usr/bin/zensu"}, "/usr/bin/zensu is managed by a system package manager"},
		{"development build", "dev", update.Install{Method: update.MethodDevelopment, Executable: "/tmp/zensu"}, "development build"},
		{"unknown install method", "1.2.3", update.Install{Method: update.InstallMethod("unknown"), Executable: "/opt/zensu"}, "zensu cannot tell how /opt/zensu was installed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &updateStub{install: tt.install, latest: "v1.3.0"}
			stubUpdate(t, tt.current, s)
			out, _, err := runUpdateCmd(t)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want %q", err, tt.wantErr)
			}
			if out != "" || len(s.selfUpdates) != 0 || len(s.external) != 0 {
				t.Errorf("a refused update must change nothing: out %q, %v, %v", out, s.selfUpdates, s.external)
			}
		})
	}
}

func TestUpdateCheckDevelopmentBuild(t *testing.T) {
	stubUpdate(t, "dev", &updateStub{install: update.Install{Method: update.MethodDevelopment}, latest: "v1.3.0"})
	out, _, err := runUpdateCmd(t, "--check", "--json")
	if err != nil {
		t.Fatal(err)
	}
	want := updateReport{CurrentVersion: "dev", LatestVersion: "1.3.0", InstallMethod: "development", UpdateCommand: "go install github.com/MKITConsulting/zensu-cli/cmd/zensu@latest"}
	if got := decodeReport(t, out); got != want {
		t.Errorf("report = %+v, want %+v", got, want)
	}
	out, _, err = runUpdateCmd(t, "--check")
	if err != nil || !strings.Contains(out, "development build; the latest release is 1.3.0") {
		t.Errorf("human output = %q, %v", out, err)
	}
}

func TestUpdateLatestReleaseError(t *testing.T) {
	s := &updateStub{install: update.Install{Method: update.MethodBinary}, latestErr: errors.New("github releases API: status 500")}
	stubUpdate(t, "1.2.3", s)
	_, _, err := runUpdateCmd(t, "--check")
	if err == nil || !strings.Contains(err.Error(), "checking for the latest zensu release: github releases API: status 500") {
		t.Fatalf("error = %v", err)
	}
}

func TestUpdateRejectsArguments(t *testing.T) {
	s := &updateStub{install: update.Install{Method: update.MethodBinary}, latest: "v1.3.0"}
	stubUpdate(t, "1.2.3", s)
	if _, _, err := runUpdateCmd(t, "v1.3.0"); err == nil {
		t.Fatal("zensu update must reject positional arguments")
	}
	if len(s.selfUpdates) != 0 {
		t.Errorf("a rejected invocation must not update: %v", s.selfUpdates)
	}
}

func TestSkipsUpdateNotice(t *testing.T) {
	root := NewRootCmd()
	upd, _, err := root.Find([]string{"update"})
	if err != nil {
		t.Fatal(err)
	}
	if !SkipsUpdateNotice(upd) {
		t.Error("zensu update must suppress the passive update notice")
	}
	for _, path := range [][]string{{"features", "update"}, {"wiki", "update"}, {"auth", "status"}} {
		c, _, err := root.Find(path)
		if err != nil {
			t.Fatal(err)
		}
		if SkipsUpdateNotice(c) {
			t.Errorf("%v must keep the update notice", path)
		}
	}
	if SkipsUpdateNotice(nil) {
		t.Error("a nil command must keep the update notice")
	}
}
