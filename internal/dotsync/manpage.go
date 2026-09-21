package dotsync

import (
	"bytes"
	"fmt"
	"io"
	"strings"
)

// ManPage renders dotsync(1) in roff. Commands and their options come from commandGroups and
// each command's own flags, the same source as `dotsync help`, so the two can't drift apart.
// date (YYYY-MM-DD) and version go in the page footer.
func ManPage(version, date string) string {
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	w(`.TH DOTSYNC 1 "%s" "dotsync %s" "User Commands"`, roffEscape(date), roffEscape(version))
	w(".SH NAME")
	w(`dotsync \- set-and-forget dotfile sync for macOS and Linux`)
	w(".SH SYNOPSIS")
	w(`.B dotsync`)
	w(`.I command`)
	w(`.RI [ options ]`)
	w(".SH DESCRIPTION")
	w(".B dotsync")
	w("keeps dotfiles in sync across machines through a private git repository.")
	w("Edit a managed file on any machine and a background agent propagates it to every other machine.")
	w("Local files are backed up before they are replaced or deleted, and a file changed on two machines")
	w("becomes a conflict for you to resolve instead of a silent overwrite.")
	w(".PP")
	w("Run")
	w(`.B dotsync init`)
	w(`.I git\-url`)
	w("on each machine, then")
	w(`.B dotsync add`)
	w(`.I path`)
	w("to start managing files.")
	w(".SH COMMANDS")
	for _, g := range commandGroups {
		w(".SS %s", roffEscape(capitalize(g.title)))
		for _, cmd := range g.commands {
			synopsis, options := commandHelp(cmd)
			w(".TP")
			w(`.B dotsync %s`, roffEscape(synopsis))
			w("%s.", roffEscape(capitalize(cmd.summary)))
			if len(cmd.aliases) > 0 {
				w(`Alias: \fB%s\fR.`, roffEscape(strings.Join(cmd.aliases, ", ")))
			}
			if len(options) > 0 {
				w(".RS")
				for _, o := range options {
					w(".TP")
					w("%s", o.term)
					w("%s", roffEscape(o.text))
				}
				w(".RE")
			}
		}
	}
	w(".SH EXIT STATUS")
	w(".TP\n.B 0\nSuccess.")
	w(".TP\n.B 1\nAn error, or unresolved conflicts.")
	w(".TP\n.B 2\nInvalid usage.")
	w(".SH ENVIRONMENT")
	for _, e := range [][2]string{
		{"HOME", "The home directory. Targets must resolve inside it."},
		{"XDG_CONFIG_HOME, XDG_DATA_HOME, XDG_STATE_HOME", "Where dotsync keeps its own files. Also usable in targets, along with XDG_CACHE_HOME."},
		{"DOTSYNC_HOSTNAME", "Machine name in commit messages (default: the short hostname)."},
		{"DOTSYNC_NO_NOTIFY", "Set to any value to disable desktop notifications."},
		{"GIT_SSH_COMMAND", "Used as-is if set; otherwise dotsync runs ssh in batch mode so it never prompts."},
	} {
		w(".TP\n.B %s\n%s", roffEscape(e[0]), roffEscape(e[1]))
	}
	w(".SH FILES")
	for _, f := range [][2]string{
		{"~/.config/dotsync/config.json", "This machine's settings."},
		{"~/.local/share/dotsync/repo", "This machine's clone of the shared repository."},
		{"~/.local/state/dotsync/", "State, logs, backups and conflict copies."},
	} {
		w(".TP\n.I %s\n%s", roffEscape(f[0]), roffEscape(f[1]))
	}
	w(".SH SEE ALSO")
	w(".BR git (1)")
	w(".PP")
	w("Full documentation: https://pungoyal.github.io/dotsync/")
	w(".SH BUGS")
	w("https://github.com/pungoyal/dotsync/issues")
	return b.String()
}

type manOption struct{ term, text string }

// commandHelp runs `dotsync <cmd> -h` and returns its synopsis and options, parsed from the
// flag package's usage output.
func commandHelp(cmd command) (string, []manOption) {
	var out bytes.Buffer
	_, _ = cmd.run([]string{"-h"}, io.Discard, &out) // always flag.ErrHelp
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	synopsis := strings.TrimPrefix(lines[0], "usage: dotsync ")
	var opts []manOption
	for _, l := range lines[1:] {
		// "  -name type" then an indented description line, or "  -x\tdescription".
		head, text, _ := strings.Cut(l, "\t")
		switch {
		case strings.HasPrefix(head, "  -"):
			name, arg, _ := strings.Cut(strings.TrimSpace(head), " ")
			dashes := `\-`
			if len(name) > 2 {
				dashes = `\-\-`
			}
			term := `.B ` + dashes + roffEscape(name[1:])
			if arg != "" {
				term = `.BI ` + dashes + roffEscape(name[1:]) + ` " ` + roffEscape(arg) + `"`
			}
			opts = append(opts, manOption{term: term, text: strings.TrimSpace(text)})
		case len(opts) > 0:
			o := &opts[len(opts)-1]
			o.text = strings.TrimSpace(o.text + " " + strings.TrimSpace(text))
		}
	}
	return synopsis, opts
}

// roffEscape makes s safe as roff text: backslashes, hyphens and non-ASCII characters are
// escaped, and a line can't start with a control character.
func roffEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\\':
			b.WriteString(`\e`)
		case r == '-':
			b.WriteString(`\-`)
		case r > 0x7f:
			fmt.Fprintf(&b, `\[u%04X]`, r)
		default:
			b.WriteRune(r)
		}
	}
	s = b.String()
	if strings.HasPrefix(s, ".") || strings.HasPrefix(s, "'") {
		s = `\&` + s
	}
	return s
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
