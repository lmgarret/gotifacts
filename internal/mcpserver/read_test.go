package mcpserver

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/lmgarret/gotifacts/internal/auth"
	"github.com/lmgarret/gotifacts/internal/ingest"
	"github.com/lmgarret/gotifacts/internal/keys"
	"github.com/lmgarret/gotifacts/internal/router"
	"github.com/lmgarret/gotifacts/internal/store"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	notReadableFmt = "site %q in group %q not found or not readable by this connection"
	pngHeader      = "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"
)

func adminReq() *mcpsdk.CallToolRequest {
	return mcpReq(&auth.Principal{User: "tester", Admin: true, Source: auth.SourceAPIKey})
}

func readReq(grants ...store.Grant) *mcpsdk.CallToolRequest {
	return mcpReq(mcpPrincipal(grants...))
}

func readGrant(kind store.GrantKind, target string) store.Grant {
	return store.Grant{Kind: kind, Target: target, Permissions: []keys.Capability{keys.CapRead}}
}

func resText(t *testing.T, res *mcpsdk.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		return ""
	}
	tc, ok := res.Content[0].(*mcpsdk.TextContent)
	if !ok {
		t.Fatalf("content is %T, want *TextContent", res.Content[0])
	}
	return tc.Text
}

// publishSiteFiles publishes group/slug as an admin. files maps path to
// content; an index.html is added when absent.
func publishSiteFiles(t *testing.T, s *Service, group, slug string, files map[string]string) {
	t.Helper()
	in := publishInput{Slug: slug, Group: group, Title: "Title " + slug}
	if _, ok := files["index.html"]; !ok {
		in.Files = append(in.Files, publishFile{Path: "index.html", Content: "<h1>" + slug + "</h1>"})
	}
	for p, c := range files {
		in.Files = append(in.Files, publishFile{Path: p, Content: c})
	}
	res, _, err := s.publishSite(context.Background(), adminReq(), in)
	if err != nil || res.IsError {
		t.Fatalf("publish %s/%s: %v / %+v", group, slug, err, res.Content)
	}
}

