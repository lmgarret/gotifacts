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

func TestSitePathContentDir(t *testing.T) {
	if got := (SitePath{Group: "grp/sub", Slug: "a"}).ContentDir(); got != "grp/sub/a/"+ContentLeaf {
		t.Fatalf("ContentDir() = %q", got)
	}
	if got := (SitePath{Slug: "demo"}).ContentDir(); got != "demo/"+ContentLeaf {
		t.Fatalf("flat ContentDir() = %q", got)
	}
	// The content leaf must never be a valid label, so it can't collide with a
	// real slug or group segment nested under the same path.
	if labelRE.MatchString(ContentLeaf) {
		t.Fatalf("ContentLeaf %q must not match LabelPattern", ContentLeaf)
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

func TestMatchDomain(t *testing.T) {
	domains := []string{"example.com", "example.org"}
	tests := []struct {
		host     string
		wantBase string
		wantOK   bool
	}{
		{"example.com", "example.com", true},            // canonical apex
		{"example.org", "example.org", true},            // alias apex
		{"app.claude.example.com", "example.com", true}, // canonical sub-domain
		{"app.claude.example.org", "example.org", true}, // alias sub-domain
		{"EXAMPLE.ORG:8080", "example.org", true},       // case + port normalized
		{"demo.example.org.", "example.org", true},      // trailing dot normalized
		{"other.com", "", false},                        // unrelated domain
		{"notexample.com", "", false},                   // suffix without label boundary
		{"", "", false},                                 // empty host
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			base, ok := MatchDomain(tt.host, domains)
			if ok != tt.wantOK || base != tt.wantBase {
				t.Fatalf("MatchDomain(%q) = (%q, %v), want (%q, %v)", tt.host, base, ok, tt.wantBase, tt.wantOK)
			}
		})
	}
}

// TestMatchDomainOverlap verifies the longest (most-specific) domain wins when
// one configured domain is a suffix of another, so the correct labels are
// stripped by a subsequent ParseHost.
func TestMatchDomainOverlap(t *testing.T) {
	domains := []string{"example.com", "sites.example.com"}
	base, ok := MatchDomain("demo.sites.example.com", domains)
	if !ok || base != "sites.example.com" {
		t.Fatalf("MatchDomain overlap = (%q, %v), want (sites.example.com, true)", base, ok)
	}
	if sp, err := ParseHost("demo.sites.example.com", base); err != nil || sp.Slug != "demo" || sp.Group != "" {
		t.Fatalf("ParseHost under longest match = %+v, err=%v", sp, err)
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
