package dotsync

import (
	"bytes"
	"errors"
	"fmt"
	"html"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	agentLabel = "io.github.dotsync"
	cronTag    = "# dotsync-agent"
)

func agentCommand(c *Ctx) []string {
	exe := c.Paths.Bin
	if _, err := os.Stat(exe); err != nil {
		exe, _ = os.Executable()
	}
	return []string{exe, "sync", "-q"}
}

// agentPath gives the agent a PATH that finds git; launchd and cron start with a minimal one.
func agentPath() string {
	var dirs []string
	if g, err := exec.LookPath("git"); err == nil {
		dirs = append(dirs, filepath.Dir(g))
	}
	dirs = append(dirs, "/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin")
	var out []string
	for _, d := range dirs {
		if !containsStr(out, d) {
			out = append(out, d)
		}
	}
	return strings.Join(out, ":")
}

// agentEnv is the environment baked into the scheduled job: PATH plus HOME and any XDG base
// directories set when the agent was installed, so the job finds the same config, state and
// targets as the interactive shell.
func agentEnv() [][2]string {
	env := [][2]string{{"PATH", agentPath()}, {"HOME", homeDir()}}
	for _, v := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"} {
		if val := os.Getenv(v); val != "" && filepath.IsAbs(val) {
			env = append(env, [2]string{v, val})
		}
	}
	return env
}

func launchdPlist() string {
	return filepath.Join(homeDir(), "Library", "LaunchAgents", agentLabel+".plist")
}

func systemdDir() string { return filepath.Join(mustEnvDir("XDG_CONFIG_HOME"), "systemd", "user") }

func runQuiet(name string, args ...string) (string, int) {
	cmd := exec.Command(name, args...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		return err.Error(), -1
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return out.String(), ee.ExitCode()
		}
		if err != nil {
			return err.Error(), -1
		}
		return out.String(), 0
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		return "timed out", -1
	}
}

func haveSystemdUser() bool {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false
	}
	_, code := runQuiet("systemctl", "--user", "show-environment")
	return code == 0
}

var shellSafe = regexp.MustCompile(`^[\w@%+=:,./-]+$`)

