package dotsync

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

const agentGuide = "https://pungoyal.github.io/dotsync/guides/background-agent/#git-access-without-prompts"

// gitProblem explains a git failure that needs the user, with a concrete next step. Network
// outages aren't problems in this sense: syncs simply resume once the remote is reachable.
type gitProblem struct {
	Problem string
	Fix     string
}

var (
	missingProgram = regexp.MustCompile(`(?m):\s*([^\s:]+):\s*(?:No such file or directory|command not found|not found)\s*$`)
	gitErrors      = []struct {
		rx      *regexp.Regexp
		problem string
		fix     func(host string) string
	}{
		{regexp.MustCompile(`(?i)host key verification failed`),
			"SSH doesn't know %s's host key yet, and dotsync can't ask you to accept it",
			func(h string) string {
				return fmt.Sprintf("connect once interactively and accept the key: ssh -T git@%s", h)
			}},
		{regexp.MustCompile(`(?i)permission denied \(publickey`),
			"%s rejected the SSH key, or no key is available to git",
			func(h string) string {
				return fmt.Sprintf("load your key (macOS: ssh-add --apple-use-keychain ~/.ssh/id_ed25519) and check: ssh -T git@%s; see %s", h, agentGuide)
			}},
		{regexp.MustCompile(`(?i)could not read (username|password)|terminal prompts disabled`),
			"git has no stored credentials for %s, and dotsync can't prompt for them",
			func(h string) string {
				if strings.Contains(h, "github.com") {
					return "sign in once and let git reuse it: gh auth login, then gh auth setup-git; see " + agentGuide
				}
				return "store credentials with a credential helper (osxkeychain on macOS, libsecret on Linux); see " + agentGuide
			}},
		{regexp.MustCompile(`(?i)authentication failed|invalid username or password|returned error: 40[13]`),
			"%s rejected the stored credentials (expired or revoked?)",
			func(h string) string {
				if strings.Contains(h, "github.com") {
					return "sign in again: gh auth login"
				}
				return "update the credentials stored for this remote"
			}},
		{regexp.MustCompile(`(?i)repository not found|does not appear to be a git repository|not found`),
			"the repository doesn't exist at that URL, or this account can't access it",
			func(string) string {
				return "check the remote in ~/.config/dotsync/config.json and your access to it"
			}},
	}
)

// diagnoseGit turns git's error output into a specific problem and fix, or nil when there's
// nothing the user needs to do (e.g. the network is down).
func diagnoseGit(c *Ctx, stderr string) *gitProblem {
	if stderr == "" {
		return nil
	}
	host := remoteHost("")
	if c != nil && c.Config != nil {
		host = remoteHost(c.Config.Remote)
	}
	// A credential helper (or ssh) that isn't installed here. The most common cause: a git config
	// synced from another machine that refers to a program by an absolute path.
	if m := missingProgram.FindStringSubmatch(stderr); m != nil {
		prog := m[1]
		where, managed := helperOrigin(c, prog)
		p := &gitProblem{Problem: fmt.Sprintf("git is configured to run %s, which isn't installed on this machine", prog)}
		if where != "" {
			p.Problem += " (set in " + tilde(where) + ")"
		}
		if managed {
			p.Problem += ". That file is synced by dotsync, so the setting probably came from another machine where the program lives elsewhere"
		}
		if strings.Contains(prog, "/") {
			p.Fix = fmt.Sprintf("refer to the program by name so each machine finds its own copy (e.g. helper = !%s auth git-credential), "+
				"or keep machine-specific settings in a local, unsynced file: https://pungoyal.github.io/dotsync/guides/differences/#tools-that-write-absolute-paths",
				filepath.Base(prog))
		} else {
			p.Fix = "install " + prog + " on this machine, or change the credential helper in that config file"
		}
		return p
	}
	for _, e := range gitErrors {
		if e.rx.MatchString(stderr) {
			problem := e.problem
			if strings.Contains(problem, "%s") {
				problem = fmt.Sprintf(problem, host)
			}
			return &gitProblem{Problem: problem, Fix: e.fix(host)}
		}
	}
	return nil
}

// remoteHost extracts the host from an scp-style or URL remote.
func remoteHost(remote string) string {
	r := remote
	if i := strings.Index(r, "://"); i >= 0 {
		r = r[i+3:]
	}
	if i := strings.Index(r, "@"); i >= 0 {
		r = r[i+1:]
	}
	if i := strings.IndexAny(r, ":/"); i >= 0 {
		r = r[:i]
	}
	if r == "" {
		return "your git host"
	}
	return r
}

// helperOrigin finds which config file sets a credential helper that runs prog, and whether that
// file is one dotsync manages.
func helperOrigin(c *Ctx, prog string) (string, bool) {
	if c == nil || c.Git == nil || c.Paths == nil {
		return "", false
	}
	r := c.Git.Try("config", "--show-origin", "--get-regexp", `^credential\..*helper$`)
	for _, line := range strings.Split(r.Stdout, "\n") {
		if !strings.Contains(line, prog) || !strings.HasPrefix(line, "file:") {
			continue
		}
		file := strings.TrimPrefix(strings.SplitN(line, "\t", 2)[0], "file:")
		return file, isManagedPath(c, file)
	}
	return "", false
}

// isManagedPath reports whether path is (or is inside) a target managed on this machine.
func isManagedPath(c *Ctx, path string) bool {
	m, err := loadManifest(c.Paths.Repo)
	if err != nil {
		return false
	}
	real := path
	if r, err := filepath.EvalSymlinks(path); err == nil {
		real = r
	}
	for _, raw := range m.Entries {
		e, err := parseEntry(raw)
		if err != nil {
			continue
		}
		t, err := expandTarget(e.TargetSpec, c.Paths)
		if err != nil {
			continue
		}
		rt := t
		if r, err := filepath.EvalSymlinks(t); err == nil {
			rt = r
		}
		if isWithin(path, t) || isWithin(real, rt) {
			return true
		}
	}
	return false
}
