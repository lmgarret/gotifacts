package ingest

import (
	"encoding/base64"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// maxFaviconBytes caps the size of a cached favicon. Real favicons are a few KiB;
// this bounds what a single site can push into the registry row.
const maxFaviconBytes = 512 << 10 // 512 KiB

// iconExtTypes maps common favicon file extensions to their MIME type, used when
// a <link> declares no explicit type attribute.
var iconExtTypes = map[string]string{
	".ico":  "image/x-icon",
	".svg":  "image/svg+xml",
	".png":  "image/png",
	".gif":  "image/gif",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".webp": "image/webp",
	".avif": "image/avif",
}

// iconRels are the <link rel> tokens we treat as declaring a favicon, mapped to
// a preference rank (lower is better): a standard rel="icon" beats an
// apple-touch-icon, which beats a monochrome mask-icon.
var iconRels = map[string]int{
	"icon":                         0, // includes rel="shortcut icon"
	"apple-touch-icon":             1,
	"apple-touch-icon-precomposed": 1,
	"mask-icon":                    2,
}

// iconCandidate is one declared <link rel="...icon..."> element.
type iconCandidate struct {
	href    string
	typ     string // the type attribute, lowercased, if any
	relRank int    // from iconRels; lower is preferred
	svg     bool   // scalable icon (preferred over raster)
	size    int    // largest declared pixel dimension; "any" ranks very high
	order   int    // declaration order, for a stable tie-break
}

// detectFavicon inspects the site rooted at dir (which is expected to contain
// index.html) and returns the bytes and MIME type of its best favicon, or
// (nil, "") when none can be resolved.
//
// It parses index.html for <link rel="...icon..."> declarations, preferring a
// standard rel="icon" (SVG over raster, larger over smaller). Each icon is
// resolved to bytes either by decoding an inline data: URI (common for the
// self-contained single-file artifacts gotifacts hosts) or by reading the
// referenced file out of the bundle; external http(s) URLs are never fetched.
// When no declared icon resolves, it falls back to a bundled /favicon.ico,
// mirroring browser behavior.
func detectFavicon(dir string) ([]byte, string) {
	f, err := os.Open(filepath.Join(dir, "index.html"))
	if err != nil {
		return nil, ""
	}
	defer func() { _ = f.Close() }()
	doc, err := html.Parse(f)
	if err != nil {
		return nil, ""
	}

	var cands []iconCandidate
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.DataAtom == atom.Link || n.Data == "link") {
			if c, ok := iconCandidateFrom(n, len(cands)); ok {
				cands = append(cands, c)
			}
		}
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			walk(ch)
		}
	}
	walk(doc)

	sort.SliceStable(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		if a.relRank != b.relRank {
			return a.relRank < b.relRank
		}
		if a.svg != b.svg {
			return a.svg
		}
		if a.size != b.size {
			return a.size > b.size
		}
		return a.order < b.order
	})

	for _, c := range cands {
		if data, ct, ok := resolveIcon(dir, c); ok {
			return data, ct
		}
	}

	// Fall back to a bundled /favicon.ico even when nothing is declared.
	if data, ok := readIconFile(filepath.Join(dir, "favicon.ico")); ok {
		return data, "image/x-icon"
	}
	return nil, ""
}

// iconCandidateFrom extracts a candidate from a <link> node, or reports false if
// the element does not declare an icon.
func iconCandidateFrom(n *html.Node, order int) (iconCandidate, bool) {
	var rel, href, typ, sizes string
	for _, a := range n.Attr {
		switch strings.ToLower(a.Key) {
		case "rel":
			rel = a.Val
		case "href":
			href = a.Val
		case "type":
			typ = a.Val
		case "sizes":
			sizes = a.Val
		}
	}
	rank := -1
	for _, tok := range strings.Fields(strings.ToLower(rel)) {
		if r, ok := iconRels[tok]; ok && (rank == -1 || r < rank) {
			rank = r
		}
	}
	href = strings.TrimSpace(href)
	if rank == -1 || href == "" {
		return iconCandidate{}, false
	}
	typ = strings.ToLower(strings.TrimSpace(typ))
	return iconCandidate{
		href:    href,
		typ:     typ,
		relRank: rank,
		svg:     typ == "image/svg+xml" || strings.HasSuffix(strings.ToLower(hrefPath(href)), ".svg"),
		size:    maxSize(sizes),
		order:   order,
	}, true
}

