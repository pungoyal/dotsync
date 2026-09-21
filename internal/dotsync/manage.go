package dotsync

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
)

// boolFlag records whether a boolean flag was given at all, so "--allow-secrets=false" can be
// told apart from "not mentioned".
type boolFlag struct {
	set, value bool
}

func (b *boolFlag) String() string   { return fmt.Sprint(b.value) }
func (b *boolFlag) IsBoolFlag() bool { return true }
func (b *boolFlag) Set(s string) error {
	switch s {
	case "true", "1", "":
		b.value = true
	case "false", "0":
		b.value = false
	default:
		return fmt.Errorf("expected true or false, got %q", s)
	}
	b.set = true
	return nil
}

func cmdSet(args []string, stdout, stderr io.Writer) (int, error) {
	fs := newFlags("set", "<path|source> [changes]", stderr)
	desc := fs.String("d", "", "new description")
	fs.StringVar(desc, "description", "", "same as -d")
	var addIgnore, removeIgnore, oses stringList
	fs.Var(&addIgnore, "ignore", "add an ignore pattern (repeatable)")
	fs.Var(&removeIgnore, "unignore", "remove an ignore pattern (repeatable)")
	clearIgnore := fs.Bool("clear-ignore", false, "remove every ignore pattern")
	fs.Var(&oses, "os", "only manage on this OS: darwin or linux (repeatable; replaces the current list)")
	anyOS := fs.Bool("any-os", false, "manage on every OS (removes the os restriction)")
	mode := fs.String("mode", "", `force permissions on every machine, e.g. "0600"`)
	noMode := fs.Bool("no-mode", false, "stop forcing permissions")
	write := fs.String("write", "", "how files are replaced: atomic or inplace")
	var allowSecrets boolFlag
	fs.Var(&allowSecrets, "allow-secrets", "allow content that looks like credentials (use --allow-secrets=false to undo)")
	noSync := fs.Bool("no-sync", false, "only queue the change")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return 2, err
	}
	if len(pos) != 1 {
		fs.Usage()
		return 2, errors.New("expected exactly one path or source")
	}

	op := &Op{ID: newOpID(), Op: "update", Fields: map[string]any{}, Queued: nowISO()}
	var changes []string
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "d", "description":
			op.Fields["description"] = *desc
			changes = append(changes, "description")
		case "write":
			op.Fields["write"] = *write
			changes = append(changes, "write = "+*write)
		case "mode":
			op.Fields["mode"] = *mode
			changes = append(changes, "mode = "+*mode)
		}
	})
	if *noMode {
		if _, ok := op.Fields["mode"]; ok {
			return 2, errors.New("--mode and --no-mode are mutually exclusive")
		}
		op.Unset = append(op.Unset, "mode")
		changes = append(changes, "no forced mode")
	}
	if len(oses) > 0 && *anyOS {
		return 2, errors.New("--os and --any-os are mutually exclusive")
	}
	if err := validateOS(oses); err != nil {
		return 2, err
	}
	if len(oses) > 0 {
		list := make([]any, 0, len(oses))
		for _, o := range oses {
			list = append(list, o)
		}
		op.Fields["os"] = list
		changes = append(changes, "os = "+strings.Join(oses, ", "))
	}
	if *anyOS {
		op.Unset = append(op.Unset, "os")
		changes = append(changes, "any OS")
	}
	if *clearIgnore {
		if len(removeIgnore) > 0 {
			return 2, errors.New("--clear-ignore and --unignore are mutually exclusive")
		}
		op.Unset = append(op.Unset, "ignore")
		changes = append(changes, "no ignore patterns")
	}
	for _, p := range addIgnore {
		if p = strings.Trim(p, "/"); p != "" {
			op.AddIgnore = append(op.AddIgnore, p)
		}
	}
	op.RemoveIgnore = append(op.RemoveIgnore, removeIgnore...)
	if len(op.AddIgnore) > 0 {
		changes = append(changes, "ignore + "+strings.Join(op.AddIgnore, ", "))
	}
	if len(op.RemoveIgnore) > 0 {
		changes = append(changes, "ignore − "+strings.Join(op.RemoveIgnore, ", "))
	}
	if allowSecrets.set {
		if allowSecrets.value {
			op.Fields["allow_secrets"] = true
		} else {
			op.Unset = append(op.Unset, "allow_secrets")
		}
		changes = append(changes, fmt.Sprintf("allow_secrets = %v", allowSecrets.value))
	}
	if len(changes) == 0 {
		fs.Usage()
		return 2, errors.New("nothing to change")
	}

	c, err := newCtx(true, false, stdout, stderr)
	if err != nil {
		return 1, err
	}
	return queueOps(c, pos, *noSync, func(raw map[string]any) (*Op, string) {
		op.Source = rawSource(raw)
		return op, fmt.Sprintf("updated '%s': %s", op.Source, strings.Join(changes, "; "))
	})
}

