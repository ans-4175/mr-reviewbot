package main

import (
	"os"
	"path/filepath"

	"github.com/ans-4175/mr-reviewbot/internal/app"
)

func main() {
	// launchd/ssh PATHs omit Homebrew and ~/.local/bin, where glab and claude live.
	home, _ := os.UserHomeDir()
	os.Setenv("PATH", "/opt/homebrew/bin:/usr/local/bin:"+filepath.Join(home, ".local", "bin")+":"+os.Getenv("PATH"))
	os.Exit(app.Run(os.Args[1:], os.Stdout, os.Stderr))
}
