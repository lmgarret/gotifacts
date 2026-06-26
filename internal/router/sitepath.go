// Package router implements the URL ⇄ filesystem-path convention that is the
// core contract of gotifacts, plus host-based request classification.
//
// Convention: strip the configured base domain from a host. The remaining
// sub-labels, read left→right, run [most-specific … least-specific]. The logical
// directory is those labels reversed; each site's content is then stored under a
// reserved [ContentLeaf] subdir so a site can be both a leaf and a parent of
// other sites (see [SitePath.ContentDir]):
//
//	app.claude.<base>   → sites/claude/app/@site   (group "claude", slug "app")
//	a.sub.grp.<base>    → sites/grp/sub/a/@site     (group "grp/sub", slug "a")
//	demo.<base>         → sites/demo/@site          (group "",        slug "demo")
//
// Total depth (group segments + slug) must be ≤ 3. Each label must match
// [LabelPattern].
package router

import (
	"errors"
	"regexp"
	"strings"
)

// MaxDepth is the maximum number of path segments (group segments + slug).
const MaxDepth = 3

// LabelPattern is the regular expression every host label / path segment must match.
const LabelPattern = `^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`

// ContentLeaf is the reserved subdirectory that holds a site's actual content,
// nested under its logical [SitePath.Dir]. The leading "@" makes it impossible
// to collide with any slug or group segment (which must match [LabelPattern]),
// so the directory at Dir() can simultaneously hold this site's content and the
// directories of nested child sites.
const ContentLeaf = "@site"

var labelRE = regexp.MustCompile(LabelPattern)

// Errors returned by the mapping functions.
var (
	ErrNotBaseSubdomain = errors.New("host is not a sub-domain of the base domain")
	ErrEmptyHost        = errors.New("empty host")
	ErrTooDeep          = errors.New("path depth exceeds maximum")
	ErrEmptySlug        = errors.New("slug must not be empty")
	ErrInvalidLabel     = errors.New("label does not match required pattern")
)

// SitePath identifies a site by its group sub-path and leaf slug.
type SitePath struct {
	// Group is the slash-joined group segments, possibly empty (flat site).
	Group string
	// Slug is the leaf identifier of the site.
	Slug string
}

// GroupSegments returns the group split into its individual segments.
func (s SitePath) GroupSegments() []string {
	if s.Group == "" {
		return nil
	}
	return strings.Split(s.Group, "/")
}

// Dir returns the logical relative path of the site (group segments then slug),
// e.g. "grp/sub/a". It is always clean and contains no traversal. This is the
// site's identity used for grant targets; use [SitePath.ContentDir] for the
// directory that actually holds the site's files.
func (s SitePath) Dir() string {
	if s.Group == "" {
		return s.Slug
	}
	return s.Group + "/" + s.Slug
}

// ContentDir returns the on-disk relative directory that holds the site's
// content: its [SitePath.Dir] plus the reserved [ContentLeaf], e.g.
// "grp/sub/a/@site". Keeping content in a reserved leaf lets the same logical
// path also be a parent of nested child sites without their files colliding.
func (s SitePath) ContentDir() string {
	return s.Dir() + "/" + ContentLeaf
}

// Host renders the site's host name under base, e.g. "a.sub.grp.<base>".
// Group segments are emitted in reverse (least-specific rightmost), with the
// slug as the leftmost (most-specific) label.
func (s SitePath) Host(base string) string {
	labels := []string{s.Slug}
	segs := s.GroupSegments()
	for i := len(segs) - 1; i >= 0; i-- {
		labels = append(labels, segs[i])
	}
	labels = append(labels, base)
	return strings.Join(labels, ".")
}

// URL renders the canonical https URL for the site under base.
func (s SitePath) URL(base string) string {
	return "https://" + s.Host(base)
}

// Validate checks the slug, group segments, label syntax, and depth cap.
func (s SitePath) Validate() error {
	if s.Slug == "" {
		return ErrEmptySlug
	}
	segs := s.GroupSegments()
	if len(segs)+1 > MaxDepth {
		return ErrTooDeep
	}
	if !labelRE.MatchString(s.Slug) {
		return ErrInvalidLabel
	}
	for _, g := range segs {
		if !labelRE.MatchString(g) {
			return ErrInvalidLabel
		}
	}
	return nil
}

// NewSitePath builds a validated SitePath from a raw group string and slug.
func NewSitePath(group, slug string) (SitePath, error) {
	group = strings.Trim(strings.ToLower(strings.TrimSpace(group)), "/")
	// Collapse any accidental empty segments.
	var segs []string
	for _, g := range strings.Split(group, "/") {
		if g != "" {
			segs = append(segs, g)
		}
	}
	sp := SitePath{Group: strings.Join(segs, "/"), Slug: strings.ToLower(strings.TrimSpace(slug))}
	if err := sp.Validate(); err != nil {
		return SitePath{}, err
	}
	return sp, nil
}

// hostLabels strips an optional :port and the trailing base domain from host,
// returning the remaining sub-labels (left→right) or an error.
func hostLabels(host, base string) ([]string, error) {
	host = normalizeHost(host)
	if host == "" {
		return nil, ErrEmptyHost
	}
	base = strings.ToLower(base)
	if host == base {
		return nil, ErrNotBaseSubdomain
	}
	suffix := "." + base
	if !strings.HasSuffix(host, suffix) {
		return nil, ErrNotBaseSubdomain
	}
	prefix := strings.TrimSuffix(host, suffix)
	if prefix == "" {
		return nil, ErrNotBaseSubdomain
	}
	return strings.Split(prefix, "."), nil
}

// IsBaseHost reports whether host is exactly the apex (management) domain.
func IsBaseHost(host, base string) bool {
	return normalizeHost(host) == strings.ToLower(base)
}

// ParseHost maps a request host to a SitePath, validating syntax and depth.
func ParseHost(host, base string) (SitePath, error) {
	labels, err := hostLabels(host, base)
	if err != nil {
		return SitePath{}, err
	}
	if len(labels) > MaxDepth {
		return SitePath{}, ErrTooDeep
	}
	// Reverse labels to get directory order: [group… , slug].
	dir := make([]string, len(labels))
	for i, l := range labels {
		dir[len(labels)-1-i] = l
	}
	slug := dir[len(dir)-1]
	group := strings.Join(dir[:len(dir)-1], "/")
	sp := SitePath{Group: group, Slug: slug}
	if err := sp.Validate(); err != nil {
		return SitePath{}, err
	}
	return sp, nil
}

func normalizeHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	// Strip port if present (IPv6 hosts are not expected here).
	if i := strings.LastIndexByte(host, ':'); i >= 0 && !strings.Contains(host[i:], "]") {
		host = host[:i]
	}
	return strings.TrimSuffix(host, ".")
}
