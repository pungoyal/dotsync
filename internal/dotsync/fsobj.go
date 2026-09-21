package dotsync

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
)

const maxFileSize = 10 << 20

// Obj is a file or symlink as dotsync sees it. Sig identifies type + executable bit + content;
// an absent object has signature "".
type Obj struct {
	Kind string // "file", "link", "dir", "other"
	Data []byte
	Exec bool
	Sig  string
}

func sigOf(o *Obj) string {
	if o == nil {
		return ""
	}
	return o.Sig
}

func hashBytes(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// UnstableError marks a file that changed while dotsync looked at it; it is retried next sync.
type UnstableError struct{ Path string }

func (e *UnstableError) Error() string {
	return fmt.Sprintf("%s changed during sync; will retry", tilde(e.Path))
}

func notExist(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}

type statID struct {
	size, mtime int64
	ino         uint64
}

func statKey(fi os.FileInfo) statID {
	id := statID{size: fi.Size(), mtime: fi.ModTime().UnixNano()}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		id.ino = uint64(st.Ino) //nolint:unconvert // Ino is uint32 on some platforms
	}
	return id
}

// readObj reads the object at path without following a final symlink. nil if absent.
func readObj(path string) (*Obj, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		if notExist(err) {
			return nil, nil
		}
		return nil, err
	}
	switch {
	case fi.Mode()&os.ModeSymlink != 0:
		t, err := os.Readlink(path)
		if err != nil {
			return nil, err
		}
		return &Obj{Kind: "link", Data: []byte(t), Sig: "l:" + hashBytes([]byte(t))}, nil
	case fi.IsDir():
		return &Obj{Kind: "dir", Sig: "d"}, nil
	case !fi.Mode().IsRegular():
		return &Obj{Kind: "other", Sig: "?"}, nil
	}
	if fi.Size() > maxFileSize {
		return nil, fmt.Errorf("%s is larger than %d MiB; not syncing it", tilde(path), maxFileSize>>20)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	fi2, err := os.Lstat(path)
	if err != nil || statKey(fi) != statKey(fi2) || int64(len(data)) != fi.Size() {
		return nil, &UnstableError{path}
	}
	x := fi.Mode()&0o100 != 0
	prefix := "f-:"
	if x {
		prefix = "fx:"
	}
	return &Obj{Kind: "file", Data: data, Exec: x, Sig: prefix + hashBytes(data)}, nil
}

func fsyncDir(dir string) {
	if f, err := os.Open(dir); err == nil {
		_ = f.Sync()
		f.Close()
	}
}

func randSuffix() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func atomicWrite(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".dotsync-tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	fail := func(err error) error {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if _, err := f.Write(data); err != nil {
		return fail(err)
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	if err := f.Chmod(perm); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	fsyncDir(dir)
	return nil
}

// writeLocal materializes obj at path on this machine. mode < 0 keeps the existing permissions
// and only carries the executable bit over. strategy "inplace" rewrites the existing inode for
// applications that watch or bind-mount it; "atomic" writes a temp file and renames it.
func writeLocal(path string, o *Obj, mode int, strategy string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	existing, err := os.Lstat(path)
	if err != nil && !notExist(err) {
		return err
	}
	if err != nil {
		existing = nil
	}
	if existing != nil && existing.IsDir() {
		return fmt.Errorf("%s is a directory; expected a file", tilde(path))
	}
	if o.Kind == "link" {
		tmp := filepath.Join(dir, fmt.Sprintf(".dotsync-tmp-%d-%s", os.Getpid(), randSuffix()))
		if err := os.Symlink(string(o.Data), tmp); err != nil {
			return err
		}
		if err := os.Rename(tmp, path); err != nil {
			os.Remove(tmp)
			return err
		}
		fsyncDir(dir)
		return nil
	}
	var perm os.FileMode
	if mode >= 0 {
		perm = os.FileMode(mode)
	} else {
		base := os.FileMode(0o644)
		if existing != nil && existing.Mode().IsRegular() {
			base = existing.Mode().Perm()
		}
		if o.Exec {
			perm = base | ((base >> 2) & 0o111)
		} else {
			perm = base &^ 0o111
		}
	}
	if strategy == "inplace" && existing != nil && existing.Mode().IsRegular() {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
		if err != nil {
			return err
		}
		if _, err := f.Write(o.Data); err != nil {
			f.Close()
			return err
		}
		if err := f.Sync(); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		return os.Chmod(path, perm)
	}
	return atomicWrite(path, o.Data, perm)
}

