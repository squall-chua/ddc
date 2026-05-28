package redact

import (
	"strings"
	"testing"
)

func TestScrubRedactsSecrets(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		secret string // substring that must NOT survive scrubbing
	}{
		{
			name:   "github classic PAT",
			in:     "token is ghp_abcdefghijklmnopqrstuvwxyz0123456789 here",
			secret: "ghp_abcdefghijklmnopqrstuvwxyz0123456789",
		},
		{
			name:   "github fine-grained PAT",
			in:     "GH_TOKEN=github_pat_11ABCDE0a1b2c3d4e5f6g7_h8i9j0k1l2m3n4o5p6q7r8s9t0u1v2w3x4y5z6",
			secret: "github_pat_11ABCDE0a1b2c3d4e5f6g7_h8i9j0k1l2m3n4o5p6q7r8s9t0u1v2w3x4y5z6",
		},
		{
			name:   "aws access key id",
			in:     "AWS_ACCESS_KEY_ID is AKIAIOSFODNN7EXAMPLE in env",
			secret: "AKIAIOSFODNN7EXAMPLE",
		},
		{
			name:   "jwt",
			in:     "Authorization header eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0In0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U end",
			secret: "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0In0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U",
		},
		{
			name:   "bearer token",
			in:     "curl -H 'Authorization: Bearer sk-supersecretvalue1234567890' url",
			secret: "sk-supersecretvalue1234567890",
		},
		{
			name:   "url with basic auth",
			in:     "cloning https://alice:hunter2pass@github.com/org/repo.git now",
			secret: "hunter2pass",
		},
		{
			name:   "password key-value",
			in:     `db config password=s3cr3tP@ss host=localhost`,
			secret: "s3cr3tP@ss",
		},
		{
			name:   "json secret field",
			in:     `{"api_key": "abc123def456ghi789", "region": "us-east-1"}`,
			secret: "abc123def456ghi789",
		},
		{
			name:   "pem private key",
			in:     "key:\n-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA1234567890abcdef\n-----END RSA PRIVATE KEY-----\ndone",
			secret: "MIIEowIBAAKCAQEA1234567890abcdef",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Scrub(tc.in)
			if strings.Contains(got, tc.secret) {
				t.Fatalf("secret survived scrubbing:\n in:  %q\n got: %q\n leaked: %q", tc.in, got, tc.secret)
			}
			if !strings.Contains(got, Marker) {
				t.Fatalf("expected redaction marker %q in output, got: %q", Marker, got)
			}
		})
	}
}

func TestScrubPreservesNonSecrets(t *testing.T) {
	// These must pass through untouched: they are exactly the debugging signal
	// the agent needs (image digests, pod hashes, normal log lines).
	keep := []string{
		"pod nginx-7c5ddbdf54-abcde started; listening on :8080",
		"image sha256:9b2a2b8f5c1d4e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d3e4f5a6b7c8d9e0f",
		"deployment rollout complete: 3/3 replicas ready",
		"GET /healthz 200 OK in 12ms",
	}
	for _, line := range keep {
		t.Run(line, func(t *testing.T) {
			if got := Scrub(line); got != line {
				t.Fatalf("non-secret line was altered:\n in:  %q\n got: %q", line, got)
			}
		})
	}
}
