package dotsync

import (
	"bytes"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// secretPaths are lower-cased glob patterns for files that are never uploaded unless the entry
// sets allow_secrets. Patterns containing '/' match the path relative to $HOME ('*' also matches
// '/'); the others match the file name only.
var secretPaths = []string{
	".ssh/id_*", ".ssh/*_key", "*.pem", "*.key", "*.p12", "*.pfx", "*.jks", "*.keystore", "*.kdbx",
	".gnupg/private-keys-v1.d/*", ".gnupg/secring.*", ".gnupg/*.key", ".password-store/*",
	".aws/credentials", ".aws/sso/*", ".azure/*", ".config/gcloud/*", ".kube/config", ".docker/config.json",
	".netrc", ".git-credentials", ".pgpass", ".vault-token", ".config/gh/hosts.yml", ".local/share/keyrings/*",
	".env", ".env.*", "*.env", "*secret*", "*credential*", ".*history",
}

type secretRule struct {
	name string
	re   *regexp.Regexp
}

// secretContent is never uploaded unless the entry sets allow_secrets. A line containing
// allowMarker is exempt, for known false positives.
var secretContent = []secretRule{
	{"private key", regexp.MustCompile(`-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY(?: BLOCK)?-----`)},
	{"AWS access key", regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{"GitHub token", regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{22,})`)},
	{"GitLab token", regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{20,}`)},
	{"Slack token", regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9-]{10,}`)},
	{"API key", regexp.MustCompile(`\bsk-(?:ant-|proj-)?[A-Za-z0-9_-]{20,}`)},
	{"Google API key", regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}`)},
	{"npm token", regexp.MustCompile(`_authToken\s*=\s*[^\s$]{8,}`)},
	// key = value where the value is a literal of 8+ characters (not a $VAR or $(command)).
	{"password or token assignment", regexp.MustCompile(
		`(?i)(?:password|passwd|secret|token|api[_-]?key|access[_-]?key|client[_-]?secret)` +
			`["']?\s*[:=]\s*["']?[^\s"'$(){}<>]{8,}`)},
}

var allowMarker = []byte("dotsync:allow-secret")

func secretPathReason(path string) string {
	rel := strings.ToLower(homeRel(path))
	name := strings.ToLower(filepath.Base(path))
	for _, p := range secretPaths {
		subject := name
		if strings.Contains(p, "/") {
			subject = rel
		}
		if globMatch(p, subject) {
			return fmt.Sprintf("path looks like a secret (matches '%s')", p)
		}
	}
	return ""
}

func secretContentReason(data []byte) string {
	for _, line := range splitLines(data) {
		if bytes.Contains(line, allowMarker) {
			continue
		}
		for _, r := range secretContent {
			if r.re.Match(line) {
				return "content looks like a " + r.name
			}
		}
	}
	return ""
}

// secretReason explains why path/obj must not leave this machine, or returns "".
func secretReason(path string, o *Obj) string {
	if r := secretPathReason(path); r != "" {
		return r
	}
	if o != nil && o.Kind == "file" {
		return secretContentReason(o.Data)
	}
	return ""
}
