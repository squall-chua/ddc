package credential

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	keyring "github.com/zalando/go-keyring"
)

const rawToken = "ghp_supersecrettokenvalue1234567890"

// A Secret must never expose its value through any of the common accidental
// leak paths: fmt verbs, String(), or JSON marshaling. Only Reveal() returns it.
func TestSecretNeverLeaks(t *testing.T) {
	s := NewSecret(rawToken)

	if got := s.Reveal(); got != rawToken {
		t.Fatalf("Reveal() = %q, want the raw value", got)
	}
	if strings.Contains(s.String(), rawToken) {
		t.Fatalf("String() leaked: %q", s.String())
	}
	if strings.Contains(fmt.Sprintf("%v %s %#v", s, s, s), rawToken) {
		t.Fatalf("fmt verbs leaked the secret")
	}

	b, err := json.Marshal(struct{ Token Secret }{Token: s})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), rawToken) {
		t.Fatalf("json.Marshal leaked: %s", b)
	}
}

func TestResolvePrefersEnvOverKeychain(t *testing.T) {
	keyring.MockInit()
	if err := KeychainSet("gha", "default", NewSecret("from-keychain")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GH_TOKEN", rawToken)

	spec := TokenSpec{Provider: "gha", Env: "default", EnvVars: []string{"GH_TOKEN"}}
	res, err := spec.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if res.Secret.Reveal() != rawToken {
		t.Fatalf("expected env value to win, got source=%s", res.Source)
	}
	if res.Source != SourceEnv {
		t.Fatalf("Source = %q, want %q", res.Source, SourceEnv)
	}
}

func TestResolveFallsBackToKeychain(t *testing.T) {
	keyring.MockInit()
	if err := KeychainSet("gha", "default", NewSecret(rawToken)); err != nil {
		t.Fatal(err)
	}

	spec := TokenSpec{Provider: "gha", Env: "default", EnvVars: []string{"GH_TOKEN_UNSET_XYZ"}}
	res, err := spec.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if res.Secret.Reveal() != rawToken {
		t.Fatalf("expected keychain value, got source=%s", res.Source)
	}
	if res.Source != SourceKeychain {
		t.Fatalf("Source = %q, want %q", res.Source, SourceKeychain)
	}
}

func TestResolveErrorsWhenMissing(t *testing.T) {
	keyring.MockInit()
	spec := TokenSpec{Provider: "gha", Env: "default", EnvVars: []string{"GH_TOKEN_UNSET_XYZ"}}
	_, err := spec.Resolve()
	if err == nil {
		t.Fatal("expected error when no credential source is available")
	}
}

func TestKeychainRoundTrip(t *testing.T) {
	keyring.MockInit()
	if err := KeychainSet("gha", "prod", NewSecret(rawToken)); err != nil {
		t.Fatal(err)
	}
	got, err := KeychainGet("gha", "prod")
	if err != nil {
		t.Fatal(err)
	}
	if got.Reveal() != rawToken {
		t.Fatalf("round-trip mismatch: %q", got.Reveal())
	}
	if err := KeychainDelete("gha", "prod"); err != nil {
		t.Fatal(err)
	}
	if _, err := KeychainGet("gha", "prod"); err == nil {
		t.Fatal("expected error after delete")
	}
}
