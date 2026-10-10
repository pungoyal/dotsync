package dotsync

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// stowLink is one symlink in $HOME that GNU Stow created for a package.
type stowLink struct {
	Package string
	Target  string // the symlink, absolute
	Source  string // what it points at inside the stow directory, absolute
	Dir     bool   // a folded directory: one link for a whole tree
}

// stowNames are the names a package path component can have in $HOME: itself, and with
// stow --dotfiles "dot-foo" becomes ".foo".
func stowNames(name string) []string {
	if strings.HasPrefix(name, "dot-") && len(name) > len("dot-") {
		return []string{name, "." + name[len("dot-"):]}
	}
	return []string{name}
}

func stowPackages(stowDir string, want []string) ([]string, error) {
	if len(want) > 0 {
		for _, p := range want {
			if p == "" || p != filepath.Base(p) || p == "." || p == ".." {
				return nil, fmt.Errorf("%s: no such package in %s", p, tilde(stowDir))
			}
			fi, err := os.Stat(filepath.Join(stowDir, p))
			if err != nil || !fi.IsDir() || strings.ContainsRune(p, filepath.Separator) {
				return nil, fmt.Errorf("%s: no such package in %s", p, tilde(stowDir))
			}
		}
		return want, nil
	}
	des, err := os.ReadDir(stowDir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, de := range des {
		if strings.HasPrefix(de.Name(), ".") {
			continue
		}
		if fi, err := os.Stat(filepath.Join(stowDir, de.Name())); err == nil && fi.IsDir() {
			out = append(out, de.Name())
		}
	}
	return out, nil
}

func samePath(a, b string) bool {
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		return false
	}
	rb, err := filepath.EvalSymlinks(b)
	return err == nil && ra == rb
}

// discoverStow walks the package trees under stowDir and returns the symlinks under home that
// point at them, sorted by target. It is driven by the packages, never by scanning home.
func discoverStow(stowDir, home string, packages []string) ([]stowLink, error) {
	pkgs, err := stowPackages(stowDir, packages)
	if err != nil {
		return nil, err
	}
	var links []stowLink
	var walk func(pkg, pkgDir, targetDir string) error
	walk = func(pkg, pkgDir, targetDir string) error {
		des, err := os.ReadDir(pkgDir)
		if err != nil {
			return err
		}
		for _, de := range des {
			src := filepath.Join(pkgDir, de.Name())
			sfi, err := os.Stat(src)
			if err != nil {
				continue // package entry that cannot be stat'ed (e.g. dangling link)
			}
			found := false
			for _, name := range stowNames(de.Name()) {
				if found {
					break
				}
				target := filepath.Join(targetDir, name)
				tfi, err := os.Lstat(target)
				if err != nil {
					continue // target does not exist, try next spelling
				}
				switch {
				case tfi.Mode()&os.ModeSymlink != 0:
					if samePath(target, src) {
						links = append(links, stowLink{Package: pkg, Target: target, Source: src, Dir: sfi.IsDir()})
						found = true
					}
					// symlink points elsewhere, try next spelling
				case tfi.IsDir() && sfi.IsDir():
					if err := walk(pkg, src, target); err != nil { // unfolded by Stow
						return err
					}
					found = true
				}
				// else: not a symlink, not matching directory; try next spelling
			}
		}
		return nil
	}
	for _, pkg := range pkgs {
		if err := walk(pkg, filepath.Join(stowDir, pkg), home); err != nil {
			return nil, err
		}
	}
	sort.Slice(links, func(i, j int) bool { return links[i].Target < links[j].Target })
	return links, nil
}

// importEntry is one dotsync entry the import will create or take over.
type importEntry struct {
	Package string
	Target  string
	Kind    string // "file" or "dir"
	Links   []stowLink
}

func (e importEntry) kindLabel() string {
	switch {
	case e.Kind == "file":
		return "file"
	case len(e.Links) == 1 && e.Links[0].Dir && e.Links[0].Target == e.Target:
		return "dir (folded)"
	case len(e.Links) == 1:
		return "dir (1 link, unfolded)"
	}
	return fmt.Sprintf("dir (%d links, unfolded)", len(e.Links))
}

var errNotOnlyLinks = errors.New("not only stow links")

// onlyLinksOf reports whether every file below dir (default ignores aside) is a link of pkg, and
// none of them is a secret by path.
func onlyLinksOf(dir, pkg string, byTarget map[string]stowLink) bool {
	n := 0
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// A walk error (for example an unreadable subdirectory) also means "not
			// promotable" — fail closed, so the links are imported one by one.
			return err
		}
		if p == dir {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		if ignored(filepath.ToSlash(rel), defaultIgnore) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if l, ok := byTarget[p]; !ok || l.Package != pkg {
			return errNotOnlyLinks // stops the walk at the first foreign file
		}
		if secretPathReason(p) != "" {
			// A secret by path is skipped as a file entry and keeps its link. Inside a
			// directory entry it would be materialized and sit there blocked forever.
			return errNotOnlyLinks
		}
		n++
		return nil
	})
	return err == nil && n > 0
}

// groupStowLinks turns links into entries. A folded directory is one dir entry. File links are
// promoted to the highest directory that holds nothing but links of the same package, so an
// unfolded ~/.config/nvim becomes one dir entry; otherwise each link is a file entry. home,
// the noPromote directories and their ancestors never become entries.
func groupStowLinks(links []stowLink, home string, noPromote []string) []importEntry {
	byTarget := map[string]stowLink{}
	for _, l := range links {
		byTarget[l.Target] = l
	}
	cache := map[string]bool{}
	canPromote := func(dir, pkg string) bool {
		key := pkg + "\x00" + dir
		if v, ok := cache[key]; ok {
			return v
		}
		ok := true
		for _, np := range noPromote {
			if isWithin(np, dir) { // dir is np or one of its ancestors
				ok = false
			}
		}
		ok = ok && onlyLinksOf(dir, pkg, byTarget)
		cache[key] = ok
		return ok
	}

	roots := map[string]*importEntry{}
	var order []*importEntry
	for _, l := range links {
		if l.Dir {
			continue
		}
		best := ""
		for d := filepath.Dir(l.Target); d != home && isWithin(d, home); d = filepath.Dir(d) {
			if !canPromote(d, l.Package) {
				break // a parent holds everything d does, so it cannot qualify either
			}
			best = d
		}
		if best == "" {
			order = append(order, &importEntry{Package: l.Package, Target: l.Target, Kind: "file", Links: []stowLink{l}})
			continue
		}
		if roots[best] == nil {
			roots[best] = &importEntry{Package: l.Package, Target: best, Kind: "dir"}
			order = append(order, roots[best])
		}
		roots[best].Links = append(roots[best].Links, l)
	}
	for _, l := range links {
		if !l.Dir {
			continue
		}
		var parent *importEntry
		var parentDir string
		// Promotion climbs to the topmost qualifying ancestor, so roots never nest.
		// Choose the longest match (nearest promoted ancestor) so result doesn't depend on map order.
		for dir, e := range roots {
			if isWithin(l.Target, dir) && len(dir) > len(parentDir) {
				parentDir = dir
				parent = e
			}
		}
		if parent != nil {
			parent.Links = append(parent.Links, l) // a folded subdirectory of a promoted directory
			continue
		}
		order = append(order, &importEntry{Package: l.Package, Target: l.Target, Kind: "dir", Links: []stowLink{l}})
	}

	out := make([]importEntry, 0, len(order))
	for _, e := range order {
		sort.Slice(e.Links, func(i, j int) bool { return e.Links[i].Target < e.Links[j].Target })
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Target < out[j].Target })
	return out
}