// liveDir returns the on-disk live directory of group/slug.
func liveDir(t *testing.T, s *Service, group, slug string) string {
	t.Helper()
	sp, err := router.NewSitePath(group, slug)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := s.pub.RevisionDir(sp, ingest.CurrentRevision)
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeLive(t *testing.T, s *Service, group, slug, rel string, data []byte) {
	t.Helper()
	p := filepath.Join(liveDir(t, s, group, slug), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func setHidden(t *testing.T, s *Service, group, slug string) {
	t.Helper()
	h := true
	if _, err := s.store.PatchSite(context.Background(), group, slug, store.SitePatch{Hidden: &h}); err != nil {
		t.Fatal(err)
	}
}

// callAllReadTools runs every read tool against group/slug and returns the
// error results of the three file-level tools in order.
func callAllReadTools(t *testing.T, s *Service, req *mcpsdk.CallToolRequest, group, slug string) []*mcpsdk.CallToolResult {
	t.Helper()
	ctx := context.Background()
	r1, _, _ := s.listFiles(ctx, req, listFilesInput{Group: group, Slug: slug})
	r2, _, _ := s.readFile(ctx, req, readFileInput{Group: group, Slug: slug})
	r3, _, _ := s.searchSite(ctx, req, searchSiteInput{Group: group, Slug: slug, Query: "x"})
	return []*mcpsdk.CallToolResult{r1, r2, r3}
}

func assertNotReadable(t *testing.T, results []*mcpsdk.CallToolResult, group, slug string) {
	t.Helper()
	want := fmt.Sprintf(notReadableFmt, slug, group)
	for i, r := range results {
		if !r.IsError {
			t.Errorf("tool %d: expected error for %s/%s", i, group, slug)
			continue
		}
		if got := resText(t, r); got != want {
			t.Errorf("tool %d: error = %q, want %q", i, got, want)
		}
	}
}

func assertReadable(t *testing.T, results []*mcpsdk.CallToolResult, group, slug string) {
	t.Helper()
	for i, r := range results {
		if r.IsError {
			t.Errorf("tool %d: unexpected error for %s/%s: %s", i, group, slug, resText(t, r))
		}
	}
}

func TestReadToolsRequireReadCapability(t *testing.T) {
	s, _ := newTestService(t)
	publishSiteFiles(t, s, "docs", "app", nil)

	// Every other capability, but not read.
	noRead := readReq(store.Grant{Kind: store.GrantGroup, Target: "", Permissions: []keys.Capability{
		keys.CapPublish, keys.CapPatch, keys.CapUnpublish, keys.CapRollback, keys.CapPurge,
	}})
	assertNotReadable(t, callAllReadTools(t, s, noRead, "docs", "app"), "docs", "app")

	_, out, err := s.listSites(context.Background(), noRead, listSitesInput{})
	if err != nil || out.Total != 0 || len(out.Sites) != 0 {
		t.Fatalf("list_sites without read: total=%d sites=%d err=%v", out.Total, len(out.Sites), err)
	}

	// No principal at all.
	bare := &mcpsdk.CallToolRequest{}
	if res, _, _ := s.readFile(context.Background(), bare, readFileInput{Slug: "app", Group: "docs"}); !res.IsError {
		t.Fatal("expected authentication error")
	}
}

func TestReadToolsGrantScoping(t *testing.T) {
	s, _ := newTestService(t)
	publishSiteFiles(t, s, "docs", "app", nil)
	publishSiteFiles(t, s, "docs", "other", nil)
	publishSiteFiles(t, s, "claude", "page", nil)

	group := readReq(readGrant(store.GrantGroup, "docs"))
	assertReadable(t, callAllReadTools(t, s, group, "docs", "app"), "docs", "app")
	assertReadable(t, callAllReadTools(t, s, group, "docs", "other"), "docs", "other")
	assertNotReadable(t, callAllReadTools(t, s, group, "claude", "page"), "claude", "page")

	site := readReq(readGrant(store.GrantSite, "docs/app"))
	assertReadable(t, callAllReadTools(t, s, site, "docs", "app"), "docs", "app")
	assertNotReadable(t, callAllReadTools(t, s, site, "docs", "other"), "docs", "other")
	assertNotReadable(t, callAllReadTools(t, s, site, "claude", "page"), "claude", "page")

	admin := adminReq()
	assertReadable(t, callAllReadTools(t, s, admin, "docs", "app"), "docs", "app")
	assertReadable(t, callAllReadTools(t, s, admin, "claude", "page"), "claude", "page")
}

func TestReadToolsDefaultGroup(t *testing.T) {
	s, cfg := newTestService(t)
	publishSiteFiles(t, s, cfg.MCPGroup, "mine", map[string]string{"llms.txt": "hello"})

	req := readReq(readGrant(store.GrantGroup, cfg.MCPGroup))
	res, out, _ := s.readFile(context.Background(), req, readFileInput{Slug: "mine"})
	if res.IsError || out.Content != "hello" {
		t.Fatalf("default group read: %+v / %+v", res, out)
	}
	// The default does not apply to list_sites: an empty group lists all.
	_, sites, _ := s.listSites(context.Background(), readReq(readGrant(store.GrantGroup, "")), listSitesInput{})
	if sites.Total != 1 {
		t.Fatalf("list_sites total = %d, want 1", sites.Total)
	}
}

func TestReadToolsHiddenSites(t *testing.T) {
	s, _ := newTestService(t)
	publishSiteFiles(t, s, "docs", "pub", nil)
	publishSiteFiles(t, s, "docs", "secret", nil)
	setHidden(t, s, "docs", "secret")
	ctx := context.Background()
	req := readReq(readGrant(store.GrantGroup, "docs"))

	assertReadable(t, callAllReadTools(t, s, req, "docs", "secret"), "docs", "secret")

	_, out, _ := s.listSites(ctx, req, listSitesInput{})
	if out.Total != 1 || out.Sites[0].Slug != "pub" || out.Sites[0].Hidden {
		t.Fatalf("default listing = %+v", out)
	}
	_, out, _ = s.listSites(ctx, req, listSitesInput{IncludeHidden: true})
	if out.Total != 2 {
		t.Fatalf("include_hidden total = %d, want 2", out.Total)
	}
	for _, v := range out.Sites {
		if v.Hidden != (v.Slug == "secret") {
			t.Errorf("site %s hidden=%v", v.Slug, v.Hidden)
		}
	}
}

func TestReadToolsNotFoundIsUniform(t *testing.T) {
	s, _ := newTestService(t)
	publishSiteFiles(t, s, "docs", "app", nil)
	publishSiteFiles(t, s, "docs", "gone", nil)
	if err := s.pub.Unpublish(context.Background(), "docs", "gone"); err != nil {
		t.Fatal(err)
	}

	// Exists but no grant.
	assertNotReadable(t, callAllReadTools(t, s, readReq(readGrant(store.GrantGroup, "claude")), "docs", "app"), "docs", "app")
	// Missing, for an authorised caller.
	admin := adminReq()
	assertNotReadable(t, callAllReadTools(t, s, admin, "docs", "missing"), "docs", "missing")
	// Soft-deleted, for an authorised caller.
	assertNotReadable(t, callAllReadTools(t, s, admin, "docs", "gone"), "docs", "gone")
	// Invalid slug.
	assertNotReadable(t, callAllReadTools(t, s, admin, "docs", "Bad Slug!"), "docs", "Bad Slug!")
}

func TestListSitesFilteringAndFlags(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()
	publishSiteFiles(t, s, "docs", "alpha", map[string]string{"llms.txt": "a", "llms-full.txt": "aa"})
	publishSiteFiles(t, s, "docs", "beta", map[string]string{"llms.txt": "b"})
	publishSiteFiles(t, s, "docs/sub", "gamma", nil)
	publishSiteFiles(t, s, "claude", "delta", nil)
	if _, err := s.store.PatchSite(ctx, "docs", "beta", store.SitePatch{Tags: &[]string{"api"}}); err != nil {
		t.Fatal(err)
	}

	req := readReq(readGrant(store.GrantGroup, "docs"))
	_, out, _ := s.listSites(ctx, req, listSitesInput{})
	if out.Total != 3 {
		t.Fatalf("readable total = %d, want 3 (claude/delta must be filtered): %+v", out.Total, out.Sites)
	}
	byslug := map[string]siteView{}
	for _, v := range out.Sites {
		byslug[v.Slug] = v
	}
	if a := byslug["alpha"]; !a.LLMSTxt || !a.LLMSFullTxt || a.URL != "https://alpha.docs.example.com" {
		t.Errorf("alpha = %+v", a)
	}
	if b := byslug["beta"]; !b.LLMSTxt || b.LLMSFullTxt || len(b.Tags) != 1 {
		t.Errorf("beta = %+v", b)
	}
	if g := byslug["gamma"]; g.LLMSTxt || g.Tags == nil || g.Group != "docs/sub" {
		t.Errorf("gamma = %+v", g)
	}

	for _, tc := range []struct {
		name string
		in   listSitesInput
		want int
	}{
		{"query", listSitesInput{Query: "alph"}, 1},
		{"tag", listSitesInput{Tag: "api"}, 1},
		{"group subtree", listSitesInput{Group: "docs/sub"}, 1},
		{"group other", listSitesInput{Group: "claude"}, 0},
	} {
		if _, o, _ := s.listSites(ctx, req, tc.in); o.Total != tc.want {
			t.Errorf("%s: total = %d, want %d", tc.name, o.Total, tc.want)
		}
	}

	res, _, _ := s.listSites(ctx, req, listSitesInput{Query: "alpha"})
	if txt := resText(t, res); !strings.Contains(txt, "docs/alpha — Title alpha (llms.txt, llms-full.txt)") {
		t.Errorf("summary = %q", txt)
	}
}

func TestListSitesPaging(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()
	for _, slug := range []string{"a", "b", "c", "d", "e"} {
		publishSiteFiles(t, s, "docs", slug, nil)
	}
	req := readReq(readGrant(store.GrantGroup, "docs"))

	var got []string
	for off := 0; off < 5; off += 2 {
		_, out, _ := s.listSites(ctx, req, listSitesInput{Limit: 2, Offset: off})
		if out.Total != 5 {
			t.Fatalf("total = %d, want 5", out.Total)
		}
		for _, v := range out.Sites {
			got = append(got, v.Slug)
		}
	}
	if strings.Join(got, "") != "abcde" {
		t.Fatalf("paged slugs = %v", got)
	}
	if _, out, _ := s.listSites(ctx, req, listSitesInput{Offset: 99}); len(out.Sites) != 0 || out.Total != 5 {
		t.Fatalf("offset past end: %+v", out)
	}
	if _, out, _ := s.listSites(ctx, req, listSitesInput{Limit: 9999, Offset: -3}); len(out.Sites) != 5 {
		t.Fatalf("clamped limit/offset: %+v", out)
	}
}

func readOK(t *testing.T, s *Service, in readFileInput) (*mcpsdk.CallToolResult, readFileOutput) {
	t.Helper()
	in.Group = "docs"
	if in.Slug == "" {
		in.Slug = "app"
	}
	res, out, err := s.readFile(context.Background(), adminReq(), in)
	if err != nil {
		t.Fatal(err)
	}
	return res, out
}

func TestReadFileDefaultsAndPaths(t *testing.T) {
	s, _ := newTestService(t)
	publishSiteFiles(t, s, "docs", "app", map[string]string{
		"llms.txt":         "llms body",
		"guide/index.html": "<p>guide</p>",
		"a/b.md":           "# b",
	})
	publishSiteFiles(t, s, "docs", "plain", nil)

	for _, tc := range []struct {
		name, slug, path, wantPath, wantContent string
	}{
		{"default llms", "app", "", "llms.txt", "llms body"},
		{"default index fallback", "plain", "", "index.html", "<h1>plain</h1>"},
		{"directory slash", "app", "guide/", "guide/index.html", "<p>guide</p>"},
		{"directory bare", "app", "guide", "guide/index.html", "<p>guide</p>"},
		{"root slash", "plain", "/", "index.html", "<h1>plain</h1>"},
		{"leading slash", "app", "/a/b.md", "a/b.md", "# b"},
		{"dot segments inside root", "app", "a/../llms.txt", "llms.txt", "llms body"},
	} {
		res, out := readOK(t, s, readFileInput{Slug: tc.slug, Path: tc.path})
		if res.IsError {
			t.Errorf("%s: %s", tc.name, resText(t, res))
			continue
		}
		if out.Path != tc.wantPath || out.Content != tc.wantContent || out.Truncated {
			t.Errorf("%s: out = %+v", tc.name, out)
		}
		if resText(t, res) != tc.wantContent {
			t.Errorf("%s: text = %q", tc.name, resText(t, res))
		}
	}
}

func TestReadFileTraversalIsNotFound(t *testing.T) {
	s, _ := newTestService(t)
	publishSiteFiles(t, s, "docs", "app", nil)
	live := liveDir(t, s, "docs", "app")
	// A file just outside the site root that clamping must never reach.
	if err := os.WriteFile(filepath.Join(filepath.Dir(live), "secret.txt"), []byte("top secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{
		"../secret.txt",
		"../../../../etc/passwd",
		"/../secret.txt",
		"..%2fsecret.txt",
		"%2e%2e/secret.txt",
		"a/../../secret.txt",
	} {
		res, out := readOK(t, s, readFileInput{Path: p})
		if !res.IsError || !strings.Contains(resText(t, res), "not found") || out.Content != "" {
			t.Errorf("path %q: res=%q out=%+v", p, resText(t, res), out)
		}
	}
}

func TestReadFileRefusesSymlinks(t *testing.T) {
	s, _ := newTestService(t)
	publishSiteFiles(t, s, "docs", "app", nil)
	live := liveDir(t, s, "docs", "app")
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "x.txt"), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "x.txt"), filepath.Join(live, "link.txt")); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(live, "linkdir")); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{"link.txt", "linkdir/x.txt", "linkdir/"} {
		if res, _ := readOK(t, s, readFileInput{Path: p}); !res.IsError {
			t.Errorf("path %q: expected not found", p)
		}
	}
	// list_files and search_site skip the symlinks as well.
	_, files, _ := s.listFiles(context.Background(), adminReq(), listFilesInput{Group: "docs", Slug: "app"})
	if files.Total != 1 {
		t.Errorf("list_files followed symlinks: %+v", files.Files)
	}
	_, found, _ := s.searchSite(context.Background(), adminReq(), searchSiteInput{Group: "docs", Slug: "app", Query: "outside"})
	if len(found.Matches) != 0 {
		t.Errorf("search_site followed symlinks: %+v", found.Matches)
	}
}