// writeRepo stores obj in the repository working tree; git records content, symlinks and the exec bit.
func writeRepo(path string, o *Obj) error {
	if err := removePath(path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if o.Kind == "link" {
		return os.Symlink(string(o.Data), path)
	}
	perm := os.FileMode(0o644)
	if o.Exec {
		perm = 0o755
	}
	if err := os.WriteFile(path, o.Data, perm); err != nil {
		return err
	}
	return os.Chmod(path, perm)
}

func removePath(path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		if notExist(err) {
			return nil
		}
		return err
	}
	if fi.IsDir() {
		return os.RemoveAll(path)
	}
	return os.Remove(path)
}

// pruneEmptyDirs removes now-empty directories from start up to (not including) stop.
func pruneEmptyDirs(start, stop string) {
	for isWithin(start, stop) && filepath.Clean(start) != filepath.Clean(stop) {
		if os.Remove(start) != nil {
			return
		}
		start = filepath.Dir(start)
	}
}

func copyToBackup(src, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return err
	}
	fi, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(t, dest)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dest, data, fi.Mode().Perm()); err != nil {
		return err
	}
	return os.Chtimes(dest, fi.ModTime(), fi.ModTime())
}

// globMatch is fnmatch-style: '*' also matches '/', '?' one character, [...] a class.
func globMatch(pattern, s string) bool {
	return globRegexp(pattern).MatchString(s)
}

var (
	globMu    sync.Mutex
	globCache = map[string]*regexp.Regexp{}
)

func globRegexp(pattern string) *regexp.Regexp {
	globMu.Lock()
	defer globMu.Unlock()
	if re, ok := globCache[pattern]; ok {
		return re
	}
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch c {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		case '[':
			j := strings.IndexByte(pattern[i+1:], ']')
			if j < 0 {
				b.WriteString(`\[`)
				continue
			}
			class := pattern[i+1 : i+1+j]
			if strings.HasPrefix(class, "!") {
				class = "^" + class[1:]
			}
			b.WriteString("[" + strings.ReplaceAll(class, `\`, `\\`) + "]")
			i += j + 1
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		re = regexp.MustCompile("^" + regexp.QuoteMeta(pattern) + "$")
	}
	globCache[pattern] = re
	return re
}

// ignored reports whether rel, or any directory above it, matches one of the patterns.
// Each pattern is tried against the component name and the path so far.
func ignored(rel string, patterns []string) bool {
	parts := strings.Split(rel, "/")
	for i, name := range parts {
		prefix := strings.Join(parts[:i+1], "/")
		for _, p := range patterns {
			if globMatch(p, name) || globMatch(p, prefix) {
				return true
			}
		}
	}
	return false
}

// checkAncestors makes sure every existing directory between root and root/rel is a real
// directory. Following a symlinked (or replaced) parent could read or write outside the
// managed tree, so such items are refused instead.
func checkAncestors(root, rel string) error {
	parts := strings.Split(rel, "/")
	p := root
	for _, part := range parts[:len(parts)-1] {
		p = filepath.Join(p, part)
		fi, err := os.Lstat(p)
		if err != nil {
			if notExist(err) {
				return nil
			}
			return err
		}
		if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is not a plain directory (symlink or file); not syncing below it", tilde(p))
		}
	}
	return nil
}

// walkTree lists files and symlinks under root (relative, slash-separated), skipping ignored
// names. Empty directories are not tracked. A symlinked directory is listed as a symlink.
func walkTree(root string, patterns []string, skipDirs []string) ([]string, error) {
	fi, err := os.Lstat(root)
	if err != nil || !fi.IsDir() {
		if err != nil && !notExist(err) {
			return nil, err
		}
		return nil, nil
	}
	var out []string
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if ignored(rel, patterns) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			for _, s := range skipDirs {
				if isWithin(p, s) {
					return filepath.SkipDir
				}
			}
			return nil
		}
		out = append(out, rel)
		return nil
	})
	sort.Strings(out)
	return out, err
}

func splitLines(data []byte) [][]byte { return bytes.Split(data, []byte("\n")) }
