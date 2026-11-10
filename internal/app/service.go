package app

import (
	"flag"
	"fmt"
	"html"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ans-4175/mr-reviewbot/internal/config"
)

type service struct {
	Label    string
	Binary   string
	Config   string
	State    string
	Path     string // PATH at install time, so the scheduler finds the same glab/claude you tested with
	Interval time.Duration
}

func (s service) args() []string {
	return []string{s.Binary, "poll", "--config", s.Config, "--state", s.State}
}

// launchd counts StartInterval from the previous run's end, so a slow round never overlaps the next one.
func (s service) plist() string {
	var b strings.Builder
	x := html.EscapeString
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>` + x(s.Label) + `</string>
  <key>ProgramArguments</key>
  <array>
`)
	for _, a := range s.args() {
		b.WriteString("    <string>" + x(a) + "</string>\n")
	}
	fmt.Fprintf(&b, `  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key>
    <string>%s</string>
  </dict>
  <key>StartInterval</key>
  <integer>%d</integer>
  <key>RunAtLoad</key>
  <false/>
  <key>StandardOutPath</key>
  <string>%s</string>
  <key>StandardErrorPath</key>
  <string>%s</string>
</dict>
</plist>
`, x(s.Path), int(s.Interval.Seconds()), x(filepath.Join(s.State, "log", "poll.out.log")), x(filepath.Join(s.State, "log", "poll.err.log")))
	return b.String()
}

func systemdQuote(a string) string {
	if strings.ContainsAny(a, " \t\"\\") {
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(a) + `"`
	}
	return a
}

func (s service) systemdService() string {
	var quoted []string
	for _, a := range s.args() {
		quoted = append(quoted, systemdQuote(a))
	}
	return fmt.Sprintf(`[Unit]
Description=mr-reviewbot poll (review open merge requests)

[Service]
Type=oneshot
Environment=%s
ExecStart=%s
`, systemdQuote("PATH="+s.Path), strings.Join(quoted, " "))
}

// OnUnitInactiveSec counts from the previous run's end, like launchd's StartInterval.
func (s service) systemdTimer() string {
	return fmt.Sprintf(`[Unit]
Description=Run mr-reviewbot poll every %s

[Timer]
OnBootSec=5min
OnUnitInactiveSec=%ds
Unit=%s.service

[Install]
WantedBy=timers.target
`, s.Interval, int(s.Interval.Seconds()), s.Label)
}

type unitFile struct{ path, body string }

func (s service) files(home string) []unitFile {
	if runtime.GOOS == "darwin" {
		return []unitFile{{filepath.Join(home, "Library", "LaunchAgents", s.Label+".plist"), s.plist()}}
	}
	dir := filepath.Join(home, ".config", "systemd", "user")
	return []unitFile{
		{filepath.Join(dir, s.Label+".service"), s.systemdService()},
		{filepath.Join(dir, s.Label+".timer"), s.systemdTimer()},
	}
}