func TestReadFileRefusesBinary(t *testing.T) {
	s, _ := newTestService(t)
	publishSiteFiles(t, s, "docs", "app", nil)
	writeLive(t, s, "docs", "app", "logo.png", []byte(pngHeader))
	writeLive(t, s, "docs", "app", "bad.txt", append([]byte("fine start\n"), 0xff, 0xfe, 0xfd))

	res, _ := readOK(t, s, readFileInput{Path: "logo.png"})
	if !res.IsError || !strings.Contains(resText(t, res), "logo.png is a binary file (image/png)") ||
		!strings.Contains(resText(t, res), "read_file returns text only") {
		t.Errorf("png: %q", resText(t, res))
	}
	if res, _ := readOK(t, s, readFileInput{Path: "bad.txt"}); !res.IsError {
		t.Error("invalid UTF-8 should be refused")
	}
	if res, _ := readOK(t, s, readFileInput{Path: "missing.txt"}); !res.IsError || !strings.Contains(resText(t, res), "not found") {
		t.Errorf("missing: %q", resText(t, res))
	}
}

func TestReadFilePagingAcrossMultibyteRunes(t *testing.T) {
	s, _ := newTestService(t)
	publishSiteFiles(t, s, "docs", "app", nil)
	body := strings.Repeat("a€é😀", 40) // 1+3+2+4 bytes per repetition
	writeLive(t, s, "docs", "app", "utf.txt", []byte(body))

	for _, limit := range []int{4, 5, 7, 11, 13} {
		var b strings.Builder
		offset := 0
		for pages := 0; ; pages++ {
			if pages > len(body) {
				t.Fatalf("limit %d: paging did not terminate", limit)
			}
			res, out := readOK(t, s, readFileInput{Path: "utf.txt", Offset: offset, Limit: limit})
			if res.IsError {
				t.Fatalf("limit %d offset %d: %s", limit, offset, resText(t, res))
			}
			if !utf8.ValidString(out.Content) || out.Offset != int64(offset) || out.Size != int64(len(body)) {
				t.Fatalf("limit %d: bad page %+v", limit, out)
			}
			if out.NextOffset != out.Offset+int64(len(out.Content)) || len(out.Content) > limit {
				t.Fatalf("limit %d: next_offset %d for %d bytes at %d", limit, out.NextOffset, len(out.Content), out.Offset)
			}
			b.WriteString(out.Content)
			if !out.Truncated {
				break
			}
			wantFooter := fmt.Sprintf("[truncated: call read_file with offset=%d to continue]", out.NextOffset)
			if !strings.HasSuffix(resText(t, res), wantFooter) {
				t.Fatalf("limit %d: missing footer in %q", limit, resText(t, res))
			}
			offset = int(out.NextOffset)
		}
		if b.String() != body {
			t.Fatalf("limit %d: reassembled content differs", limit)
		}
	}
}

