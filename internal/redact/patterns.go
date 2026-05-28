package redact

import "regexp"

// rule is a single redaction pattern and its replacement. Capture groups in
// repl (e.g. "${1}") preserve surrounding context (the "Bearer " prefix, a JSON
// key) while replacing only the sensitive value.
type rule struct {
	re   *regexp.Regexp
	repl string
}

// rules are applied in order. They are deliberately specific: ddc must not
// redact normal debugging signal (image digests, pod hashes, log lines), so we
// match known secret shapes rather than any high-entropy string. This is
// defense-in-depth layered on top of structurally blocking secret-bearing reads
// (e.g. Kubernetes Secret objects), not a standalone guarantee.
var rules = []rule{
	// PEM-encoded keys/certs (multiline). Match first so inner base64 lines are
	// not matched again by other rules.
	{regexp.MustCompile(`(?s)-----BEGIN [^-]+-----.*?-----END [^-]+-----`), Marker},

	// JSON Web Tokens.
	{regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`), Marker},

	// GitHub tokens (classic, app, refresh, user-to-server, fine-grained PAT).
	{regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}`), Marker},
	{regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}`), Marker},

	// AWS access key IDs (long-term AKIA, temporary ASIA).
	{regexp.MustCompile(`A(?:KIA|SIA)[0-9A-Z]{16}`), Marker},

	// Bearer tokens in Authorization headers.
	{regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/\-]+=*`), "${1}" + Marker},

	// Basic-auth credentials embedded in URLs: scheme://user:pass@host.
	{regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.\-]*://)[^/\s:@]+:[^/\s:@]+@`), "${1}" + Marker + "@"},

	// key=value / "key": "value" pairs whose key names a secret.
	{regexp.MustCompile(`(?i)("?(?:password|passwd|pwd|token|secret|api[_-]?key|access[_-]?key|client[_-]?secret)"?\s*[:=]\s*)("?)[^\s"',}]+`), "${1}${2}" + Marker},
}