func shellQuote(s string) string {
	if shellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func agentInstall(c *Ctx) (string, error) {
	interval := c.Config.Interval
	if interval <= 0 {
		interval = defaultInterval
	}
	cmd := agentCommand(c)
	if err := os.MkdirAll(c.Paths.StateDir, 0o700); err != nil {
		return "", err
	}
	switch {
	case osName() == "darwin":
		plist := launchdPlistContent(c, cmd, interval)
		if err := atomicWrite(launchdPlist(), []byte(plist), 0o644); err != nil {
			return "", err
		}
		domain := fmt.Sprintf("gui/%d", os.Getuid())
		runQuiet("launchctl", "bootout", domain+"/"+agentLabel)
		if out, code := runQuiet("launchctl", "bootstrap", domain, launchdPlist()); code != 0 {
			if _, code := runQuiet("launchctl", "load", "-w", launchdPlist()); code != 0 {
				return "", fmt.Errorf("launchctl bootstrap failed: %s", lastLine(out))
			}
		}
		return fmt.Sprintf("launchd agent %s, every %ds", agentLabel, interval), nil
	case haveSystemdUser():
		var quoted []string
		for _, a := range cmd {
			quoted = append(quoted, systemdQuote(a))
		}
		var env strings.Builder
		for _, kv := range agentEnv() {
			fmt.Fprintf(&env, "Environment=%s\n", systemdQuote(kv[0]+"="+kv[1]))
		}
		service := fmt.Sprintf("[Unit]\nDescription=dotsync: synchronize dotfiles\nAfter=network-online.target\n\n"+
			"[Service]\nType=oneshot\n%sExecStart=%s\nNice=10\nIOSchedulingClass=idle\n",
			env.String(), strings.Join(quoted, " "))
		timer := fmt.Sprintf("[Unit]\nDescription=dotsync: periodic dotfile sync\n\n"+
			"[Timer]\nOnBootSec=1min\nOnUnitActiveSec=%ds\nPersistent=true\n\n[Install]\nWantedBy=timers.target\n", interval)
		if err := atomicWrite(filepath.Join(systemdDir(), "dotsync.service"), []byte(service), 0o644); err != nil {
			return "", err
		}
		if err := atomicWrite(filepath.Join(systemdDir(), "dotsync.timer"), []byte(timer), 0o644); err != nil {
			return "", err
		}
		runQuiet("systemctl", "--user", "daemon-reload")
		if out, code := runQuiet("systemctl", "--user", "enable", "--now", "dotsync.timer"); code != 0 {
			return "", fmt.Errorf("systemctl --user enable failed: %s", lastLine(out))
		}
		return fmt.Sprintf("systemd user timer dotsync.timer, every %ds", interval), nil
	default:
		if _, err := exec.LookPath("crontab"); err != nil {
			return "", errors.New("no launchd, systemd --user or crontab found; run `dotsync sync -q` from your own scheduler")
		}
		schedule, every := cronSchedule(interval)
		var words []string
		for _, kv := range agentEnv() {
			words = append(words, kv[0]+"="+shellQuote(kv[1]))
		}
		for _, a := range cmd {
			words = append(words, shellQuote(a))
		}
		line := fmt.Sprintf("%s %s >>%s 2>&1 %s", schedule, strings.Join(words, " "), shellQuote(c.Paths.AgentLog), cronTag)
		if err := setCronLine(strings.ReplaceAll(line, "%", `\%`)); err != nil {
			return "", err
		}
		return "cron, " + every, nil
	}
}

func launchdPlistContent(c *Ctx, cmd []string, interval int) string {
	var args strings.Builder
	for _, a := range cmd {
		fmt.Fprintf(&args, "\n    <string>%s</string>", html.EscapeString(a))
	}
	var env strings.Builder
	for _, kv := range agentEnv() {
		fmt.Fprintf(&env, "\n    <key>%s</key><string>%s</string>", kv[0], html.EscapeString(kv[1]))
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array>%s
  </array>
  <key>StartInterval</key><integer>%d</integer>
  <key>RunAtLoad</key><true/>
  <key>ProcessType</key><string>Background</string>
  <key>LowPriorityIO</key><true/>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
  <key>EnvironmentVariables</key>
  <dict>%s
  </dict>
</dict>
</plist>
`, agentLabel, args.String(), interval, html.EscapeString(c.Paths.AgentLog), html.EscapeString(c.Paths.AgentLog), env.String())
}

// cronSchedule maps an interval in seconds onto a cron schedule (minute granularity).
func cronSchedule(interval int) (string, string) {
	minutes := max(1, interval/60)
	switch {
	case minutes < 60:
		return fmt.Sprintf("*/%d * * * *", minutes), fmt.Sprintf("every %d min", minutes)
	case minutes < 24*60:
		h := minutes / 60
		return fmt.Sprintf("0 */%d * * *", h), fmt.Sprintf("every %d h", h)
	default:
		return "0 0 * * *", "daily"
	}
}

// systemdQuote quotes a word for ExecStart=/Environment= and escapes % specifiers.
func systemdQuote(s string) string {
	s = strings.ReplaceAll(s, "%", "%%")
	if shellSafe.MatchString(s) {
		return s
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", "$$").Replace(s) + `"`
}

func setCronLine(line string) error {
	current, code := runQuiet("crontab", "-l")
	var lines []string
	if code == 0 {
		for _, l := range strings.Split(strings.TrimRight(current, "\n"), "\n") {
			if l != "" && !strings.Contains(l, cronTag) {
				lines = append(lines, l)
			}
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	cmd := exec.Command("crontab", "-")
	cmd.Stdin = strings.NewReader(strings.Join(lines, "\n") + "\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("crontab update failed: %s", lastLine(string(out)))
	}
	return nil
}

func agentUninstall(c *Ctx) string {
	if osName() == "darwin" {
		runQuiet("launchctl", "bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), agentLabel))
		if err := os.Remove(launchdPlist()); err == nil {
			return "launchd agent removed"
		}
		return "no agent was installed"
	}
	var removed []string
	if haveSystemdUser() {
		runQuiet("systemctl", "--user", "disable", "--now", "dotsync.timer")
		for _, n := range []string{"dotsync.timer", "dotsync.service"} {
			if os.Remove(filepath.Join(systemdDir(), n)) == nil {
				removed = append(removed, n)
			}
		}
		runQuiet("systemctl", "--user", "daemon-reload")
	}
	if out, code := runQuiet("crontab", "-l"); code == 0 && strings.Contains(out, cronTag) {
		if setCronLine("") == nil {
			removed = append(removed, "cron entry")
		}
	}
	if len(removed) == 0 {
		return "no agent was installed"
	}
	return "removed " + strings.Join(removed, ", ")
}

func agentStatus(c *Ctx) string {
	if osName() == "darwin" {
		if _, err := os.Stat(launchdPlist()); err != nil {
			return "not installed (run `dotsync agent install`)"
		}
		if _, code := runQuiet("launchctl", "print", fmt.Sprintf("gui/%d/%s", os.Getuid(), agentLabel)); code == 0 {
			return "launchd agent loaded"
		}
		return "launchd agent installed but not loaded (run `dotsync agent install`)"
	}
	if _, err := os.Stat(filepath.Join(systemdDir(), "dotsync.timer")); err == nil {
		out, _ := runQuiet("systemctl", "--user", "is-active", "dotsync.timer")
		return "systemd timer (" + strings.TrimSpace(out) + ")"
	}
	if out, code := runQuiet("crontab", "-l"); code == 0 && strings.Contains(out, cronTag) {
		return "cron"
	}
	return "not installed (run `dotsync agent install`)"
}

func cmdAgent(args []string, stdout, stderr io.Writer) (int, error) {
	fs := newFlags("agent", "install|uninstall|status", stderr)
	pos, err := parseArgs(fs, args)
	if err != nil {
		return 2, err
	}
	if len(pos) != 1 {
		fs.Usage()
		return 2, errors.New("expected install, uninstall or status")
	}
	c, err := newCtx(true, false, stdout, stderr)
	if err != nil {
		return 1, err
	}
	switch pos[0] {
	case "install":
		msg, err := agentInstall(c)
		if err != nil {
			return 1, err
		}
		fmt.Fprintln(stdout, msg)
	case "uninstall":
		fmt.Fprintln(stdout, agentUninstall(c))
	case "status":
		fmt.Fprintln(stdout, agentStatus(c))
	default:
		return 2, fmt.Errorf("unknown agent action %q", pos[0])
	}
	return 0, nil
}
