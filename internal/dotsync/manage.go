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

// setFlags are the changes `dotsync set` can make to an entry.
type setFlags struct {
	desc, mode, write             string
	addIgnore, removeIgnore, oses stringList
	clearIgnore, anyOS, noMode    bool
	allowSecrets                  boolFlag
}

func cmdSet(args []string, stdout, stderr io.Writer) (int, error) {
	fs := newFlags("set", "<path|source> [changes]", stderr)
	var f setFlags
	fs.StringVar(&f.desc, "d", "", "new description")
	fs.StringVar(&f.desc, "description", "", "same as -d")
	fs.Var(&f.addIgnore, "ignore", "add an ignore pattern (repeatable)")
	fs.Var(&f.removeIgnore, "unignore", "remove an ignore pattern (repeatable)")
	fs.BoolVar(&f.clearIgnore, "clear-ignore", false, "remove every ignore pattern")
	fs.Var(&f.oses, "os", "only manage on this OS: darwin or linux (repeatable; replaces the current list)")
	fs.BoolVar(&f.anyOS, "any-os", false, "manage on every OS (removes the os restriction)")
	fs.StringVar(&f.mode, "mode", "", `force permissions on every machine, e.g. "0600"`)
	fs.BoolVar(&f.noMode, "no-mode", false, "stop forcing permissions")
	fs.StringVar(&f.write, "write", "", "how files are replaced: atomic or inplace")
	fs.Var(&f.allowSecrets, "allow-secrets", "allow content that looks like credentials (use --allow-secrets=false to undo)")
	noSync := fs.Bool("no-sync", false, "only queue the change")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return 2, err
	}
	if len(pos) != 1 {
		return usageError(fs, "expected exactly one path or source")
	}
	given := map[string]bool{}
	fs.Visit(func(fl *flag.Flag) { given[fl.Name] = true })
	op, changes, err := f.op(given)
	if err != nil {
		return 2, err
	}
	if len(changes) == 0 {
		return usageError(fs, "nothing to change")
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

// op turns the given flags into an update op, and describes each change for the user.
func (f *setFlags) op(given map[string]bool) (*Op, []string, error) {
	op := &Op{ID: newOpID(), Op: "update", Fields: map[string]any{}, Queued: nowISO()}
	var changes []string
	field := func(name string, value any, change string) {
		op.Fields[name] = value
		changes = append(changes, change)
	}
	unset := func(name, change string) {
		op.Unset = append(op.Unset, name)
		changes = append(changes, change)
	}
	switch {
	case given["mode"] && f.noMode:
		return nil, nil, errors.New("--mode and --no-mode are mutually exclusive")
	case len(f.oses) > 0 && f.anyOS:
		return nil, nil, errors.New("--os and --any-os are mutually exclusive")
	case f.clearIgnore && len(f.removeIgnore) > 0:
		return nil, nil, errors.New("--clear-ignore and --unignore are mutually exclusive")
	}
	if err := validateOS(f.oses); err != nil {
		return nil, nil, err
	}
	if given["d"] || given["description"] {
		field("description", f.desc, "description")
	}
	if given["mode"] {
		field("mode", f.mode, "mode = "+f.mode)
	}
	if given["write"] {
		field("write", f.write, "write = "+f.write)
	}
	if f.noMode {
		unset("mode", "no forced mode")
	}
	if len(f.oses) > 0 {
		list := make([]any, 0, len(f.oses))
		for _, o := range f.oses {
			list = append(list, o)
		}
		field("os", list, "os = "+strings.Join(f.oses, ", "))
	}
	if f.anyOS {
		unset("os", "any OS")
	}
	if f.clearIgnore {
		unset("ignore", "no ignore patterns")
	}
	for _, p := range f.addIgnore {
		if p = strings.Trim(p, "/"); p != "" {
			op.AddIgnore = append(op.AddIgnore, p)
		}
	}
	op.RemoveIgnore = append(op.RemoveIgnore, f.removeIgnore...)
	if len(op.AddIgnore) > 0 {
		changes = append(changes, "ignore + "+strings.Join(op.AddIgnore, ", "))
	}
	if len(op.RemoveIgnore) > 0 {
		changes = append(changes, "ignore − "+strings.Join(op.RemoveIgnore, ", "))
	}
	if f.allowSecrets.set {
		change := fmt.Sprintf("allow_secrets = %v", f.allowSecrets.value)
		if f.allowSecrets.value {
			field("allow_secrets", true, change)
		} else {
			unset("allow_secrets", change)
		}
	}
	return op, changes, nil
}

// cmdDescribe is kept as a shortcut for `set <entry> -d <text>`.
func cmdDescribe(args []string, stdout, stderr io.Writer) (int, error) {
	fs := newFlags("describe", "<path|source> <description>", stderr)
	pos, err := parseArgs(fs, args)
	if err != nil {
		return 2, err
	}
	if len(pos) != 2 {
		return usageError(fs, "expected a path or source and a description")
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
		code, err = syncAfter(c, *noSync)
		return err
	})
	return code, err
}
