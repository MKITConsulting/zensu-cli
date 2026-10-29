package update

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name    string
		current string
		exe     string
		image   bool
		want    InstallMethod
	}{
		{"dev build", "dev", "/usr/local/bin/zensu", false, MethodDevelopment},
		{"empty version", "", "/usr/local/bin/zensu", false, MethodDevelopment},
		{"dev build wins over homebrew", "dev", "/opt/homebrew/Caskroom/zensu/0.15.0/zensu", false, MethodDevelopment},
		{"homebrew cask", "1.2.3", "/opt/homebrew/Caskroom/zensu/0.15.0/zensu", false, MethodHomebrew},
		{"linuxbrew cask", "1.2.3", "/home/linuxbrew/.linuxbrew/Caskroom/zensu/0.15.0/zensu", false, MethodHomebrew},
		{"homebrew formula", "1.2.3", "/usr/local/Cellar/zensu/1.0.0/bin/zensu", false, MethodHomebrewFormula},
		{"linuxbrew formula", "1.2.3", "/home/linuxbrew/.linuxbrew/Cellar/zensu/1.0.0/bin/zensu", false, MethodHomebrewFormula},
		{"other cask", "1.2.3", "/opt/homebrew/Caskroom/zensu-other/1.0/zensu", false, MethodBinary},
		{"official image", "1.2.3", "/usr/local/bin/zensu", true, MethodDocker},
		{"dev build in the image", "dev", "/usr/local/bin/zensu", true, MethodDevelopment},
		{"script install in a derived image", "1.2.3", "/home/dev/.local/bin/zensu", true, MethodBinary},
		{"system package in a derived image", "1.2.3", "/usr/bin/zensu", true, MethodPackageManager},
		{"usr bin", "1.2.3", "/usr/bin/zensu", false, MethodPackageManager},
		{"bin", "1.2.3", "/bin/zensu", false, MethodPackageManager},
		{"nix store", "1.2.3", "/nix/store/abc-zensu-1.2.3/bin/zensu", false, MethodPackageManager},
		{"snap", "1.2.3", "/snap/zensu/12/bin/zensu", false, MethodPackageManager},
		{"install script default", "1.2.3", "/usr/local/bin/zensu", false, MethodBinary},
		{"install script fallback", "1.2.3", "/home/dev/.local/bin/zensu", false, MethodBinary},
		{"lookalike system dir", "1.2.3", "/usr/binx/zensu", false, MethodBinary},
		{"windows install dir", "1.2.3", `C:\Users\dev\AppData\Local\Programs\zensu\zensu.exe`, false, MethodBinary},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classify(tt.current, tt.exe, tt.image); got != tt.want {
				t.Errorf("classify(%q, %q, %v) = %q, want %q", tt.current, tt.exe, tt.image, got, tt.want)
			}
		})
	}
}

func stubInstallEnv(t *testing.T, exe, method string) {
	t.Helper()
	old := executablePath
	executablePath = func() (string, error) { return exe, nil }
	t.Cleanup(func() { executablePath = old })
	t.Setenv(installMethodEnv, method)
}

func TestDetectInstallResolvesHomebrewSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "Caskroom", "zensu", "0.15.0", "zensu")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "bin", "zensu")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	stubInstallEnv(t, link, "")

	got, err := DetectInstall("1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if got.Method != MethodHomebrew {
		t.Errorf("method = %q, want %q for a symlink into the Caskroom", got.Method, MethodHomebrew)
	}
	wantExe, _ := filepath.EvalSymlinks(target)
	if got.Executable != wantExe {
		t.Errorf("executable = %q, want the resolved target %q", got.Executable, wantExe)
	}
}

func stubImageExecutable(t *testing.T, path string) {
	t.Helper()
	old := imageExecutable
	imageExecutable = filepath.ToSlash(path)
	t.Cleanup(func() { imageExecutable = old })
}

func TestDetectInstallImageMarker(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "zensu")
	stubImageExecutable(t, exe)
	stubInstallEnv(t, exe, "docker")

	got, err := DetectInstall("1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if got.Method != MethodDocker {
		t.Errorf("method = %q, want %q when the image declares %s=docker", got.Method, MethodDocker, installMethodEnv)
	}
}

func TestDetectInstallMarkerNeedsTheImageExecutable(t *testing.T) {
	stubImageExecutable(t, filepath.Join(t.TempDir(), "image", "zensu"))
	stubInstallEnv(t, filepath.Join(t.TempDir(), "zensu"), "docker")

	got, err := DetectInstall("1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if got.Method != MethodBinary {
		t.Errorf("method = %q, want %q for a zensu the image did not ship, even with the inherited marker", got.Method, MethodBinary)
	}
}