func TestReadFileOffsetInsideRune(t *testing.T) {
	s, _ := newTestService(t)
	publishSiteFiles(t, s, "docs", "app", nil)
	writeLive(t, s, "docs", "app", "utf.txt", []byte("a€b")) // € occupies bytes 1..3

	_, out := readOK(t, s, readFileInput{Path: "utf.txt", Offset: 2})
	if out.Content != "b" || out.Offset != 4 || out.NextOffset != 5 || out.Truncated {
		t.Fatalf("out = %+v", out)
	}
	_, out = readOK(t, s, readFileInput{Path: "utf.txt", Offset: 1000})
	if out.Content != "" || out.Offset != 5 || out.Truncated {
		t.Fatalf("offset past end: %+v", out)
	}
}

func TestReadFileLimitIsClamped(t *testing.T) {
	s, _ := newTestService(t)
	publishSiteFiles(t, s, "docs", "app", nil)
	writeLive(t, s, "docs", "app", "big.txt", []byte(strings.Repeat("x", 300000)))

	_, out := readOK(t, s, readFileInput{Path: "big.txt"})
	if len(out.Content) != readDefaultLimit || !out.Truncated || out.NextOffset != readDefaultLimit {
		t.Errorf("default limit: len=%d %+v", len(out.Content), out.NextOffset)
	}
	_, out = readOK(t, s, readFileInput{Path: "big.txt", Limit: 10_000_000})
	if len(out.Content) != readMaxLimit || !out.Truncated {
		t.Errorf("max limit: len=%d", len(out.Content))
	}
	_, out = readOK(t, s, readFileInput{Path: "big.txt", Offset: 262144, Limit: -5})
	if len(out.Content) != 300000-262144 || out.Truncated {
		t.Errorf("tail page: len=%d truncated=%v", len(out.Content), out.Truncated)
	}
}

