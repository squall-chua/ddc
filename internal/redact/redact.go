// Package redact removes secret-looking values from any text before it reaches
// the agent. It is a defense-in-depth layer: ddc also structurally blocks reads
// that are known to surface secrets (e.g. Kubernetes Secret objects). Redaction
// is intentionally non-bypassable — there is no "show me the raw value" flag.
package redact

// Marker replaces any value identified as sensitive.
const Marker = "[REDACTED]"

// Scrub returns s with secret-looking values replaced by Marker.
func Scrub(s string) string {
	for _, r := range rules {
		s = r.re.ReplaceAllString(s, r.repl)
	}
	return s
}
