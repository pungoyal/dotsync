package dotsync

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	manifestName = "manifest.json"
	filesDir     = "files"
)

// Never synced from inside a managed directory: editor droppings, VCS metadata, our temp files.
var defaultIgnore = []string{".DS_Store", "._*", "*.swp", "*.swo", "*~", ".#*", ".git", "__pycache__", "*.pyc", ".dotsync-tmp-*"}

var entryKeyOrder = []string{"source", "target", "description", "type", "os", "mode", "ignore", "write", "allow_secrets"}

// Manifest keeps entries as generic maps so fields added by newer versions survive a round trip.
type Manifest struct {
	Top     map[string]any
	Entries []map[string]any
}

func loadManifest(repo string) (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(repo, manifestName))
	if err != nil {
		if notExist(err) {
			return nil, fmt.Errorf("%s is missing from the repository", manifestName)
		}
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var raw any
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("%s in the repository is not valid JSON (%w); nothing was changed. "+
			"Fix it in any clone of your dotfiles repository and push; the next sync picks it up", manifestName, err)
	}
	m := &Manifest{Top: map[string]any{}}
	var list []any
	switch v := raw.(type) {
	case []any:
		m.Top["version"] = json.Number("1")
		list = v
	case map[string]any:
		l, ok := v["entries"].([]any)
		if !ok {
			return nil, fmt.Errorf("%s must contain an 'entries' list; nothing was changed", manifestName)
		}
		list = l
		for k, val := range v {
			if k != "entries" {
				m.Top[k] = val
			}
		}
	default:
		return nil, fmt.Errorf("%s must be an object with an 'entries' list; nothing was changed", manifestName)
	}
	for _, e := range list {
		obj, ok := e.(map[string]any)
		if !ok {
			obj = map[string]any{"_invalid": e}
		}
		m.Entries = append(m.Entries, obj)
	}
	return m, nil
}

func (m *Manifest) canonical() string {
	b, _ := json.Marshal(m.Entries) // maps marshal with sorted keys
	return string(b)
}

func marshalValue(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return bytes.TrimRight(buf.Bytes(), "\n")
}

func orderedObject(m map[string]any, order []string) []byte {
	var keys []string
	seen := map[string]bool{}
	for _, k := range order {
		if _, ok := m[k]; ok {
			keys = append(keys, k)
			seen[k] = true
		}
	}
	var rest []string
	for k := range m {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	keys = append(keys, rest...)
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(marshalValue(k))
		b.WriteByte(':')
		b.Write(marshalValue(m[k]))
	}
	b.WriteByte('}')
	return b.Bytes()
}