func TestListFilesPrefixAndTruncation(t *testing.T) {
	s, _ := newTestService(t)
	publishSiteFiles(t, s, "docs", "app", map[string]string{
		"docs/a.md": "a", "docs/b.md": "bb", "docs/deep/c.md": "ccc", "z.txt": "z",
	})
	ctx := context.Background()
	req := adminReq()

	_, all, _ := s.listFiles(ctx, req, listFilesInput{Group: "docs", Slug: "app"})
	var paths []string
	for _, f := range all.Files {
		paths = append(paths, f.Path)
	}
	want := "docs/a.md docs/b.md docs/deep/c.md index.html z.txt"
	if strings.Join(paths, " ") != want || all.Total != 5 || all.Truncated {
		t.Fatalf("all = %v total=%d truncated=%v", paths, all.Total, all.Truncated)
	}

	_, sub, _ := s.listFiles(ctx, req, listFilesInput{Group: "docs", Slug: "app", Prefix: "/docs/"})
	if sub.Total != 3 || sub.Files[0].Path != "docs/a.md" || sub.Files[0].Size != 1 {
		t.Fatalf("prefix listing = %+v", sub)
	}

	res, cut, _ := s.listFiles(ctx, req, listFilesInput{Group: "docs", Slug: "app", Limit: 2})
	if len(cut.Files) != 2 || cut.Total != 5 || !cut.Truncated || !strings.Contains(resText(t, res), "[truncated:") {
		t.Fatalf("truncated = %+v / %q", cut, resText(t, res))
	}

	for _, bad := range []string{"nope/", "../x", "index.html"} {
		res, _, _ := s.listFiles(ctx, req, listFilesInput{Group: "docs", Slug: "app", Prefix: bad})
		if !res.IsError {
			t.Errorf("prefix %q: expected error", bad)
		}
	}
}

