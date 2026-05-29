// Package credential resolves credentials for token-based providers and is the
// only place in ddc where a raw secret value lives. Secrets are wrapped in the
// Secret type, which refuses to expose its value through fmt, String, or JSON —
// the value can only be obtained via the explicit, greppable Reveal method.
//
// Resolution order favors the user's existing local sessions over anything ddc
// stores itself: environment variables, then a provider-specific fallback (e.g.
// the gh CLI's config), then the OS keychain populated by `ddc auth login`.
package credential

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// ErrNotConfigured marks the absence of any credential/config for a provider, as
// opposed to a credential that exists but is broken (expired, unreachable). The
// CLI uses it to show "not configured" rather than surfacing it as an error.
var ErrNotConfigured = errors.New("not configured")

// Secret wraps a sensitive string so it cannot be accidentally logged. The
// String, GoString, and MarshalJSON methods all return a redaction marker, so
// printing a Secret (or any struct containing one) never leaks the value.
type Secret struct {
	v string
}

// NewSecret wraps a raw credential value.
func NewSecret(v string) Secret { return Secret{v: v} }

// Reveal returns the raw value. This is the only accessor that exposes the
// secret; keep its call sites few and auditable.
func (s Secret) Reveal() string { return s.v }

// IsZero reports whether the secret is empty.
func (s Secret) IsZero() bool { return s.v == "" }

// String implements fmt.Stringer with a redaction marker.
func (s Secret) String() string { return "[REDACTED]" }

// GoString implements fmt.GoStringer so %#v cannot dump the unexported field.
func (s Secret) GoString() string { return "credential.Secret{[REDACTED]}" }

// MarshalJSON ensures secrets serialize to a redaction marker.
func (s Secret) MarshalJSON() ([]byte, error) { return []byte(`"[REDACTED]"`), nil }

// Source identifies where a resolved credential came from.
type Source string

const (
	SourceEnv        Source = "env"
	SourceGHConfig   Source = "gh-config"
	SourceKeychain   Source = "keychain"
	SourceKubeconfig Source = "kubeconfig"
)

// TokenSpec describes how to resolve a token for one provider/environment.
type TokenSpec struct {
	Provider string
	Env      string
	EnvVars  []string // checked in order, e.g. {"GH_TOKEN", "GITHUB_TOKEN"}
	// Fallback resolves from a tool's own config (e.g. gh hosts.yml). It returns
	// (secret, source, true) on success.
	Fallback func() (Secret, Source, bool)
}

// TokenResult is a resolved credential and where it was found.
type TokenResult struct {
	Secret Secret
	Source Source
}

// Resolve returns the first credential found via env vars, the fallback, then
// the keychain. It returns an actionable error if none is available.
func (s TokenSpec) Resolve() (TokenResult, error) {
	for _, name := range s.EnvVars {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return TokenResult{Secret: NewSecret(v), Source: SourceEnv}, nil
		}
	}
	if s.Fallback != nil {
		if sec, src, ok := s.Fallback(); ok && !sec.IsZero() {
			return TokenResult{Secret: sec, Source: src}, nil
		}
	}
	if sec, err := KeychainGet(s.Provider, s.Env); err == nil && !sec.IsZero() {
		return TokenResult{Secret: sec, Source: SourceKeychain}, nil
	}

	hint := fmt.Sprintf("run `ddc auth login %s`", s.Provider)
	if len(s.EnvVars) > 0 {
		hint = fmt.Sprintf("set %s, or %s", strings.Join(s.EnvVars, "/"), hint)
	}
	return TokenResult{}, fmt.Errorf("no credential found for %q: %s: %w", s.Provider, hint, ErrNotConfigured)
}
