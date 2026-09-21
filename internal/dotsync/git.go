package dotsync

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

type Git struct{ Repo string }

type gitResult struct {
	Code           int
	Stdout, Stderr string
}

// run executes git non-interactively: it must never block on a password or passphrase prompt,
// because the background agent has nobody to answer it.
func (g *Git) run(inRepo bool, timeout time.Duration, args ...string) (gitResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	full := args
	if inRepo {
		full = append([]string{"-C", g.Repo}, args...)
	}
	cmd := exec.CommandContext(ctx, "git", full...)
	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C", "GIT_OPTIONAL_LOCKS=0")
	if os.Getenv("GIT_SSH_COMMAND") == "" {
		env = append(env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes -o ConnectTimeout=15")
	}
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	res := gitResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if ctx.Err() == context.DeadlineExceeded {
		return res, fmt.Errorf("git %s timed out", args[0])
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		res.Code = exitErr.ExitCode()
		return res, nil
	}
	if err != nil {
		return res, fmt.Errorf("cannot run git (is it installed?): %w", err)
	}
	return res, nil
}

// Try runs git in the repository and reports failure through Code rather than an error.
func (g *Git) Try(args ...string) gitResult {
	r, err := g.run(true, 2*time.Minute, args...)
	if err != nil {
		r.Code, r.Stderr = -1, err.Error()
	}
	return r
}

// Must runs git in the repository and turns a non-zero exit into an error.
func (g *Git) Must(args ...string) (string, error) {
	r, err := g.run(true, 2*time.Minute, args...)
	if err != nil {
		return "", err
	}
	if r.Code != 0 {
		return "", fmt.Errorf("git %s failed: %s", strings.Join(args, " "), lastLine(r.Stderr))
	}
	return strings.TrimSpace(r.Stdout), nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return "unknown error"
}

func configureRepo(g *Git) error {
	settings := [][2]string{
		{"user.name", "dotsync (" + hostname() + ")"},
		{"user.email", "dotsync@" + hostname()},
		{"commit.gpgsign", "false"}, // an agent cannot answer a pinentry prompt
		{"core.autocrlf", "false"},
		{"core.filemode", "true"},
		{"core.symlinks", "true"},
		{"core.hooksPath", "/dev/null"},
		{"gc.auto", "256"},
	}
	for _, kv := range settings {
		if _, err := g.Must("config", kv[0], kv[1]); err != nil {
			return err
		}
	}
	return nil
}

func (c *Ctx) fetch() (bool, string) {
	b := c.Config.Branch
	r := c.Git.Try("fetch", "-q", "--prune", "origin", fmt.Sprintf("+refs/heads/%s:refs/remotes/origin/%s", b, b))
	if r.Code == 0 {
		return true, ""
	}
	return false, lastLine(r.Stderr)
}

// testHookBeforePush lets tests simulate another machine pushing at the worst moment.
var testHookBeforePush func()

func (c *Ctx) push() (bool, string) {
	b := c.Config.Branch
	if testHookBeforePush != nil {
		testHookBeforePush()
	}
	r := c.Git.Try("push", "-q", "origin", "HEAD:refs/heads/"+b)
	if r.Code != 0 {
		return false, lastLine(r.Stderr)
	}
	_, _ = c.Git.Must("update-ref", "refs/remotes/origin/"+b, "HEAD")
	return true, ""
}

func (c *Ctx) originCommit() (string, error) {
	b := c.Config.Branch
	r := c.Git.Try("rev-parse", "-q", "--verify", "refs/remotes/origin/"+b+"^{commit}")
	if r.Code != 0 {
		return "", fmt.Errorf("no local copy of remote branch '%s' yet; check the network/remote and run `dotsync sync`", b)
	}
	return strings.TrimSpace(r.Stdout), nil
}
