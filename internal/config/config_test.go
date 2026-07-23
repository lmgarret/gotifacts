package config

import (
	"slices"
	"strings"
	"testing"
)

func TestAliasDomainsLoad(t *testing.T) {
	t.Setenv("GOTIFACTS_BASE_DOMAIN", "Example.com")
	t.Setenv("GOTIFACTS_ALIAS_DOMAINS", "Example.org, alt.example.net ")
	t.Setenv("GOTIFACTS_ADMIN_USERS", "admin@example.com")
	t.Setenv("GOTIFACTS_TRUSTED_PROXIES", "10.0.0.0/8")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if errs := c.Validate(); len(errs) > 0 {
		t.Fatalf("unexpected validation errors: %v", errs)
	}
	// Aliases are lowercased and trimmed, like BaseDomain.
	if want := []string{"example.org", "alt.example.net"}; !slices.Equal(c.AliasDomains, want) {
		t.Fatalf("AliasDomains = %v, want %v", c.AliasDomains, want)
	}
	// AllDomains lists the canonical domain first.
	if want := []string{"example.com", "example.org", "alt.example.net"}; !slices.Equal(c.AllDomains(), want) {
		t.Fatalf("AllDomains() = %v, want %v", c.AllDomains(), want)
	}
}

func TestAliasDomainsValidation(t *testing.T) {
	base := &Config{
		BaseDomain:        "example.com",
		ListenAddr:        DefaultListenAddr,
		DataDir:           DefaultDataDir,
		ForwardAuthHeader: DefaultForwardAuthHeader,
		AdminUsers:        []string{"a@example.com"},
		MaxUploadBytes:    DefaultMaxUploadBytes,
		MaxExtractBytes:   DefaultMaxExtractBytes,
		MaxExtractEntries: DefaultMaxExtractEntries,
		LogLevel:          DefaultLogLevel,
		LogFormat:         DefaultLogFormat,
	}
	cases := []struct {
		name    string
		aliases []string
		wantSub string // substring of the expected error; "" means no error
	}{
		{"valid", []string{"example.org"}, ""},
		{"malformed", []string{".example.org"}, "malformed"},
		{"double-dot", []string{"ex..ample.org"}, "malformed"},
		{"duplicates-base", []string{"example.com"}, "duplicates"},
		{"duplicate-alias", []string{"example.org", "example.org"}, "listed more than once"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := *base
			c.AliasDomains = tc.aliases
			errs := c.Validate()
			if tc.wantSub == "" {
				if len(errs) > 0 {
					t.Fatalf("unexpected errors: %v", errs)
				}
				return
			}
			for _, e := range errs {
				if strings.Contains(e.Error(), tc.wantSub) {
					return
				}
			}
			t.Fatalf("expected an error containing %q, got %v", tc.wantSub, errs)
		})
	}
}
