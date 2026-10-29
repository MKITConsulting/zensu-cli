package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/MKITConsulting/zensu-cli/internal/update"
	"github.com/MKITConsulting/zensu-cli/internal/version"
)

const skipUpdateNoticeAnnotation = "zensu/skip-update-notice"

var (
	detectInstall   = update.DetectInstall
	latestRelease   = update.LatestRelease
	selfUpdate      = update.SelfUpdate
	verifyInstalled = update.VerifyInstalled
	notifyContext   = signal.NotifyContext
	lookPath        = exec.LookPath
	runExternal     = func(ctx context.Context, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
		c := exec.CommandContext(ctx, name, args...)
		c.Stdin, c.Stdout, c.Stderr = stdin, stdout, stderr
		return c.Run()
	}
)

type updateReport struct {
	CurrentVersion  string `json:"currentVersion"`
	LatestVersion   string `json:"latestVersion"`
	UpdateAvailable bool   `json:"updateAvailable"`
	InstallMethod   string `json:"installMethod"`
	UpdateCommand   string `json:"updateCommand"`
	Updated         bool   `json:"updated"`
}

func SkipsUpdateNotice(ran *cobra.Command) bool {
	return ran != nil && ran.Annotations[skipUpdateNoticeAnnotation] == "true"
}

func NewUpdateCmd(f *Factory) *cobra.Command {
	var check, asJSON bool
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update zensu to the latest release",
		Long: "Update zensu to the latest release, the way it was installed.\n\n" +
			"Homebrew cask installs run `" + update.BrewCommandLine() + "` with the brew of the Homebrew " +
			"installation that owns zensu, then check that the new version is installed. Install-script and " +
			"release-archive installs download the latest release, verify the Ed25519 signature of its checksums " +
			"and the archive's SHA-256, check that the new binary starts, and replace the binary in place. The " +
			"Docker image, the retired Homebrew formula, system packages and development builds are never " +
			"changed; zensu update explains how to update them instead.\n\n" +
			"--check only reports the current and latest version, the install method and the update " +
			"command, and changes nothing.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Annotations:  map[string]string{skipUpdateNoticeAnnotation: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUpdate(cmd, f, check, asJSON)
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "only report whether an update is available; change nothing")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output the result as JSON")
	return cmd
}

func runUpdate(cmd *cobra.Command, f *Factory, check, asJSON bool) error {
	ctx, stop := notifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	current := version.Version
	inst, err := detectInstall(current)
	if err != nil {
		return err
	}
	latest, err := latestRelease(ctx, current)
	if err != nil {
		return fmt.Errorf("checking for the latest zensu release: %w", err)
	}
	rep := updateReport{
		CurrentVersion:  strings.TrimPrefix(current, "v"),
		LatestVersion:   strings.TrimPrefix(latest, "v"),
		UpdateAvailable: inst.Method != update.MethodDevelopment && update.IsNewer(latest, current),
		InstallMethod:   string(inst.Method),
		UpdateCommand:   inst.UpdateCommand(),
	}
	if check {
		return writeUpdateReport(f.Out, rep, asJSON)
	}
	if inst.Method == update.MethodDevelopment {
		return inst.Refusal()
	}
	if !rep.UpdateAvailable {
		return writeUpdateReport(f.Out, rep, asJSON)
	}
	switch inst.Method {
	case update.MethodHomebrew:
		err = runBrew(ctx, cmd, f, inst, rep.LatestVersion, asJSON)
	case update.MethodBinary:
		fmt.Fprintf(cmd.ErrOrStderr(), "Updating zensu %s to %s (%s) ...\n", rep.CurrentVersion, rep.LatestVersion, inst.Executable)
		_, err = selfUpdate(ctx, current, latest, inst.Executable)
	default:
		err = inst.Refusal()
	}
	if err != nil {
		return err
	}
	rep.Updated = true
	return writeUpdateReport(f.Out, rep, asJSON)
}

func runBrew(ctx context.Context, cmd *cobra.Command, f *Factory, inst update.Install, latest string, asJSON bool) error {
	bin := inst.HomebrewBin()
	brew, err := findBrew(bin)
	if err != nil {
		return err
	}
	stdout := f.Out
	if asJSON {
		stdout = cmd.ErrOrStderr()
	}
	for _, step := range update.BrewCommands() {
		line := strings.Join(append([]string{brew}, step[1:]...), " ")
		fmt.Fprintf(cmd.ErrOrStderr(), "Running: %s\n", line)
		if err := runExternal(ctx, brew, step[1:], cmd.InOrStdin(), stdout, cmd.ErrOrStderr()); err != nil {
			return fmt.Errorf("%s failed: %w", line, err)
		}
	}
	if err := verifyInstalled(ctx, filepath.Join(bin, "zensu"), latest); err != nil {
		return fmt.Errorf("brew finished, but zensu %s is not installed: %w; run: %s", latest, err, update.BrewCommandLine())
	}
	return nil
}

func findBrew(bin string) (string, error) {
	want := filepath.Join(bin, "brew")
	if bin != "" {
		if brew, err := lookPath(want); err == nil {
			return brew, nil
		}
	}
	return "", fmt.Errorf("zensu was installed with Homebrew, but the brew of that installation (%s) was not found; run: %s", want, update.BrewCommandLine())
}

func writeUpdateReport(w io.Writer, rep updateReport, asJSON bool) error {
	if asJSON {
		raw, err := json.Marshal(rep)
		if err != nil {
			return err
		}
		return printJSON(w, raw)
	}
	var err error
	switch {
	case rep.Updated && rep.InstallMethod == string(update.MethodHomebrew):
		_, err = fmt.Fprintf(w, "Updated zensu %s to %s with Homebrew\n", rep.CurrentVersion, rep.LatestVersion)
	case rep.Updated:
		_, err = fmt.Fprintf(w, "Updated zensu %s to %s\n", rep.CurrentVersion, rep.LatestVersion)
	case rep.InstallMethod == string(update.MethodDevelopment):
		_, err = fmt.Fprintf(w, "zensu %s is a development build; the latest release is %s (install it with: %s)\n", rep.CurrentVersion, rep.LatestVersion, rep.UpdateCommand)
	case rep.UpdateAvailable && rep.UpdateCommand == "":
		_, err = fmt.Fprintf(w, "zensu %s is available (you have %s); update it with the package manager that installed it\n", rep.LatestVersion, rep.CurrentVersion)
	case rep.UpdateAvailable:
		_, err = fmt.Fprintf(w, "zensu %s is available (you have %s, installed via %s); run: %s\n", rep.LatestVersion, rep.CurrentVersion, rep.InstallMethod, rep.UpdateCommand)
	default:
		_, err = fmt.Fprintf(w, "zensu %s is up to date\n", rep.CurrentVersion)
	}
	return err
}
