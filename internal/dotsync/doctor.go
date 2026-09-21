package dotsync

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type checkLevel int

const (
	checkOK checkLevel = iota
	checkInfo
	checkWarn
	checkFail
)

type check struct {
	level  checkLevel
	title  string
	detail string // extra context, e.g. the git error
	fix    string // what to do about it
}

type doctor struct {
	checks []check
}

func (d *doctor) add(level checkLevel, title, detail, fix string) {
	d.checks = append(d.checks, check{level, title, detail, fix})
}

func (d *doctor) print(w io.Writer) {
	marks := map[checkLevel]string{checkOK: "✓", checkInfo: "·", checkWarn: "!", checkFail: "✗"}
	for _, c := range d.checks {
		fmt.Fprintf(w, "  %s %s\n", marks[c.level], c.title)
		if c.detail != "" {
			fmt.Fprintf(w, "      %s\n", c.detail)
		}
		if c.fix != "" {
			fmt.Fprintf(w, "      → %s\n", c.fix)
		}
	}
}

func (d *doctor) worst() checkLevel {
	w := checkOK
	for _, c := range d.checks {
		if c.level > w {
			w = c.level
		}
	}
	return w
}

var gitVersionRx = regexp.MustCompile(`(\d+)\.(\d+)`)

// cmdDoctor checks everything dotsync depends on and says how to fix what's wrong.
// Exit status: 0 healthy (warnings allowed), 1 at least one check failed.
func cmdDoctor(args []string, stdout, stderr io.Writer) (int, error) {
	fs := newFlags("doctor", "", stderr)
	if _, err := parseArgs(fs, args); err != nil {
		return 2, err
	}
	d := &doctor{}
	defer func() {
		fmt.Fprintln(stdout, "dotsync doctor")
		d.print(stdout)
		switch d.worst() {
		case checkFail:
			fmt.Fprintln(stdout, "\nSome checks failed; see the → lines above.")
		case checkWarn:
			fmt.Fprintln(stdout, "\nWorking, with warnings.")
		default:
			fmt.Fprintln(stdout, "\nEverything looks good.")
		}
	}()

	d.add(checkOK, versionString(), "", "")
	if cur := currentVersion(); cur != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if latest, err := latestRelease(ctx); err == nil {
			l, _ := parseSemver(latest)
			if c, _ := parseSemver(cur); semverLess(c, l) {
				d.add(checkInfo, "update available: "+cur+" → "+strings.TrimPrefix(latest, "v"), "", "dotsync update")
			}
		}
		cancel()
	}

	// git
	if out, err := exec.Command("git", "--version").Output(); err != nil {
		d.add(checkFail, "git is not installed or not on PATH", "", "install git 2.20 or newer")
		return 1, nil
	} else {
		v := strings.TrimSpace(string(out))
		m := gitVersionRx.FindStringSubmatch(v)
		major, _ := strconv.Atoi(m[1])
		minor, _ := strconv.Atoi(m[2])
		if major < 2 || (major == 2 && minor < 20) {
			d.add(checkFail, v, "dotsync needs git 2.20 or newer", "upgrade git")
		} else {
			d.add(checkOK, v, "", "")
		}
	}

	// configuration and cache clone
	c, err := newCtx(false, true, io.Discard, io.Discard)
	if err != nil {
		d.add(checkFail, "configuration can't be read", err.Error(), "fix or delete "+tilde(NewPaths().Config)+", then run dotsync init")
		return 1, nil
	}
	if c.Config == nil {
		d.add(checkFail, "not set up on this machine", "", "dotsync init <url-of-your-private-dotfiles-repo>")
		return 1, nil
	}
	d.add(checkOK, "configuration: "+tilde(c.Paths.Config), fmt.Sprintf("remote %s, branch %s", c.Config.Remote, c.Config.Branch), "")
	if _, err := os.Stat(filepath.Join(c.Paths.Repo, ".git")); err != nil {
		d.add(checkFail, "cache clone missing: "+tilde(c.Paths.Repo), "", "dotsync init "+c.Config.Remote)
		return 1, nil
	}
	if url, _ := c.Git.Must("remote", "get-url", "origin"); url != c.Config.Remote {
		d.add(checkFail, "cache clone points at a different remote", "clone: "+url+"\nconfig: "+c.Config.Remote,
			"git -C "+tilde(c.Paths.Repo)+" remote set-url origin "+c.Config.Remote)
	}

	// remote reachable without prompts (exactly how the agent will run git)
	r := c.Git.Try("ls-remote", "--heads", "origin", c.Config.Branch)
	switch {
	case r.Code == 0 && hasHead(r.Stdout, c.Config.Branch):
		d.add(checkOK, "remote reachable without prompts", "", "")
	case r.Code == 0:
		d.add(checkFail, "remote has no branch '"+c.Config.Branch+"'", "", "check the branch in "+tilde(c.Paths.Config))
	case authFailure.MatchString(r.Stderr):
		d.add(checkFail, "remote rejects authentication", lastLine(r.Stderr),
			"make `git ls-remote "+c.Config.Remote+"` work without a prompt: https://pungoyal.github.io/dotsync/guides/background-agent/#git-access-without-prompts")
	default:
		d.add(checkWarn, "remote unreachable right now", lastLine(r.Stderr), "check your network; changes wait until it's reachable")
	}

	// background agent
	status := agentStatus(c)
	switch {
	case strings.Contains(status, "not installed") || strings.Contains(status, "not loaded"):
		d.add(checkFail, "background agent: "+strings.TrimSuffix(status, " (run `dotsync agent install`)"), "", "dotsync agent install")
	default:
		d.add(checkOK, "background agent: "+status, "", "")
		if exe := agentCommand(c)[0]; exe != "" {
			if _, err := os.Stat(exe); err != nil {
				d.add(checkFail, "the agent runs "+tilde(exe)+", which doesn't exist", "", "dotsync agent install")
			}
		}
	}
	if _, err := exec.LookPath("dotsync"); err != nil {
		d.add(checkWarn, "dotsync is not on your PATH", "", "add "+tilde(filepath.Dir(c.Paths.Bin))+" to PATH")
	}

	// last sync
	st, err := loadState(c)
	if err != nil {
		d.add(checkFail, "state can't be read", err.Error(), "")
		return 1, nil
	}
	interval := time.Duration(max(c.Config.Interval, 60)) * time.Second
	if ls := st.LastSync; ls == nil {
		d.add(checkWarn, "no sync has completed yet", "", "dotsync sync")
	} else {
		age := time.Since(lastSyncTime(c, st)).Round(time.Minute)
		switch {
		case ls.Outcome == "offline":
			d.add(checkWarn, fmt.Sprintf("last sync %s ago could not reach the remote", age), ls.FetchError, "")
		case age > 3*interval && age > time.Hour:
			d.add(checkWarn, fmt.Sprintf("last sync was %s ago", age), "the agent may not be running", "dotsync agent status; see "+tilde(c.Paths.AgentLog))
		default:
			d.add(checkOK, fmt.Sprintf("last sync %s ago (%s)", age, ls.Outcome), "", "")
		}
	}

	// managed files, planned against the last fetched state (no network, no changes)
	err = withLock(c, 5*time.Second, func() error {
		p, err := buildPlan(c, st, false)
		if err != nil {
			return err
		}
		counts := map[string]int{}
		for _, it := range p.Items {
			counts[it.Action]++
		}
		d.add(checkOK, plural(len(p.Entries), "managed entry", "managed entries")+", "+plural(len(p.Items), "file", "files"), "", "")
		if n := counts[actConflict]; n > 0 {
			d.add(checkWarn, fmt.Sprintf("%d conflict(s)", n), "", "dotsync status; then dotsync resolve <path> --keep local|remote")
		}
		if n := counts[actBlocked]; n > 0 {
			d.add(checkWarn, fmt.Sprintf("%d file(s) held back as possible secrets", n), "", "dotsync status -v; https://pungoyal.github.io/dotsync/guides/secrets/")
		}
		if n := counts[actError] + len(p.EntryErrors); n > 0 {
			d.add(checkWarn, fmt.Sprintf("%d entry or file error(s)", n), "", "dotsync status -v")
		}
		if n := len(st.PendingOps); n > 0 {
			d.add(checkInfo, fmt.Sprintf("%d queued change(s) not yet pushed", n), "", "")
		}
		if enc := needsAgeKey(p); len(enc) > 0 && ageKeyFile() == "" {
			d.add(checkWarn, fmt.Sprintf("%s encrypted with age, but this machine has no age key", plural(len(enc), "managed file is", "managed files are")),
				"e.g. "+enc[0]+". Keys are never synced, so each machine needs its own copy",
				"copy your key to ~/.config/mise/age.txt or ~/.config/sops/age/keys.txt (chmod 600); ignore this if you decrypt with an SSH key")
		}
		return nil
	})
	switch {
	case errors.Is(err, ErrBusy):
		d.add(checkInfo, "a sync is running; skipped checking managed files", "", "")
	case err != nil:
		d.add(checkFail, "can't evaluate managed files", err.Error(), "")
	}

	// backups
	files, bytes := backupUsage(c.Paths.Backups)
	keep := "kept forever"
	if days := c.Config.backupRetention(); days > 0 {
		keep = fmt.Sprintf("older than %d days are pruned", days)
	}
	d.add(checkInfo, fmt.Sprintf("backups: %d file(s), %s in %s (%s)", files, humanBytes(bytes), tilde(c.Paths.Backups), keep), "", "")

	if d.worst() == checkFail {
		return 1, nil
	}
	return 0, nil
}

