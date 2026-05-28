package credential

import keyring "github.com/zalando/go-keyring"

// keyringService namespaces all ddc entries in the OS keychain.
const keyringService = "ddc"

func keyringAccount(provider, env string) string {
	if env == "" {
		return provider
	}
	return provider + ":" + env
}

// KeychainGet reads a stored credential for the provider/environment.
func KeychainGet(provider, env string) (Secret, error) {
	v, err := keyring.Get(keyringService, keyringAccount(provider, env))
	if err != nil {
		return Secret{}, err
	}
	return NewSecret(v), nil
}

// KeychainSet stores a credential in the OS keychain.
func KeychainSet(provider, env string, s Secret) error {
	return keyring.Set(keyringService, keyringAccount(provider, env), s.Reveal())
}

// KeychainDelete removes a stored credential.
func KeychainDelete(provider, env string) error {
	return keyring.Delete(keyringService, keyringAccount(provider, env))
}
