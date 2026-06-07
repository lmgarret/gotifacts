package router

import "testing"

const base = "example.com"

func TestParseHost(t *testing.T) {
	tests := []struct {
		host      string
		wantGroup string
		wantSlug  string
		wantErr   bool
	}{
		{"app.claude.example.com", "claude", "app", false},
		{"a.sub.grp.example.com", "grp/sub", "a", false},
		{"demo.example.com", "", "demo", false},
		{"APP.CLAUDE.EXAMPLE.COM", "claude", "app", false},
		{"app.claude.example.com:8080", "claude", "app", false},
		{"app.claude.example.com.", "claude", "app", false},
		// Too deep: 4 labels under base.
		{"w.x.y.z.example.com", "", "", true},
		// Apex itself is not a site.
		{"example.com", "", "", true},
		// Different domain.
		{"app.other.com", "", "", true},
		// Invalid label characters.
		{"App_1.example.com", "", "", true},
		{"-bad.example.com", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			sp, err := ParseHost(tt.host, base)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", sp)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if sp.Group != tt.wantGroup || sp.Slug != tt.wantSlug {
				t.Fatalf("got group=%q slug=%q, want group=%q slug=%q", sp.Group, sp.Slug, tt.wantGroup, tt.wantSlug)
			}
		})
	}
}

func TestSitePathRoundTrip(t *testing.T) {
	cases := []SitePath{
		{Group: "claude", Slug: "app"},
		{Group: "grp/sub", Slug: "a"},
		{Group: "", Slug: "demo"},
	}
	for _, sp := range cases {
		host := sp.Host(base)
		got, err := ParseHost(host, base)
		if err != nil {
			t.Fatalf("ParseHost(%q): %v", host, err)
		}
		if got != sp {
			t.Fatalf("round trip mismatch: %+v -> %q -> %+v", sp, host, got)
		}
	}
}

func TestSitePathDir(t *testing.T) {
	if got := (SitePath{Group: "grp/sub", Slug: "a"}).Dir(); got != "grp/sub/a" {
		t.Fatalf("Dir() = %q", got)
	}
	if got := (SitePath{Slug: "demo"}).Dir(); got != "demo" {
		t.Fatalf("flat Dir() = %q", got)
	}
}

func TestNewSitePathValidation(t *testing.T) {
	if _, err := NewSitePath("a/b", "c"); err != nil {
		t.Fatalf("valid path rejected: %v", err)
	}
	// Depth 4 (3 group + slug) must be rejected.
	if _, err := NewSitePath("a/b/c", "d"); err == nil {
		t.Fatal("expected too-deep rejection")
	}
	// Traversal-ish input must not validate.
	if _, err := NewSitePath("..", "x"); err == nil {
		t.Fatal("expected invalid label rejection for '..'")
	}
	if _, err := NewSitePath("", ""); err == nil {
		t.Fatal("expected empty slug rejection")
	}
}

func TestIsBaseHost(t *testing.T) {
	if !IsBaseHost("example.com", base) {
		t.Fatal("apex not recognized")
	}
	if !IsBaseHost("EXAMPLE.COM:8080", base) {
		t.Fatal("apex with port/case not recognized")
	}
	if IsBaseHost("app.example.com", base) {
		t.Fatal("subdomain wrongly treated as apex")
	}
}
