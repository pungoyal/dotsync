package dotsync

import (
	"strings"
	"testing"
)

func TestManPage(t *testing.T) {
	page := ManPage("1.2.3", "2026-09-21")
	for _, g := range commandGroups {
		for _, cmd := range g.commands {
			if !strings.Contains(page, ".B dotsync "+roffEscape(cmd.name)) {
				t.Errorf("%s is missing", cmd.name)
			}
		}
	}
	for _, want := range []string{`.TH DOTSYNC 1 "2026\-09\-21" "dotsync 1.2.3"`, `.B \-\-keep\-existing`, `.BI \-n " int"`, ".SH EXIT STATUS"} {
		if !strings.Contains(page, want) {
			t.Errorf("missing %q", want)
		}
	}
	for i, line := range strings.Split(page, "\n") {
		if strings.HasPrefix(line, "'") || strings.ContainsFunc(line, func(r rune) bool { return r > 0x7f || r == '\t' }) {
			t.Errorf("line %d isn't safe roff: %q", i+1, line)
		}
	}
}