func TestSearchSiteMatching(t *testing.T) {
	s, _ := newTestService(t)
	publishSiteFiles(t, s, "docs", "app", map[string]string{
		"a.md":     "first line\nThe NEEDLE is here\nlast",
		"b.txt":    "needle again\n   indented Needle  \n",
		"sub/c.md": "no match",
	})
	writeLive(t, s, "docs", "app", "bin.dat", []byte(pngHeader+" needle"))
	writeLive(t, s, "docs", "app", "huge.txt", []byte("needle\n"+strings.Repeat("x", searchMaxFileSize)))
	ctx := context.Background()

	_, out, _ := s.searchSite(ctx, adminReq(), searchSiteInput{Group: "docs", Slug: "app", Query: "nEeDlE"})
	var got []string
	for _, m := range out.Matches {
		got = append(got, fmt.Sprintf("%s:%d:%s", m.Path, m.Line, m.Text))
	}
	want := []string{"a.md:2:The NEEDLE is here", "b.txt:1:needle again", "b.txt:2:indented Needle"}
	if strings.Join(got, "|") != strings.Join(want, "|") || out.Truncated {
		t.Fatalf("matches = %v truncated=%v", got, out.Truncated)
	}

	res, _, _ := s.searchSite(ctx, adminReq(), searchSiteInput{Group: "docs", Slug: "app", Query: "needle", Prefix: "sub"})
	if res.IsError || resText(t, res) != "No matches." {
		t.Errorf("prefix search: %q", resText(t, res))
	}
	res, _, _ = s.searchSite(ctx, adminReq(), searchSiteInput{Group: "docs", Slug: "app", Query: "needle", Prefix: "missing"})
	if !res.IsError {
		t.Error("missing prefix should error")
	}
	res, _, _ = s.searchSite(ctx, adminReq(), searchSiteInput{Group: "docs", Slug: "app", Query: ""})
	if !res.IsError {
		t.Error("empty query should error")
	}
}

