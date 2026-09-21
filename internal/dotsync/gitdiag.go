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

// problem and fix are nil-safe accessors, for when there's nothing to report.
func (g *gitProblem) problem() string {
	if g == nil {
		return ""
	}
	return g.Problem
}

func (g *gitProblem) fix() string {
	if g == nil {
		return ""
	}
	return g.Fix
}

// gitFailures maps git's error output to an explanation. Hints are built from the configured
// remote only; nothing here knows about particular hosts or tools.
var gitFailures = []struct {
	rx      *regexp.Regexp
	problem string                   // may contain %s for the remote's host
	fix     func(r gitRemote) string // how to fix it
}{
	{regexp.MustCompile(`(?i)host key verification failed`),
		"SSH doesn't know %s's host key yet, and dotsync can't ask you to accept it",
		func(r gitRemote) string { return "connect once and accept the key: ssh -T " + r.sshTarget }},
	{regexp.MustCompile(`(?i)permission denied \(publickey`),
		"%s rejected the SSH key, or no key is available to git",
		func(r gitRemote) string {
			return "add your key to ssh-agent (ssh-add) and check with: ssh -T " + r.sshTarget + "; see " + agentGuide
		}},
	{regexp.MustCompile(`(?i)could not read (username|password)|terminal prompts disabled`),
		"git has no stored credentials for %s, and dotsync can't prompt for them",
		func(r gitRemote) string {
			return "store them with a git credential helper, then check with: git ls-remote " + r.url + "; see " + agentGuide
		}},
	{regexp.MustCompile(`(?i)authentication failed|invalid username or password|returned error: 40[13]`),
		"%s rejected the stored credentials (expired or revoked?)",
		func(r gitRemote) string {
			return "renew them in your credential helper, then check with: git ls-remote " + r.url
		}},
	{regexp.MustCompile(`(?i)repository not found|does not appear to be a git repository|not found`),
		"the repository doesn't exist at that URL, or this account can't access it",
		func(gitRemote) string {
			return "check the remote in ~/.config/dotsync/config.json and your access to it"
		}},
}

// missingProgram matches git (or a helper it runs) failing to find a program.
var missingProgram = regexp.MustCompile(`(?m):\s*([^\s:]+):\s*(?:No such file or directory|command not found|not found)\s*$`)

// diagnoseGit turns git's error output into a specific problem and fix, or nil when there's
// nothing the user needs to do (e.g. the network is down).
func diagnoseGit(c *Ctx, stderr string) *gitProblem {
	if stderr == "" {
		return nil
	}
	if m := missingProgram.FindStringSubmatch(stderr); m != nil {
		return missingProgramProblem(c, m[1])
	}
	remote := parseRemote(c)
	for _, f := range gitFailures {
		if f.rx.MatchString(stderr) {
			problem := f.problem
			if strings.Contains(problem, "%s") {
				problem = fmt.Sprintf(problem, remote.host)
			}
			return &gitProblem{Problem: problem, Fix: f.fix(remote)}
		}
	}
	return nil
}

// missingProgramProblem explains a git setting that runs a program this machine doesn't have.
// The usual cause is a git config synced from another machine that names the program by an
// absolute path.
func missingProgramProblem(c *Ctx, prog string) *gitProblem {
	p := &gitProblem{Problem: fmt.Sprintf("git is configured to run %s, which isn't installed on this machine", prog)}
	s := findSetting(c, prog)
	if s.file != "" {
		p.Problem += " (set in " + tilde(s.file) + ")"
	}
	if s.managed {
		p.Problem += ". That file is synced by dotsync, so the setting probably came from another machine"
	}
	switch {
	case strings.Contains(prog, "/") && s.value != "":
		p.Fix = fmt.Sprintf("name the program without its path so each machine finds its own copy: %s = %s",
			s.key, strings.Replace(s.value, prog, filepath.Base(prog), 1))
	case strings.Contains(prog, "/"):
		p.Fix = "name " + filepath.Base(prog) + " without its path so each machine finds its own copy"
	default:
		p.Fix = "install " + prog + " on this machine, or change the setting that runs it"
	}
	p.Fix += ", or keep machine-specific settings in a local, unsynced file: " +
		"https://pungoyal.github.io/dotsync/guides/differences/#tools-that-write-absolute-paths"
	return p
}

type gitRemote struct {
	url       string
	host      string
	sshTarget string // [user@]host, as ssh expects it
}

// parseRemote extracts the host (and ssh login) from an scp-style or URL remote.
func parseRemote(c *Ctx) gitRemote {
	r := gitRemote{host: "your git host", sshTarget: "your git host"}
	if c == nil || c.Config == nil || c.Config.Remote == "" {
		return r
	}
	r.url = c.Config.Remote
	rest := r.url
	if _, after, ok := strings.Cut(rest, "://"); ok {
		rest = after
	}
	if i := strings.IndexAny(rest, ":/"); i >= 0 {
		rest = rest[:i]
	}
	if rest == "" {
		return r
	}
	r.sshTarget = rest
	r.host = rest[strings.LastIndex(rest, "@")+1:]
	return r
}

// gitSetting is a git config value that runs a program, and where it's set.
type gitSetting struct {
	file, key, value string
	managed          bool // the file is one dotsync syncs
}

// findSetting finds the git config value that mentions prog, and the file that sets it.
func findSetting(c *Ctx, prog string) gitSetting {
	if c == nil || c.Git == nil || c.Paths == nil {
		return gitSetting{}
	}
	// --show-origin --list prints "file:<path>\t<key>=<value>" for every setting.
	for _, line := range strings.Split(c.Git.Try("config", "--show-origin", "--list").Stdout, "\n") {
		origin, kv, ok := strings.Cut(line, "\t")
		key, value, _ := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(origin, "file:") || !strings.Contains(value, prog) {
			continue
		}
		file := strings.TrimPrefix(origin, "file:")
		return gitSetting{file: file, key: key[strings.LastIndex(key, ".")+1:], value: value, managed: isManagedPath(c, file)}
	}
	return gitSetting{}
}

// isManagedPath reports whether path is (or is inside) a target managed on this machine,
// comparing both as given and with symlinks resolved.
func isManagedPath(c *Ctx, path string) bool {
	m, err := loadManifest(c.Paths.Repo)
	if err != nil {
		return false
	}
	for _, raw := range m.Entries {
		e, err := parseEntry(raw)
		if err != nil {
			continue
		}
		t, err := expandTarget(e.TargetSpec, c.Paths)
		if err == nil && (isWithin(path, t) || isWithin(resolved(path), resolved(t))) {
			return true
		}
	}
	return false
}

// resolved is path with symlinks resolved, or path itself if that fails.
func resolved(path string) string {
	if r, err := filepath.EvalSymlinks(path); err == nil {
		return r
	}
	return path
}