// saveManifest writes entries sorted by source with a stable key order, so that every machine
// produces byte-identical manifests for the same content.
func saveManifest(repo string, m *Manifest) error {
	entries := append([]map[string]any(nil), m.Entries...)
	sort.SliceStable(entries, func(i, j int) bool { return str(entries[i]["source"]) < str(entries[j]["source"]) })
	var list bytes.Buffer
	list.WriteByte('[')
	for i, e := range entries {
		if i > 0 {
			list.WriteByte(',')
		}
		list.Write(orderedObject(e, entryKeyOrder))
	}
	list.WriteByte(']')
	top := map[string]any{}
	for k, v := range m.Top {
		top[k] = v
	}
	top["entries"] = json.RawMessage(list.Bytes())
	var out bytes.Buffer
	if err := json.Indent(&out, orderedObject(top, []string{"version", "entries"}), "", "  "); err != nil {
		return err
	}
	out.WriteByte('\n')
	return os.WriteFile(filepath.Join(repo, manifestName), out.Bytes(), 0o644)
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func normalizeSource(s string) (string, error) {
	s = strings.ReplaceAll(strings.TrimSpace(s), `\`, "/")
	if s == "" {
		return "", errors.New("entry has no 'source'")
	}
	if strings.HasPrefix(s, "/") {
		return "", fmt.Errorf("source '%s' must be relative to the repository's files/ directory", s)
	}
	var parts []string
	for _, p := range strings.Split(s, "/") {
		if p == "" || p == "." {
			continue
		}
		if p == ".." {
			return "", fmt.Errorf("source '%s' may not contain '..'", s)
		}
		if strings.TrimSpace(p) != p {
			return "", fmt.Errorf("source '%s' has a path component with leading or trailing spaces", s)
		}
		if strings.EqualFold(p, ".git") {
			return "", fmt.Errorf("source '%s' may not contain a .git component", s)
		}
		parts = append(parts, p)
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("source '%s' is not a valid path", s)
	}
	return strings.Join(parts, "/"), nil
}

func rawSource(raw map[string]any) string {
	s, err := normalizeSource(str(raw["source"]))
	if err != nil {
		return ""
	}
	return s
}

var varRx = regexp.MustCompile(`^\$(?:\{([A-Za-z_][A-Za-z0-9_]*)\}|([A-Za-z_][A-Za-z0-9_]*))(/.*)?$`)

// expandTarget turns a portable target (~/..., $XDG_CONFIG_HOME/...) into a path on this machine.
func expandTarget(spec string, p *Paths) (string, error) {
	var path string
	if spec == "~" || strings.HasPrefix(spec, "~/") {
		path = homeDir() + spec[1:]
	} else {
		m := varRx.FindStringSubmatch(spec)
		if m == nil {
			return "", fmt.Errorf("target '%s' must start with ~/ or $VAR/ (absolute paths are machine-specific)", spec)
		}
		name := m[1] + m[2]
		base, ok := envDir(name)
		if !ok {
			v := os.Getenv(name)
			if v == "" || !filepath.IsAbs(v) {
				return "", fmt.Errorf("target '%s' uses $%s, which is not set on this machine", spec, name)
			}
			base = v
		}
		path = base + m[3]
	}
	path = filepath.Clean(path)
	if !isWithin(path, homeDir()) || path == homeDir() {
		return "", fmt.Errorf("target '%s' resolves to %s, which is not inside the home directory", spec, path)
	}
	for _, d := range p.OwnDirs() {
		if isWithin(path, d) {
			return "", fmt.Errorf("target '%s' is inside dotsync's own directory %s", spec, tilde(d))
		}
	}
	return path, nil
}

// Entry is a validated manifest entry.
type Entry struct {
	Source, TargetSpec, Description string
	Kind                            string // kindFile or kindDir; "" until resolved
	OS                              []string
	Mode                            int // -1: keep local permissions
	Ignore                          []string
	Write                           string
	AllowSecrets                    bool

	Target   string // absolute path on this machine
	RepoPath string // path inside the cache clone
}

func parseEntry(raw map[string]any) (*Entry, error) {
	if _, bad := raw["_invalid"]; bad {
		return nil, errors.New("manifest entry is not a JSON object")
	}
	src, err := normalizeSource(str(raw["source"]))
	if err != nil {
		return nil, err
	}
	e := &Entry{Source: src, Mode: -1, Write: "atomic"}
	target := strings.TrimSpace(str(raw["target"]))
	if target == "" {
		return nil, fmt.Errorf("entry '%s' has no 'target'", src)
	}
	if len(target) > 1 {
		target = strings.TrimRight(target, "/")
	}
	e.TargetSpec = target
	e.Description = str(raw["description"])
	if v, ok := raw["type"]; ok {
		e.Kind = str(v)
		if e.Kind != kindFile && e.Kind != kindDir {
			return nil, fmt.Errorf("entry '%s': type must be \"file\" or \"dir\"", src)
		}
	}
	switch v := raw["os"].(type) {
	case nil:
	case string:
		e.OS = []string{v}
	case []any:
		for _, o := range v {
			s, ok := o.(string)
			if !ok {
				return nil, fmt.Errorf("entry '%s': os must be a list like [\"darwin\", \"linux\"]", src)
			}
			e.OS = append(e.OS, s)
		}
	default:
		return nil, fmt.Errorf("entry '%s': os must be a list like [\"darwin\", \"linux\"]", src)
	}
	switch v := raw["mode"].(type) {
	case nil:
	case string:
		n, err := strconv.ParseInt(v, 8, 32)
		if err != nil || n < 0 || n > 0o7777 {
			return nil, fmt.Errorf("entry '%s': mode must be octal like \"0600\"", src)
		}
		e.Mode = int(n)
	default:
		return nil, fmt.Errorf("entry '%s': mode must be a string like \"0600\"", src)
	}
	e.Ignore = append([]string(nil), defaultIgnore...)
	switch v := raw["ignore"].(type) {
	case nil:
	case []any:
		for _, p := range v {
			s, ok := p.(string)
			if !ok {
				return nil, fmt.Errorf("entry '%s': ignore must be a list of glob patterns", src)
			}
			if s = strings.Trim(s, "/"); s != "" {
				e.Ignore = append(e.Ignore, s)
			}
		}
	default:
		return nil, fmt.Errorf("entry '%s': ignore must be a list of glob patterns", src)
	}
	if v, ok := raw["write"]; ok {
		e.Write = str(v)
		if e.Write != "atomic" && e.Write != "inplace" {
			return nil, fmt.Errorf("entry '%s': write must be \"atomic\" or \"inplace\"", src)
		}
	}
	if v, ok := raw["allow_secrets"].(bool); ok {
		e.AllowSecrets = v
	}
	return e, nil
}

// Op is a manifest change made on this machine. It is kept in local state and replayed on top of
// the latest remote manifest at every sync until it has been pushed, so it survives offline
// periods and concurrent edits without ever needing a git merge.
type Op struct {
	ID     string         `json:"id"`
	Op     string         `json:"op"` // add, remove, update
	Entry  map[string]any `json:"entry,omitempty"`
	Source string         `json:"source,omitempty"`
	Fields map[string]any `json:"fields,omitempty"` // update: fields to set
	Unset  []string       `json:"unset,omitempty"`  // update: fields to delete
	// update: ignore patterns to add/remove. Expressed as changes rather than a whole list so
	// that edits made concurrently on different machines combine instead of overwriting.
	AddIgnore    []string `json:"add_ignore,omitempty"`
	RemoveIgnore []string `json:"remove_ignore,omitempty"`
	Queued       string   `json:"queued"`
}

func (o *Op) subject() string {
	if o.Op == "add" {
		return str(o.Entry["source"])
	}
	return o.Source
}

// remove drops the entry with the given source, reporting whether there was one.
func (m *Manifest) remove(source string) bool {
	kept := m.Entries[:0:0]
	for _, raw := range m.Entries {
		if rawSource(raw) != source {
			kept = append(kept, raw)
		}
	}
	removed := len(kept) != len(m.Entries)
	m.Entries = kept
	return removed
}

// applyUpdate returns a copy of raw with an update op's changes applied.
func applyUpdate(raw map[string]any, op *Op) map[string]any {
	out := map[string]any{}
	for k, v := range raw {
		out[k] = v
	}
	for k, v := range op.Fields {
		out[k] = v
	}
	for _, k := range op.Unset {
		delete(out, k)
	}
	if len(op.AddIgnore) > 0 || len(op.RemoveIgnore) > 0 {
		var list []any
		for _, p := range stringsOf(out["ignore"]) {
			if !containsStr(op.RemoveIgnore, p) {
				list = append(list, p)
			}
		}
		for _, p := range op.AddIgnore {
			if !containsStr(stringsOf(list), p) {
				list = append(list, p)
			}
		}
		if len(list) == 0 {
			delete(out, "ignore")
		} else {
			out["ignore"] = list
		}
	}
	return out
}

// stringsOf converts a JSON list ([]any or []string) to []string, skipping non-strings.
func stringsOf(v any) []string {
	var out []string
	switch l := v.(type) {
	case []string:
		out = append(out, l...)
	case []any:
		for _, x := range l {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// transientError is an I/O failure while replaying an op; the op is kept and retried.
type transientError struct{ err error }

func (e *transientError) Error() string { return e.err.Error() }
func (e *transientError) Unwrap() error { return e.err }

func applyOp(c *Ctx, m *Manifest, op *Op) error {
	switch op.Op {
	case "add":
		src, err := normalizeSource(str(op.Entry["source"]))
		if err != nil {
			return err
		}
		newTarget, err := expandTarget(str(op.Entry["target"]), c.Paths)
		if err != nil {
			return err
		}
		for _, raw := range m.Entries {
			other := rawSource(raw)
			if other == "" {
				continue
			}
			if other == src {
				if str(raw["target"]) == str(op.Entry["target"]) {
					return nil // already managed, perhaps added from another machine too
				}
				return fmt.Errorf("source '%s' is already used for %s", src, str(raw["target"]))
			}
			if overlaps(strings.ToLower(other), strings.ToLower(src)) {
				return fmt.Errorf("source '%s' overlaps existing source '%s'", src, other)
			}
			if t, err := expandTarget(str(raw["target"]), c.Paths); err == nil && overlaps(t, newTarget) {
				return fmt.Errorf("%s overlaps %s, which is managed as '%s'", tilde(newTarget), str(raw["target"]), other)
			}
		}
		entry := map[string]any{}
		for k, v := range op.Entry {
			entry[k] = v
		}
		m.Entries = append(m.Entries, entry)
	case "remove":
		if m.remove(op.Source) {
			if err := removePath(filepath.Join(c.Paths.Repo, filesDir, filepath.FromSlash(op.Source))); err != nil {
				return &transientError{err}
			}
		}
	case "update":
		for _, raw := range m.Entries {
			if rawSource(raw) != op.Source {
				continue
			}
			updated := applyUpdate(raw, op)
			if _, err := parseEntry(updated); err != nil {
				return err
			}
			for k := range raw {
				delete(raw, k)
			}
			for k, v := range updated {
				raw[k] = v
			}
			return nil
		}
		return fmt.Errorf("no managed entry with source '%s'", op.Source)
	default:
		return fmt.Errorf("unknown queued operation %q", op.Op)
	}
	return nil
}