// cmdDescribe is kept as a shortcut for `set <entry> -d <text>`.
func cmdDescribe(args []string, stdout, stderr io.Writer) (int, error) {
	fs := newFlags("describe", "<path|source> <description>", stderr)
	pos, err := parseArgs(fs, args)
	if err != nil {
		return 2, err
	}
	if len(pos) != 2 {
		fs.Usage()
		return 2, errors.New("expected a path or source and a description")
	}
	return cmdSet([]string{pos[0], "--description", pos[1]}, stdout, stderr)
}

// cmdExclude and cmdInclude edit this machine's config: excluded entries stay in the shared
// manifest but are not managed here (their local files are left alone).
func cmdExclude(args []string, stdout, stderr io.Writer) (int, error) {
	return editExcludes("exclude", args, stdout, stderr)
}

func cmdInclude(args []string, stdout, stderr io.Writer) (int, error) {
	return editExcludes("include", args, stdout, stderr)
}

func editExcludes(verb string, args []string, stdout, stderr io.Writer) (int, error) {
	fs := newFlags(verb, "[--no-sync] <path|source>...", stderr)
	noSync := fs.Bool("no-sync", false, "don't sync afterwards")
	needles, err := parseArgs(fs, args)
	if err != nil {
		return 2, err
	}
	c, err := newCtx(true, false, stdout, stderr)
	if err != nil {
		return 1, err
	}
	if len(needles) == 0 {
		if len(c.Config.Exclude) == 0 {
			fmt.Fprintln(stdout, "nothing is excluded on this machine")
		} else {
			fmt.Fprintln(stdout, "excluded on this machine: "+strings.Join(c.Config.Exclude, ", "))
		}
		return 0, nil
	}
	code := 0
	err = withState(c, func(st *State) error {
		m, err := manifestWithPending(c, st)
		if err != nil {
			return err
		}
		for _, n := range needles {
			src := strings.Trim(n, "/")
			if raw := findEntry(c, m, n); raw != nil {
				src = rawSource(raw)
			} else if verb == "exclude" || !containsStr(c.Config.Exclude, src) {
				return fmt.Errorf("%s: not a managed entry (see `dotsync list`)", n)
			}
			switch {
			case verb == "exclude" && containsStr(c.Config.Exclude, src):
				c.say("'%s' is already excluded on this machine", src)
			case verb == "exclude":
				c.Config.Exclude = append(c.Config.Exclude, src)
				c.say("'%s' is no longer managed on this machine; its local files are left as they are", src)
			case !containsStr(c.Config.Exclude, src):
				c.say("'%s' is not excluded on this machine", src)
			default:
				var kept []string
				for _, e := range c.Config.Exclude {
					if e != src {
						kept = append(kept, e)
					}
				}
				c.Config.Exclude = kept
				c.say("'%s' is managed on this machine again", src)
			}
		}
		if c.Config.Exclude == nil {
			c.Config.Exclude = []string{}
		}
		if err := writeJSON(c.Paths.Config, c.Config, 0o600); err != nil {
			return err
		}
		if *noSync {
			return nil
		}
		code, err = syncAndReport(c)
		return err
	})
	return code, err
}