func runService(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: reviewbot service <install|uninstall|status|print> [--interval 30m] [--label mr-reviewbot] [--config f] [--state d]")
		return 2
	}
	action := args[0]
	fs := flag.NewFlagSet("service", flag.ContinueOnError)
	fs.SetOutput(stderr)
	s := service{}
	fs.StringVar(&s.Label, "label", "mr-reviewbot", "launchd label / systemd unit name")
	fs.StringVar(&s.Config, "config", config.DefaultPath(), "config file the scheduler uses")
	fs.StringVar(&s.State, "state", config.DefaultStateDir(), "state dir the scheduler uses")
	fs.DurationVar(&s.Interval, "interval", 30*time.Minute, "time between the end of one run and the start of the next")
	fs.StringVar(&s.Binary, "binary", "", "reviewbot binary to run (default: this one)")
	if _, err := parse(fs, args[1:]); err != nil {
		return 2
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		fmt.Fprintf(stderr, "error: no scheduler support for %s; run `reviewbot poll` from your own scheduler\n", runtime.GOOS)
		return 2
	}
	if s.Interval < time.Minute {
		fmt.Fprintln(stderr, "error: --interval must be at least 1m")
		return 2
	}
	if s.Binary == "" {
		exe, err := os.Executable()
		if err == nil {
			exe, err = filepath.EvalSymlinks(exe)
		}
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		s.Binary = exe
	}
	s.Path = os.Getenv("PATH")
	home, _ := os.UserHomeDir()
	files := s.files(home)

	switch action {
	case "print":
		for _, f := range files {
			fmt.Fprintf(stdout, "# %s\n%s\n", f.path, f.body)
		}
		return 0
	case "install":
		if _, err := config.Load(s.Config); err != nil {
			fmt.Fprintln(stderr, "error: the scheduler would fail:", err)
			return 2
		}
		if err := os.MkdirAll(filepath.Join(s.State, "log"), 0o755); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		for _, f := range files {
			if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
				fmt.Fprintln(stderr, "error:", err)
				return 1
			}
			if err := os.WriteFile(f.path, []byte(f.body), 0o644); err != nil {
				fmt.Fprintln(stderr, "error:", err)
				return 1
			}
			fmt.Fprintln(stdout, "wrote", f.path)
		}
		if runtime.GOOS == "darwin" {
			_ = sh(io.Discard, "launchctl", "bootout", launchdTarget(s.Label)) // reload if already loaded
			if err := sh(stderr, "launchctl", "bootstrap", fmt.Sprintf("gui/%d", os.Getuid()), files[0].path); err != nil {
				return 1
			}
			fmt.Fprintf(stdout, "loaded %s: first run in %s, then %s after each run ends.\nnote: LaunchAgents run only while this user is logged in to the GUI.\n", s.Label, s.Interval, s.Interval)
			return 0
		}
		if sh(stderr, "systemctl", "--user", "daemon-reload") != nil || sh(stderr, "systemctl", "--user", "enable", "--now", s.Label+".timer") != nil {
			return 1
		}
		fmt.Fprintf(stdout, "enabled %s.timer.\nnote: to keep it running after logout: sudo loginctl enable-linger %s\n", s.Label, os.Getenv("USER"))
		return 0
	case "uninstall":
		if runtime.GOOS == "darwin" {
			_ = sh(io.Discard, "launchctl", "bootout", launchdTarget(s.Label))
		} else {
			_ = sh(io.Discard, "systemctl", "--user", "disable", "--now", s.Label+".timer")
		}
		for _, f := range files {
			if err := os.Remove(f.path); err == nil {
				fmt.Fprintln(stdout, "removed", f.path)
			}
		}
		if runtime.GOOS == "linux" {
			_ = sh(io.Discard, "systemctl", "--user", "daemon-reload")
		}
		return 0
	case "status":
		if runtime.GOOS == "darwin" {
			out, err := exec.Command("launchctl", "print", launchdTarget(s.Label)).CombinedOutput()
			if err != nil {
				fmt.Fprintf(stdout, "%s is not loaded\n", s.Label)
				return 1
			}
			seen := map[string]bool{}
			for _, l := range strings.Split(string(out), "\n") {
				t := strings.TrimSpace(l)
				for _, k := range []string{"state =", "runs =", "last exit code =", "run interval =", "path ="} {
					if strings.HasPrefix(t, k) && !seen[k] { // nested sections repeat keys; the job's own come first
						seen[k] = true
						fmt.Fprintln(stdout, t)
					}
				}
			}
			return 0
		}
		if sh(stdout, "systemctl", "--user", "list-timers", s.Label+".timer", "--no-pager") != nil {
			return 1
		}
		return 0
	}
	fmt.Fprintf(stderr, "unknown service action %q\n", action)
	return 2
}

func launchdTarget(label string) string { return fmt.Sprintf("gui/%d/%s", os.Getuid(), label) }

func sh(w io.Writer, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = w, w
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(w, "%s %s: %v\n", name, strings.Join(args, " "), err)
		return err
	}
	return nil
}
