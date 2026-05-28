// Package provider defines the read-only provider abstraction shared by every
// tool ddc supports. A Provider only knows how to authenticate (read-only) and
// report a safe identity summary; the actual read operations live as typed
// methods on the concrete provider type. There is intentionally no generic
// "execute" method — mutation is absent from the abstraction by design.
package provider

import (
	"context"
	"fmt"
	"sort"
)

// Identity is a non-sensitive summary of an authenticated session. It MUST NOT
// contain secrets — only stable identifiers safe to show an agent (a kube
// context name, a GitHub login, a server host).
type Identity struct {
	Provider string `json:"provider"`
	Account  string `json:"account,omitempty"`  // e.g. kube context name, github login
	Endpoint string `json:"endpoint,omitempty"` // e.g. API host (never includes creds)
	Source   string `json:"source,omitempty"`   // where creds came from: kubeconfig, gh-config, keychain
}

// Provider is the minimal contract every tool integration satisfies.
type Provider interface {
	// Name returns the stable provider key (e.g. "k8s", "gha").
	Name() string
	// Connect resolves credentials read-only for the given environment and
	// prepares the client. It must never return or print the raw secret.
	Connect(ctx context.Context, env string) error
	// Status returns a safe identity summary for `ddc auth status`.
	Status(ctx context.Context) (Identity, error)
}

var registry = map[string]func() Provider{}

// Register makes a provider constructor discoverable by `ddc auth status`.
// Providers call this from their package init.
func Register(name string, ctor func() Provider) {
	registry[name] = ctor
}

// Names returns the registered provider keys in sorted order.
func Names() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// New constructs a registered provider by name.
func New(name string) (Provider, error) {
	ctor, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("unknown provider %q", name)
	}
	return ctor(), nil
}