// needsAgeKey lists managed files (as displayed) whose content can only be read with an age key:
// age files and sops files with age recipients. Like the secret rules, it knows file formats, not
// the tools that write them.
func needsAgeKey(p *Plan) []string {
	var out []string
	for _, it := range p.Items {
		data, err := os.ReadFile(it.Path())
		if err != nil {
			if data, err = os.ReadFile(it.Repo); err != nil {
				continue
			}
		}
		switch encryption(data) {
		case "age":
			out = append(out, it.Display())
		case "sops":
			if sopsAgeRecipient.Match(data) { // KMS, PGP or Vault keys aren't ours to check
				out = append(out, it.Display())
			}
		}
	}
	return out
}

var sopsAgeRecipient = regexp.MustCompile(`recipient"?\s*[:=]\s*"?age1`)

// ageKeyFile returns where this machine keeps an age key, or "". sops's own locations come first;
// mise's (which it uses for sops files too) are included because the guide recommends them.
func ageKeyFile() string {
	for _, v := range []string{"SOPS_AGE_KEY", "SOPS_AGE_KEY_FILE", "SOPS_AGE_KEY_CMD", "MISE_SOPS_AGE_KEY", "MISE_SOPS_AGE_KEY_FILE"} {
		if os.Getenv(v) != "" {
			return "$" + v
		}
	}
	cfg := mustEnvDir("XDG_CONFIG_HOME")
	for _, f := range []string{
		filepath.Join(cfg, "sops", "age", "keys.txt"),
		filepath.Join(homeDir(), "Library", "Application Support", "sops", "age", "keys.txt"),
		filepath.Join(cfg, "mise", "age.txt"),
	} {
		if _, err := os.Stat(f); err == nil {
			return f
		}
	}
	return ""
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
