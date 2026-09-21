package dotsync

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var xdgDefaults = map[string]string{
	"XDG_CONFIG_HOME": ".config",
	"XDG_DATA_HOME":   ".local/share",
	"XDG_STATE_HOME":  ".local/state",
	"XDG_CACHE_HOME":  ".cache",
}

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return filepath.Clean(h)
	}
	h, _ := os.UserHomeDir()
	return filepath.Clean(h)
}

// envDir returns the directory named by an environment variable, applying XDG defaults.
func envDir(name string) (string, bool) {
	if name == "HOME" {
		return homeDir(), true
	}
	if v := os.Getenv(name); v != "" && filepath.IsAbs(v) {
		return filepath.Clean(v), true
	}
	if d, ok := xdgDefaults[name]; ok {
		return filepath.Join(homeDir(), d), true
	}
	return "", false
}

func mustEnvDir(name string) string {
	d, _ := envDir(name)
	return d
}

func hostname() string {
	if h := os.Getenv("DOTSYNC_HOSTNAME"); h != "" {
		return h
	}
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "unknown"
	}
	return strings.SplitN(h, ".", 2)[0]
}

func osName() string { return runtime.GOOS }

// Paths are all machine-local; none of them is ever synchronized.
type Paths struct {
	ConfigDir, Config          string
	DataDir, Repo              string
	StateDir, State, Lock, Log string
	AgentLog, Backups          string
	Conflicts, Bin             string
}

func NewPaths() *Paths {
	p := &Paths{}
	p.ConfigDir = filepath.Join(mustEnvDir("XDG_CONFIG_HOME"), "dotsync")
	p.Config = filepath.Join(p.ConfigDir, "config.json")
	p.DataDir = filepath.Join(mustEnvDir("XDG_DATA_HOME"), "dotsync")
	p.Repo = filepath.Join(p.DataDir, "repo")
	p.StateDir = filepath.Join(mustEnvDir("XDG_STATE_HOME"), "dotsync")
	p.State = filepath.Join(p.StateDir, "state.json")
	p.Lock = filepath.Join(p.StateDir, "lock")
	p.Log = filepath.Join(p.StateDir, "sync.log")
	p.AgentLog = filepath.Join(p.StateDir, "agent.log")
	p.Backups = filepath.Join(p.StateDir, "backups")
	p.Conflicts = filepath.Join(p.StateDir, "conflicts")
	p.Bin = filepath.Join(homeDir(), ".local", "bin", "dotsync")
	return p
}

func (p *Paths) OwnDirs() []string { return []string{p.ConfigDir, p.DataDir, p.StateDir} }

func isWithin(path, root string) bool {
	path, root = filepath.Clean(path), filepath.Clean(root)
	return path == root || strings.HasPrefix(path, strings.TrimRight(root, "/")+"/")
}

func overlaps(a, b string) bool { return isWithin(a, b) || isWithin(b, a) }

// realHome is $HOME with symlinks resolved (e.g. /home -> /data/home).
func realHome() string {
	if r, err := filepath.EvalSymlinks(homeDir()); err == nil {
		return r
	}
	return homeDir()
}

// tilde renders a path relative to $HOME as ~/...
func tilde(p string) string {
	h := homeDir()
	if isWithin(p, h) {
		rel, _ := filepath.Rel(h, p)
		if rel == "." {
			return "~"
		}
		return "~/" + rel
	}
	return p
}

func homeRel(p string) string {
	if isWithin(p, homeDir()) {
		rel, _ := filepath.Rel(homeDir(), p)
		return filepath.ToSlash(rel)
	}
	return p
}

func nowISO() string { return time.Now().Format(time.RFC3339) }