func TestSearchSiteTruncationAndTextSummary(t *testing.T) {
	s, _ := newTestService(t)
	long := "hit " + strings.Repeat("é", 400)
	publishSiteFiles(t, s, "docs", "app", map[string]string{
		"a.txt": strings.Repeat("hit\n", 10),
		"b.txt": long,
	})
	ctx := context.Background()

	res, out, _ := s.searchSite(ctx, adminReq(), searchSiteInput{Group: "docs", Slug: "app", Query: "hit", MaxResults: 3})
	if len(out.Matches) != 3 || !out.Truncated || !strings.Contains(resText(t, res), "a.txt:1: hit\n") ||
		!strings.Contains(resText(t, res), "[truncated:") {
		t.Fatalf("truncation: %+v / %q", out, resText(t, res))
	}

	// Exactly max matches available: not truncated.
	_, out, _ = s.searchSite(ctx, adminReq(), searchSiteInput{Group: "docs", Slug: "app", Query: "hit", MaxResults: 11})
	if len(out.Matches) != 11 || out.Truncated {
		t.Fatalf("exact fit: %d truncated=%v", len(out.Matches), out.Truncated)
	}

	_, out, _ = s.searchSite(ctx, adminReq(), searchSiteInput{Group: "docs", Slug: "app", Query: "éé", MaxResults: 1000})
	last := out.Matches[len(out.Matches)-1]
	if n := utf8.RuneCountInString(last.Text); n != searchMaxLineText || !strings.HasSuffix(last.Text, "…") {
		t.Fatalf("long line text has %d runes: %q", n, last.Text[:20])
	}
}

// TestReadToolsOverMCPTransport drives the four tools through the real
// Streamable HTTP transport with a read-only token, validating the generated
// input/output schemas and the tool annotations end-to-end.
func TestReadToolsOverMCPTransport(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()
	publishSiteFiles(t, s, "docs", "app", map[string]string{"llms.txt": "hello needle"})

	const token = "read-access-token"
	if err := s.store.CreateToken(ctx, store.Token{
		Hash: keys.Hash(token), ConnID: "conn-read", Kind: "access", ClientID: "c", User: "tester",
		Grants:    []store.Grant{readGrant(store.GrantGroup, "docs")},
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.testHandler(&auth.Principal{User: "tester"}))
	defer srv.Close()
	session, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "c", Version: "0"}, nil).Connect(ctx, &mcpsdk.StreamableClientTransport{
		Endpoint:   srv.URL + "/mcp",
		HTTPClient: &http.Client{Transport: bearerRoundTripper{token: token}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	annotated := 0
	for _, tool := range tools.Tools {
		if a := tool.Annotations; a != nil && a.ReadOnlyHint {
			annotated++
			if !a.IdempotentHint || a.OpenWorldHint == nil || *a.OpenWorldHint {
				t.Errorf("%s annotations = %+v", tool.Name, a)
			}
		}
	}
	if annotated != 4 {
		t.Errorf("%d read-only tools, want 4", annotated)
	}

	for name, args := range map[string]map[string]any{
		"list_sites":  {},
		"list_files":  {"slug": "app", "group": "docs"},
		"read_file":   {"slug": "app", "group": "docs"},
		"search_site": {"slug": "app", "group": "docs", "query": "NEEDLE"},
	} {
		res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: name, Arguments: args})
		if err != nil || res.IsError {
			t.Fatalf("%s: %v / %+v", name, err, res)
		}
		if res.StructuredContent == nil {
			t.Errorf("%s: no structured content", name)
		}
	}
	// A site outside the read grant is refused with the uniform error.
	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "read_file", Arguments: map[string]any{"slug": "x", "group": "claude"}})
	if err != nil || !res.IsError {
		t.Fatalf("expected tool error, got %v / %+v", err, res)
	}
}
