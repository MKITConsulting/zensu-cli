package update

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type InstallMethod string

const (
	MethodBinary          InstallMethod = "binary"
	MethodHomebrew        InstallMethod = "homebrew"
	MethodHomebrewFormula InstallMethod = "homebrew-formula"
	MethodDocker          InstallMethod = "docker"
	MethodPackageManager  InstallMethod = "package-manager"
	MethodDevelopment     InstallMethod = "development"
)

const (
	brewTap          = "mkitconsulting/tap"
	brewCask         = brewTap + "/zensu"
	caskroomDir      = "/Caskroom/zensu/"
	cellarDir        = "/Cellar/zensu/"
	formulaToCask    = "brew uninstall --formula zensu && brew install --cask " + brewCask
	dockerImage      = "ghcr.io/mkitconsulting/zensu-cli:latest"
	goInstallTarget  = "github.com/MKITConsulting/zensu-cli/cmd/zensu@latest"
	installMethodEnv = "ZENSU_INSTALL_METHOD"
)

type Install struct {
	Method     InstallMethod
	Executable string
}

var executablePath = os.Executable

var imageExecutable = "/usr/local/bin/zensu"

var packageManagerDirs = []string{"/usr/bin/", "/bin/", "/usr/sbin/", "/sbin/", "/nix/store/", "/snap/"}

func DetectInstall(current string) (Install, error) {
	exe, err := executablePath()
	if err != nil {
		return Install{}, fmt.Errorf("locating the zensu executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	image := os.Getenv(installMethodEnv) == string(MethodDocker)
	return Install{Method: classify(current, exe, image), Executable: exe}, nil
}

func classify(current, exe string, image bool) InstallMethod {
	path := filepath.ToSlash(exe)
	switch {
	case current == "" || current == "dev":
		return MethodDevelopment
	case strings.Contains(path, caskroomDir):
		return MethodHomebrew
	case strings.Contains(path, cellarDir):
		return MethodHomebrewFormula
	case image && path == imageExecutable:
		return MethodDocker
	case hasDirPrefix(path, packageManagerDirs):
		return MethodPackageManager
	default:
		return MethodBinary
	}
}

func hasDirPrefix(path string, dirs []string) bool {
	for _, d := range dirs {
		if strings.HasPrefix(path, d) {
			return true
		}
	}
	return false
}

func (i Install) HomebrewBin() string {
	prefix, _, ok := strings.Cut(filepath.ToSlash(i.Executable), caskroomDir)
	if !ok {
		return ""
	}
	return filepath.FromSlash(prefix + "/bin")
}

func BrewCommands() [][]string {
	return [][]string{
		{"brew", "update", "--quiet"},
		{"brew", "upgrade", "--cask", brewCask},
	}
}

func BrewCommandLine() string {
	var steps []string
	for _, c := range BrewCommands() {
		steps = append(steps, strings.Join(c, " "))
	}
	return strings.Join(steps, " && ")
}

func (i Install) UpdateCommand() string {
	switch i.Method {
	case MethodBinary, MethodHomebrew:
		return "zensu update"
	case MethodHomebrewFormula:
		return formulaToCask
	case MethodDocker:
		return "docker pull " + dockerImage
	case MethodDevelopment:
		return "go install " + goInstallTarget
	default:
		return ""
	}
}

func (i Install) Refusal() error {
	switch i.Method {
	case MethodBinary, MethodHomebrew:
		return nil
	case MethodHomebrewFormula:
		return fmt.Errorf("zensu was installed with the Homebrew formula, which %s no longer ships; switch to the cask: %s", brewTap, formulaToCask)
	case MethodDocker:
		return fmt.Errorf("zensu runs in a container; pull the new image instead: docker pull %s", dockerImage)
	case MethodPackageManager:
		return fmt.Errorf("%s is managed by a system package manager; update zensu with that package manager", i.Executable)
	case MethodDevelopment:
		return fmt.Errorf("this is a development build of zensu; install a release (https://github.com/MKITConsulting/zensu-cli#install) or rebuild it: go install %s", goInstallTarget)
	default:
		return fmt.Errorf("zensu cannot tell how %s was installed; update it the way you installed it", i.Executable)
	}
}
