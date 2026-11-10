package app

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	reviewbot "mr-reviewbot"
)

// runSkill copies the embedded review-mr skill for installs that have only the binary.
// The command is rewritten to this binary's absolute path because agent runtimes often have a minimal PATH.
func runSkill(args []string, stdout, stderr io.Writer) int {
	if len(args) != 2 || args[0] != "install" {
		fmt.Fprintln(stderr, "usage: reviewbot skill install <skills-dir>   (writes <skills-dir>/review-mr/SKILL.md)\n"+
			"from a source checkout you can instead symlink skills/review-mr into the skills dir")
		return 2
	}
	text, err := fs.ReadFile(reviewbot.Assets, "skills/review-mr/SKILL.md")
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	body := strings.ReplaceAll(string(text), "\n   reviewbot ", "\n   "+exe+" ")
	dest := filepath.Join(args[1], "review-mr", "SKILL.md")
	if fi, err := os.Lstat(filepath.Dir(dest)); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		fmt.Fprintf(stderr, "error: %s is a symlink (probably to a checkout); leaving it alone\n", filepath.Dir(dest))
		return 2
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if err := os.WriteFile(dest, []byte(body), 0o644); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	fmt.Fprintln(stdout, "wrote", dest)
	return 0
}
