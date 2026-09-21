package dotsync

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// packageManager is the package manager that installed this binary. It owns the binary: it
// upgrades it, so `dotsync update` must not replace it, and the background agent must run it
// from a path that survives upgrades.
type packageManager struct {
	name    string // e.g. "Homebrew"
	upgrade string // how to upgrade dotsync, usually a command
	bin     string // stable path of the binary, e.g. /opt/homebrew/bin/dotsync
}

// Overridable in tests.
var (
	aptSourcesDir = "/etc/apt/sources.list.d"
	dpkgOwns      = func() bool { return exec.Command("dpkg-query", "-W", "dotsync").Run() == nil }
)

// running is the resolved path of the running binary.
func running() string {
	exe, err := updateTarget()
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return exe
}

// packagedBin is the stable path of a binary installed by a package manager at exe (a
// resolved path), or "" if no package manager owns it. It's cheap: no commands run.
func packagedBin(exe string) string {
	// Homebrew installs to <prefix>/Cellar/dotsync/<version>/bin/dotsync and links it as
	// <prefix>/bin/dotsync, which keeps pointing at the current version across upgrades.
	if i := strings.LastIndex(exe, "/Cellar/dotsync/"); i > 0 {
		return filepath.Join(exe[:i], "bin", "dotsync")
	}
	// The .deb package installs /usr/bin/dotsync; the install script and `dotsync init`
	// never write there.
	if exe == "/usr/bin/dotsync" {
		return exe
	}
	return ""
}

// packaged reports the package manager that installed the running binary, if any.
func packaged() (packageManager, bool) { return packagedAt(running()) }

func packagedAt(exe string) (packageManager, bool) {
	bin := packagedBin(exe)
	switch {
	case bin == "":
		return packageManager{}, false
	case strings.Contains(exe, "/Cellar/"):
		return packageManager{"Homebrew", "brew upgrade dotsync", bin}, true
	case !dpkgOwns():
		return packageManager{"your system's package manager", "upgrade the dotsync package with your system's package manager", bin}, true
	case fileExists(filepath.Join(aptSourcesDir, "dotsync.sources")) || fileExists(filepath.Join(aptSourcesDir, "dotsync.list")):
		return packageManager{"apt", "sudo apt update && sudo apt install --only-upgrade dotsync", bin}, true
	default:
		// Installed from a downloaded .deb: apt doesn't know where newer versions are.
		return packageManager{"apt", "install the new .deb from https://github.com/" + releases.repo + "/releases/latest, or add the dotsync APT repository", bin}, true
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// upgradeHint says how to install a newer release, given how this binary was installed.
func upgradeHint() string {
	if pm, ok := packaged(); ok {
		return pm.upgrade
	}
	return "dotsync update"
}

// runHint phrases an upgrade hint for a parenthetical: commands are quoted as "run `…`".
func runHint(hint string) string {
	for _, cmd := range []string{"dotsync ", "brew ", "sudo "} {
		if strings.HasPrefix(hint, cmd) {
			return "run `" + hint + "`"
		}
	}
	return hint
}