func TestHomebrewBin(t *testing.T) {
	tests := []struct {
		exe  string
		want string
	}{
		{"/opt/homebrew/Caskroom/zensu/0.15.0/zensu", filepath.FromSlash("/opt/homebrew/bin")},
		{"/usr/local/Caskroom/zensu/0.15.0/zensu", filepath.FromSlash("/usr/local/bin")},
		{"/home/linuxbrew/.linuxbrew/Caskroom/zensu/0.15.0/zensu", filepath.FromSlash("/home/linuxbrew/.linuxbrew/bin")},
		{"/usr/local/Cellar/zensu/1.0.0/bin/zensu", ""},
		{"/usr/local/bin/zensu", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := (Install{Method: MethodHomebrew, Executable: tt.exe}).HomebrewBin(); got != tt.want {
			t.Errorf("HomebrewBin(%q) = %q, want %q", tt.exe, got, tt.want)
		}
	}
}

func TestDetectInstallClassifiesOtherMarkerValuesByPath(t *testing.T) {
	for _, value := range []string{"", "binary", "homebrew", "Docker", " docker"} {
		t.Run(value, func(t *testing.T) {
			stubInstallEnv(t, filepath.Join(t.TempDir(), "zensu"), value)

			got, err := DetectInstall("1.2.3")
			if err != nil {
				t.Fatal(err)
			}
			if got.Method != MethodBinary {
				t.Errorf("method = %q with %s=%q, want %q from the executable path", got.Method, installMethodEnv, value, MethodBinary)
			}
		})
	}
}

func TestDetectInstallPlainBinary(t *testing.T) {
	root := t.TempDir()
	stubInstallEnv(t, filepath.Join(root, "zensu"), "")

	got, err := DetectInstall("1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if got.Method != MethodBinary {
		t.Errorf("method = %q, want %q", got.Method, MethodBinary)
	}
	if got.Executable != filepath.Join(root, "zensu") {
		t.Errorf("executable = %q, want the unresolvable path unchanged", got.Executable)
	}
}

func TestDetectInstallExecutableError(t *testing.T) {
	old := executablePath
	executablePath = func() (string, error) { return "", errors.New("no executable") }
	t.Cleanup(func() { executablePath = old })
	if _, err := DetectInstall("1.2.3"); err == nil || !strings.Contains(err.Error(), "no executable") {
		t.Errorf("DetectInstall error = %v, want the executable lookup error", err)
	}
}

func TestUpdateCommandAndRefusal(t *testing.T) {
	tests := []struct {
		method      InstallMethod
		wantCommand string
		wantRefusal string
	}{
		{MethodBinary, "zensu update", ""},
		{MethodHomebrew, "zensu update", ""},
		{MethodHomebrewFormula, "brew uninstall --formula zensu && brew install --cask mkitconsulting/tap/zensu", "switch to the cask: brew uninstall --formula zensu && brew install --cask mkitconsulting/tap/zensu"},
		{MethodDocker, "docker pull ghcr.io/mkitconsulting/zensu-cli:latest", "docker pull ghcr.io/mkitconsulting/zensu-cli:latest"},
		{MethodPackageManager, "", "/usr/bin/zensu is managed by a system package manager"},
		{MethodDevelopment, "go install github.com/MKITConsulting/zensu-cli/cmd/zensu@latest", "go install github.com/MKITConsulting/zensu-cli/cmd/zensu@latest"},
		{InstallMethod("unknown"), "", "zensu cannot tell how /usr/bin/zensu was installed"},
	}
	for _, tt := range tests {
		t.Run(string(tt.method), func(t *testing.T) {
			inst := Install{Method: tt.method, Executable: "/usr/bin/zensu"}
			if got := inst.UpdateCommand(); got != tt.wantCommand {
				t.Errorf("UpdateCommand() = %q, want %q", got, tt.wantCommand)
			}
			err := inst.Refusal()
			if tt.wantRefusal == "" {
				if err != nil {
					t.Errorf("Refusal() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantRefusal) {
				t.Errorf("Refusal() = %v, want it to mention %q", err, tt.wantRefusal)
			}
		})
	}
}

func TestBrewCommandLine(t *testing.T) {
	want := "brew update --quiet && brew upgrade --cask mkitconsulting/tap/zensu"
	if got := BrewCommandLine(); got != want {
		t.Errorf("BrewCommandLine() = %q, want %q", got, want)
	}
	cmds := BrewCommands()
	cmds[0][0] = "mutated"
	if BrewCommands()[0][0] != "brew" {
		t.Error("BrewCommands must return a fresh slice on every call")
	}
}