// resolveIcon turns a candidate into concrete bytes + content-type, or reports
// false when it cannot be resolved from the bundle (or is an external URL).
func resolveIcon(dir string, c iconCandidate) ([]byte, string, bool) {
	u, err := url.Parse(c.href)
	if err != nil {
		return nil, "", false
	}
	// Inline data: URI — decode directly.
	if strings.EqualFold(u.Scheme, "data") {
		return parseDataURI(c.href)
	}
	// External or protocol-relative references are never fetched.
	if u.Scheme != "" || u.Host != "" {
		return nil, "", false
	}
	full, ok := resolveWithin(dir, u.Path)
	if !ok {
		return nil, "", false
	}
	data, ok := readIconFile(full)
	if !ok {
		return nil, "", false
	}
	return data, contentTypeFor(c, full, data), true
}

// resolveWithin maps an href path (root-relative "/x" or relative "x") to an
// absolute path inside dir, refusing anything that would escape it. Both forms
// resolve against the site root, matching how the static server serves the same
// href (index.html sits at the root).
func resolveWithin(dir, rel string) (string, bool) {
	if rel == "" {
		return "", false
	}
	clean := path.Clean("/" + strings.TrimPrefix(rel, "/"))
	full := filepath.Join(dir, filepath.FromSlash(clean))
	rp, err := filepath.Rel(dir, full)
	if err != nil || rp == ".." || strings.HasPrefix(rp, ".."+string(filepath.Separator)) {
		return "", false
	}
	return full, true
}

// readIconFile reads p if it is a non-empty regular file within the size cap.
func readIconFile(p string) ([]byte, bool) {
	info, err := os.Stat(p)
	if err != nil || info.IsDir() || info.Size() == 0 || info.Size() > maxFaviconBytes {
		return nil, false
	}
	data, err := os.ReadFile(p)
	if err != nil || len(data) == 0 {
		return nil, false
	}
	return data, true
}

// contentTypeFor picks a MIME type for a resolved icon: an explicit type
// attribute wins, then the file extension, then content sniffing.
func contentTypeFor(c iconCandidate, p string, data []byte) string {
	if c.typ != "" {
		return c.typ
	}
	if ct, ok := iconExtTypes[strings.ToLower(filepath.Ext(p))]; ok {
		return ct
	}
	return normalizeContentType(http.DetectContentType(data))
}

// parseDataURI decodes a data: URI into its bytes and content-type.
func parseDataURI(uri string) ([]byte, string, bool) {
	// The "data:" scheme is case-insensitive; the prefix is always 5 bytes.
	if len(uri) < 5 || !strings.EqualFold(uri[:5], "data:") {
		return nil, "", false
	}
	meta, payload, ok := strings.Cut(uri[5:], ",")
	if !ok {
		return nil, "", false
	}
	var (
		ct       string
		isBase64 bool
	)
	for i, part := range strings.Split(meta, ";") {
		part = strings.TrimSpace(part)
		switch {
		case strings.EqualFold(part, "base64"):
			isBase64 = true
		case i == 0 && part != "":
			ct = strings.ToLower(part)
		}
	}
	var (
		data []byte
		err  error
	)
	if isBase64 {
		data, err = base64.StdEncoding.DecodeString(stripWhitespace(payload))
	} else {
		var s string
		s, err = url.QueryUnescape(payload)
		data = []byte(s)
	}
	if err != nil || len(data) == 0 || len(data) > maxFaviconBytes {
		return nil, "", false
	}
	if ct == "" {
		ct = normalizeContentType(http.DetectContentType(data))
	}
	return data, ct, true
}

// maxSize returns the largest pixel dimension declared in a sizes attribute
// (e.g. "16x16 32x32" → 32). The special value "any" ranks above any fixed size.
func maxSize(sizes string) int {
	best := 0
	for _, tok := range strings.Fields(strings.ToLower(sizes)) {
		if tok == "any" {
			return 1 << 20
		}
		x := strings.IndexByte(tok, 'x')
		if x <= 0 {
			continue
		}
		if w, err := strconv.Atoi(tok[:x]); err == nil && w > best {
			best = w
		}
	}
	return best
}

// hrefPath returns the path portion of an href (without query/fragment), used
// only for extension sniffing.
func hrefPath(href string) string {
	if u, err := url.Parse(href); err == nil {
		return u.Path
	}
	return href
}

// normalizeContentType drops any "; charset=..." parameter from a sniffed type.
func normalizeContentType(ct string) string {
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		return strings.TrimSpace(ct[:i])
	}
	return ct
}

func stripWhitespace(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\r':
			return -1
		}
		return r
	}, s)
}
