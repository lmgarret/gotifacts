// Package mcpserver embeds an OAuth 2.1-protected Model Context Protocol server
// inside gotifacts, exposing a single publish_site tool over Streamable HTTP.
//
// It exists because Claude's mobile/web "custom connector" UI authenticates
// remote MCP servers exclusively via OAuth (no bearer/header field), so the
// env-var skill cannot be used there. This server reuses the existing publish
// pipeline (ingest.Publisher) and key-hashing primitives, and is inert unless
// GOTIFACTS_MCP_ENABLED is set.
//
// The OAuth surface is split across two planes, mirroring gotifacts' existing
// ingest/management split: the browser-facing /mcp/oauth/authorize consent step
// is authenticated by the reverse-proxy forward-auth (a *auth.Principal is
// supplied by the caller); every machine-facing endpoint (metadata, register,
// token, and /mcp itself) authenticates via OAuth and must NOT sit behind
// forward-auth.
package mcpserver

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/lmgarret/gotifacts/internal/archive"
	"github.com/lmgarret/gotifacts/internal/auth"
	"github.com/lmgarret/gotifacts/internal/config"
	"github.com/lmgarret/gotifacts/internal/ingest"
	"github.com/lmgarret/gotifacts/internal/keys"
	"github.com/lmgarret/gotifacts/internal/router"
	"github.com/lmgarret/gotifacts/internal/store"

	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// scopePublish is the only scope MCP-issued tokens carry.
	scopePublish = "publish"
	// codeTTL is the lifetime of an authorization code.
	codeTTL = 10 * time.Minute
	// serverVersion is reported in the MCP initialize handshake.
	serverVersion = "0.1.0"
	// toolListTTL is the cache hint attached to tools/list results. The tool
	// set is static for the lifetime of the process, so this only bounds how
	// stale a client may be after a gotifacts upgrade.
	toolListTTL = time.Hour
	// purgeConfirmID is the input-request key under which purge_site asks for
	// confirmation; the client echoes it back in InputResponses.
	purgeConfirmID = "confirm"
	// purgeConfirmTTL bounds how long an issued purge confirmation stays valid.
	purgeConfirmTTL = 5 * time.Minute
)

// Service holds the MCP + OAuth dependencies and HTTP handlers.
type Service struct {
	cfg     *config.Config
	store   *store.Store
	pub     *ingest.Publisher
	log     *slog.Logger
	csrfKey []byte
	stream  http.Handler
	reg     *rateLimiter
}

// New constructs the MCP service, building the MCP server, registering the
// publish_site tool, and wrapping the Streamable HTTP transport with bearer
// authentication.
func New(cfg *config.Config, st *store.Store, pub *ingest.Publisher, log *slog.Logger) (*Service, error) {
	csrf := make([]byte, 32)
	if _, err := rand.Read(csrf); err != nil {
		return nil, fmt.Errorf("mcp csrf key: %w", err)
	}
	s := &Service{cfg: cfg, store: st, pub: pub, log: log.With("component", "mcp"), csrfKey: csrf, reg: newRateLimiter(20, time.Minute)}

	srv := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "gotifacts", Version: serverVersion}, nil)
	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name: "publish_site",
		Description: "Publish a site to this gotifacts instance and return its public URL. " +
			"Provide a URL-safe `slug` and the content as exactly one of: `html` — a single " +
			"self-contained HTML document (inline CSS/JS) for a one-page site; or `files` — a " +
			"multi-file site as an array of {path, content, encoding} objects (encoding is " +
			"\"utf8\" (default) or \"base64\" for binary assets like images/fonts). A multi-file " +
			"site MUST include a top-level `index.html`. Re-publishing the same slug replaces " +
			"the existing site.",
	}, s.publishSite)
	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "unpublish_site",
		Description: "Soft-delete a published site, taking it offline immediately. The site and its files are retained for a configurable grace period before permanent removal, so an accidental unpublish can be recovered by re-publishing the same slug.",
	}, s.unpublishSite)
	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "update_site",
		Description: "Update the metadata of an existing published site (title, description, tags, hidden flag). Does not replace the site content; use publish_site for that.",
	}, s.updateSite)
	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "rollback_site",
		Description: "Restore a previous version of a site, replacing the current live content. With no revision it restores the most recent archived version; pass a revision (an archive timestamp) to promote that specific version. Requires versioning to be enabled on the gotifacts instance.",
	}, s.rollbackSite)
	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "list_revisions",
		Description: "List a site's available revisions: the current (live) content plus any retained archived versions, newest first. Use this to find the revision id (an archive timestamp) to pass to rollback_site. Requires versioning to be enabled on the gotifacts instance.",
	}, s.listRevisions)
	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "restore_site",
		Description: "Bring a soft-deleted (unpublished) site back online by moving its quarantined files back to live and clearing its deleted status. Use this to undo an accidental unpublish within the grace period.",
	}, s.restoreSite)
	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "purge_site",
		Description: "Permanently and immediately delete a soft-deleted (quarantined) site, bypassing the retention TTL. This is irreversible — the site's files are destroyed. Only use when you are certain the site should be gone. Clients that support elicitation are asked to confirm before anything is deleted; answer the prompt and the call completes on its own.",
	}, s.purgeSite)

	// gotifacts' tool set is compiled in and never changes at runtime, so the
	// SDK's default TTL of 0 ("immediately stale") makes every client re-list
	// the tools on each connection for no benefit. SEP-2549 cache hints live on
	// the result, which the SDK builds internally, so annotate it on the way
	// out. The scope stays "public": these descriptions are identical for every
	// caller and carry nothing user-specific.
	srv.AddReceivingMiddleware(toolListCacheHint)

	// go-sdk >= v1.7.0 bounds the Streamable HTTP request body, defaulting to
	// 4 MiB. That is far below the multipart ingest limit, so a publish_site
	// call carrying a multi-file site would be rejected with 413 long before
	// the ingest pipeline saw it. Align the two on GOTIFACTS_MAX_UPLOAD_BYTES;
	// note that base64-encoded files inflate ~4/3 on the JSON-RPC wire, so the
	// effective site size over MCP is correspondingly smaller.
	streamHandler := mcpsdk.NewStreamableHTTPHandler(
		func(*http.Request) *mcpsdk.Server { return srv },
		&mcpsdk.StreamableHTTPOptions{MaxRequestBodyBytes: cfg.MaxUploadBytes},
	)
	// No required scope at the middleware: a valid, unexpired token is admitted
	// and the per-capability/target check happens in the tool via Principal.Can.
	s.stream = mcpauth.RequireBearerToken(s.verifyToken, &mcpauth.RequireBearerTokenOptions{
		ResourceMetadataURL: cfg.BaseURL() + "/.well-known/oauth-protected-resource",
	})(streamHandler)

	return s, nil
}

// publishInput mirrors the publishable subset of ingest.Meta plus the content.
// Exactly one of HTML (single-page) or Files (multi-file site) must be set.
type publishInput struct {
	Slug        string        `json:"slug"`
	HTML        string        `json:"html,omitempty"`
	Files       []publishFile `json:"files,omitempty"`
	Title       string        `json:"title,omitempty"`
	Description string        `json:"description,omitempty"`
	Tags        []string      `json:"tags,omitempty"`
	Group       string        `json:"group,omitempty"`
}

// publishFile is one file of a multi-file site upload. Content is UTF-8 text by
// default; set Encoding to "base64" to carry binary assets (images, fonts).
type publishFile struct {
	Path     string `json:"path"`
	Content  string `json:"content"`
	Encoding string `json:"encoding,omitempty"`
}

// publishOutput is the structured result of a successful publish.
type publishOutput struct {
	URL   string `json:"url"`
	Group string `json:"group"`
	Slug  string `json:"slug"`
}

// publishSite is the MCP tool handler. It resolves the bearer principal from the
// validated token, enforces the token's capability grants against the target
// site, and runs the same publish path as POST /ingest/sites.
func (s *Service) publishSite(ctx context.Context, req *mcpsdk.CallToolRequest, in publishInput) (*mcpsdk.CallToolResult, publishOutput, error) {
	p := principalFromRequest(req)
	if p == nil {
		return errorResult("authentication required"), publishOutput{}, nil
	}
	log := s.reqLog(req)

	group := strings.TrimSpace(in.Group)
	if group == "" {
		group = s.cfg.MCPGroup
	}

	hasHTML := strings.TrimSpace(in.HTML) != ""
	hasFiles := len(in.Files) > 0
	if hasHTML == hasFiles {
		return errorResult("provide exactly one of `html` (single page) or `files` (multi-file site)"), publishOutput{}, nil
	}
	if !p.CanPublishTo(group, in.Slug) {
		return errorResult(fmt.Sprintf("this connection is not permitted to publish %q in group %q", in.Slug, group)), publishOutput{}, nil
	}

	kind := ingest.KindIndex
	var content io.Reader = strings.NewReader(in.HTML)
	if hasFiles {
		buf, err := buildBundle(in.Files, s.cfg.MaxUploadBytes)
		if err != nil {
			return errorResult(err.Error()), publishOutput{}, nil
		}
		kind, content = ingest.KindBundle, buf
	} else if int64(len(in.HTML)) > s.cfg.MaxUploadBytes {
		return errorResult(fmt.Sprintf("html exceeds the %d-byte upload limit", s.cfg.MaxUploadBytes)), publishOutput{}, nil
	}

	meta := ingest.Meta{
		Group:       group,
		Slug:        in.Slug,
		Title:       in.Title,
		Description: in.Description,
		Tags:        in.Tags,
	}
	res, _, err := s.pub.Publish(ctx, meta, kind, content)
	if err != nil {
		log.Warn("mcp publish failed", "user", p.User, "group", group, "slug", in.Slug, "err", err)
		return errorResult("publish failed: " + err.Error()), publishOutput{}, nil
	}
	log.Info("site published", "user", p.User, "source", "mcp", "group", res.Group, "slug", res.Slug, "url", res.URL)
	out := publishOutput{URL: res.URL, Group: res.Group, Slug: res.Slug}
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "Published to " + res.URL}},
	}, out, nil
}

// buildBundle converts a multi-file site input into an in-memory gzip-tar
// (ingest.KindBundle) payload. It validates each path (rejecting absolute paths
// and ".." traversal), decodes per-file encodings, caps the cumulative decoded
// size at maxBytes (mirroring the HTTP plane's MaxUploadBytes, which the direct
// Publish call would otherwise skip), and requires a top-level index.html — the
// same invariant the publish pipeline enforces after extraction, checked here so
// callers get a clear, early error.
func buildBundle(files []publishFile, maxBytes int64) (*bytes.Buffer, error) {
	named := make([]archive.NamedFile, 0, len(files))
	seen := make(map[string]bool, len(files))
	hasIndex := false
	var total int64
	for _, f := range files {
		norm := strings.ReplaceAll(f.Path, `\`, "/")
		if norm == "" || strings.HasPrefix(norm, "/") || path.IsAbs(norm) {
			return nil, fmt.Errorf("invalid file path %q", f.Path)
		}
		rel := path.Clean(norm)
		if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
			return nil, fmt.Errorf("invalid file path %q", f.Path)
		}
		if seen[rel] {
			return nil, fmt.Errorf("duplicate file path %q", rel)
		}
		seen[rel] = true
		if rel == "index.html" {
			hasIndex = true
		}
		data, err := decodeContent(f)
		if err != nil {
			return nil, err
		}
		total += int64(len(data))
		if total > maxBytes {
			return nil, fmt.Errorf("files exceed the %d-byte upload limit", maxBytes)
		}
		named = append(named, archive.NamedFile{Name: rel, Data: data})
	}
	if !hasIndex {
		return nil, fmt.Errorf("`files` must include a top-level index.html")
	}
	var buf bytes.Buffer
	if err := archive.WriteTarGz(&buf, named); err != nil {
		return nil, err
	}
	return &buf, nil
}

// decodeContent returns a file's raw bytes, honoring its encoding: UTF-8 text by
// default, or base64 for binary assets.
func decodeContent(f publishFile) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(f.Encoding)) {
	case "", "utf8", "utf-8", "text":
		return []byte(f.Content), nil
	case "base64":
		data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(f.Content))
		if err != nil {
			return nil, fmt.Errorf("file %q: invalid base64 content: %w", f.Path, err)
		}
		return data, nil
	default:
		return nil, fmt.Errorf("file %q: unknown encoding %q (use \"utf8\" or \"base64\")", f.Path, f.Encoding)
	}
}

// unpublishInput identifies the site to soft-delete.
type unpublishInput struct {
	Slug  string `json:"slug"`
	Group string `json:"group,omitempty"`
}

// unpublishSite is the MCP tool handler for soft-deleting a site.
func (s *Service) unpublishSite(ctx context.Context, req *mcpsdk.CallToolRequest, in unpublishInput) (*mcpsdk.CallToolResult, struct{}, error) {
	p := principalFromRequest(req)
	if p == nil {
		return errorResult("authentication required"), struct{}{}, nil
	}
	log := s.reqLog(req)
	group := strings.TrimSpace(in.Group)
	if group == "" {
		group = s.cfg.MCPGroup
	}
	if strings.TrimSpace(in.Slug) == "" {
		return errorResult("slug must not be empty"), struct{}{}, nil
	}
	if !p.Can(keys.CapUnpublish, group, in.Slug) {
		return errorResult(fmt.Sprintf("this connection is not permitted to unpublish %q in group %q", in.Slug, group)), struct{}{}, nil
	}
	if err := s.pub.Unpublish(ctx, group, in.Slug); err != nil {
		log.Warn("mcp unpublish failed", "user", p.User, "group", group, "slug", in.Slug, "err", err)
		return errorResult("unpublish failed: " + err.Error()), struct{}{}, nil
	}
	log.Info("site unpublished", "user", p.User, "source", "mcp", "group", group, "slug", in.Slug)
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: fmt.Sprintf("Site %q in group %q has been unpublished.", in.Slug, group)}},
	}, struct{}{}, nil
}

// updateInput carries the metadata fields that can be patched on an existing site.
type updateInput struct {
	Slug        string   `json:"slug"`
	Group       string   `json:"group,omitempty"`
	Title       *string  `json:"title,omitempty"`
	Description *string  `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Hidden      *bool    `json:"hidden,omitempty"`
}

// updateOutput is the structured result of a successful metadata update.
type updateOutput struct {
	Group string `json:"group"`
	Slug  string `json:"slug"`
}

// updateSite is the MCP tool handler for patching site metadata.
func (s *Service) updateSite(ctx context.Context, req *mcpsdk.CallToolRequest, in updateInput) (*mcpsdk.CallToolResult, updateOutput, error) {
	p := principalFromRequest(req)
	if p == nil {
		return errorResult("authentication required"), updateOutput{}, nil
	}
	log := s.reqLog(req)
	group := strings.TrimSpace(in.Group)
	if group == "" {
		group = s.cfg.MCPGroup
	}
	if strings.TrimSpace(in.Slug) == "" {
		return errorResult("slug must not be empty"), updateOutput{}, nil
	}
	if !p.Can(keys.CapPatch, group, in.Slug) {
		return errorResult(fmt.Sprintf("this connection is not permitted to update %q in group %q", in.Slug, group)), updateOutput{}, nil
	}
	patch := store.SitePatch{
		Title:       in.Title,
		Description: in.Description,
		Hidden:      in.Hidden,
	}
	if in.Tags != nil {
		patch.Tags = &in.Tags
	}
	if _, err := s.store.PatchSite(ctx, group, in.Slug, patch); err != nil {
		log.Warn("mcp update failed", "user", p.User, "group", group, "slug", in.Slug, "err", err)
		return errorResult("update failed: " + err.Error()), updateOutput{}, nil
	}
	log.Info("site metadata patched", "user", p.User, "source", "mcp", "group", group, "slug", in.Slug)
	out := updateOutput{Group: group, Slug: in.Slug}
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: fmt.Sprintf("Site %q in group %q has been updated.", in.Slug, group)}},
	}, out, nil
}

// listRevisionsInput identifies the site whose revisions to list.
type listRevisionsInput struct {
	Slug  string `json:"slug"`
	Group string `json:"group,omitempty"`
}

// revisionView is one revision in the list_revisions output.
type revisionView struct {
	// ID is "current" for the live content, otherwise the archive timestamp to
	// pass to rollback_site.
	ID string `json:"id"`
	// Current is true for the live content.
	Current bool `json:"current"`
	// CreatedAt is the revision's creation time in RFC3339.
	CreatedAt string `json:"created_at"`
}

// listRevisionsOutput is the structured result of list_revisions.
type listRevisionsOutput struct {
	Group     string         `json:"group"`
	Slug      string         `json:"slug"`
	Revisions []revisionView `json:"revisions"`
}

// listRevisions is the MCP tool handler that lists a site's revisions.
func (s *Service) listRevisions(_ context.Context, req *mcpsdk.CallToolRequest, in listRevisionsInput) (*mcpsdk.CallToolResult, listRevisionsOutput, error) {
	p := principalFromRequest(req)
	if p == nil {
		return errorResult("authentication required"), listRevisionsOutput{}, nil
	}
	log := s.reqLog(req)
	group := strings.TrimSpace(in.Group)
	if group == "" {
		group = s.cfg.MCPGroup
	}
	if strings.TrimSpace(in.Slug) == "" {
		return errorResult("slug must not be empty"), listRevisionsOutput{}, nil
	}
	if !p.Can(keys.CapRollback, group, in.Slug) {
		return errorResult(fmt.Sprintf("this connection is not permitted to view revisions of %q in group %q", in.Slug, group)), listRevisionsOutput{}, nil
	}
	sp, err := router.NewSitePath(group, in.Slug)
	if err != nil {
		return errorResult("invalid site path: " + err.Error()), listRevisionsOutput{}, nil
	}
	revs, err := s.pub.ListRevisions(sp)
	if err != nil {
		log.Warn("mcp list revisions failed", "user", p.User, "group", group, "slug", in.Slug, "err", err)
		return errorResult("failed to list revisions: " + err.Error()), listRevisionsOutput{}, nil
	}
	out := listRevisionsOutput{Group: group, Slug: in.Slug, Revisions: make([]revisionView, 0, len(revs))}
	var b strings.Builder
	fmt.Fprintf(&b, "Site %q in group %q has %d revision(s):\n", in.Slug, group, len(revs))
	for _, rv := range revs {
		out.Revisions = append(out.Revisions, revisionView{ID: rv.ID, Current: rv.Current, CreatedAt: rv.CreatedAt.Format(time.RFC3339)})
		label := "archived"
		if rv.Current {
			label = "current"
		}
		fmt.Fprintf(&b, "- %s (%s, %s)\n", rv.ID, label, rv.CreatedAt.Format(time.RFC3339))
	}
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: b.String()}},
	}, out, nil
}

// rollbackInput identifies the site to roll back, and optionally which revision
// to promote.
type rollbackInput struct {
	Slug  string `json:"slug"`
	Group string `json:"group,omitempty"`
	// Revision is the archive timestamp to promote. Empty restores the most
	// recent archived version.
	Revision string `json:"revision,omitempty"`
}

// rollbackOutput is the structured result of a successful rollback.
type rollbackOutput struct {
	Group string `json:"group"`
	Slug  string `json:"slug"`
}

// rollbackSite is the MCP tool handler for restoring a previous site version:
// the most recent archived version, or a specific revision when one is given.
func (s *Service) rollbackSite(ctx context.Context, req *mcpsdk.CallToolRequest, in rollbackInput) (*mcpsdk.CallToolResult, rollbackOutput, error) {
	p := principalFromRequest(req)
	if p == nil {
		return errorResult("authentication required"), rollbackOutput{}, nil
	}
	log := s.reqLog(req)
	group := strings.TrimSpace(in.Group)
	if group == "" {
		group = s.cfg.MCPGroup
	}
	if strings.TrimSpace(in.Slug) == "" {
		return errorResult("slug must not be empty"), rollbackOutput{}, nil
	}
	if !p.Can(keys.CapRollback, group, in.Slug) {
		return errorResult(fmt.Sprintf("this connection is not permitted to roll back %q in group %q", in.Slug, group)), rollbackOutput{}, nil
	}
	sp, err := router.NewSitePath(group, in.Slug)
	if err != nil {
		return errorResult("invalid site path: " + err.Error()), rollbackOutput{}, nil
	}
	rev := strings.TrimSpace(in.Revision)
	var rollErr error
	if rev != "" {
		rollErr = s.pub.RollbackTo(ctx, sp, rev)
	} else {
		rollErr = s.pub.Rollback(ctx, sp)
	}
	if rollErr != nil {
		log.Warn("mcp rollback failed", "user", p.User, "group", group, "slug", in.Slug, "revision", rev, "err", rollErr)
		return errorResult("rollback failed: " + rollErr.Error()), rollbackOutput{}, nil
	}
	log.Info("site rolled back", "user", p.User, "source", "mcp", "group", group, "slug", in.Slug, "revision", rev)
	out := rollbackOutput{Group: group, Slug: in.Slug}
	msg := fmt.Sprintf("Site %q in group %q has been rolled back to the previous version.", in.Slug, group)
	if rev != "" {
		msg = fmt.Sprintf("Site %q in group %q has been rolled back to revision %q.", in.Slug, group, rev)
	}
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: msg}},
	}, out, nil
}

// restoreInput identifies the soft-deleted site to restore.
type restoreInput struct {
	Slug  string `json:"slug"`
	Group string `json:"group,omitempty"`
}

// restoreSite is the MCP tool handler for restoring a soft-deleted site.
func (s *Service) restoreSite(ctx context.Context, req *mcpsdk.CallToolRequest, in restoreInput) (*mcpsdk.CallToolResult, struct{}, error) {
	p := principalFromRequest(req)
	if p == nil {
		return errorResult("authentication required"), struct{}{}, nil
	}
	log := s.reqLog(req)
	group := strings.TrimSpace(in.Group)
	if group == "" {
		group = s.cfg.MCPGroup
	}
	if strings.TrimSpace(in.Slug) == "" {
		return errorResult("slug must not be empty"), struct{}{}, nil
	}
	if !p.Can(keys.CapPublish, group, in.Slug) {
		return errorResult(fmt.Sprintf("this connection is not permitted to restore %q in group %q", in.Slug, group)), struct{}{}, nil
	}
	if err := s.pub.Restore(ctx, group, in.Slug); err != nil {
		log.Warn("mcp restore failed", "user", p.User, "group", group, "slug", in.Slug, "err", err)
		return errorResult("restore failed: " + err.Error()), struct{}{}, nil
	}
	log.Info("site restored", "user", p.User, "source", "mcp", "group", group, "slug", in.Slug)
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: fmt.Sprintf("Site %q in group %q has been restored.", in.Slug, group)}},
	}, struct{}{}, nil
}

// purgeInput identifies the soft-deleted site to permanently destroy.
type purgeInput struct {
	Slug  string `json:"slug"`
	Group string `json:"group,omitempty"`
}

// purgeSite is the MCP tool handler for permanently deleting a quarantined site.
func (s *Service) purgeSite(ctx context.Context, req *mcpsdk.CallToolRequest, in purgeInput) (*mcpsdk.CallToolResult, struct{}, error) {
	p := principalFromRequest(req)
	if p == nil {
		return errorResult("authentication required"), struct{}{}, nil
	}
	log := s.reqLog(req)
	group := strings.TrimSpace(in.Group)
	if group == "" {
		group = s.cfg.MCPGroup
	}
	if strings.TrimSpace(in.Slug) == "" {
		return errorResult("slug must not be empty"), struct{}{}, nil
	}
	if !p.Can(keys.CapPurge, group, in.Slug) {
		return errorResult(fmt.Sprintf("this connection is not permitted to purge %q in group %q", in.Slug, group)), struct{}{}, nil
	}
	if res, done := s.confirmPurge(req, p, group, in.Slug, log); done {
		return res, struct{}{}, nil
	}
	if err := s.pub.Purge(ctx, group, in.Slug); err != nil {
		log.Warn("mcp purge failed", "user", p.User, "group", group, "slug", in.Slug, "err", err)
		return errorResult("purge failed: " + err.Error()), struct{}{}, nil
	}
	log.Info("site purged", "user", p.User, "source", "mcp", "group", group, "slug", in.Slug)
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: fmt.Sprintf("Site %q in group %q has been permanently deleted.", in.Slug, group)}},
	}, struct{}{}, nil
}

// confirmPurge is the human-in-the-loop gate in front of an irreversible purge.
//
// Rather than deleting on the first call, it returns an SEP-2322 input request:
// the client elicits the user's answer and retries the same tool call with the
// response attached, and the SDK's compatibility middleware turns that into a
// direct elicitation for clients still on older protocol versions, so this code
// is protocol-version-independent. A client that cannot elicit at all would
// fail the call outright instead of being asked, so the gate is skipped unless
// the client advertised form elicitation — purge stays reachable for the
// scripted API-key path, which never had a prompt to begin with.
//
// The second return reports whether the caller should stop and return res; a
// false means the purge was confirmed and should proceed.
func (s *Service) confirmPurge(req *mcpsdk.CallToolRequest, p *auth.Principal, group, slug string, log *slog.Logger) (*mcpsdk.CallToolResult, bool) {
	if req.Params == nil || !clientCanElicitForms(req.ClientCapabilities()) {
		return nil, false
	}
	answer, answered := req.Params.InputResponses[purgeConfirmID]
	if !answered {
		return &mcpsdk.CallToolResult{
			InputRequests: mcpsdk.InputRequestMap{purgeConfirmID: &mcpsdk.ElicitParams{
				Message: fmt.Sprintf(
					"Permanently delete site %q in group %q? Its files are destroyed immediately and cannot be recovered.",
					slug, group),
			}},
			RequestState: s.purgeToken(p.User, group, slug, time.Now().Add(purgeConfirmTTL)),
		}, true
	}
	// The client chooses what to echo back, so re-derive consent from the
	// signed state rather than trusting that an answer belongs to this target:
	// a confirmation for one site must not authorize purging another.
	if !s.purgeTokenValid(req.Params.RequestState, p.User, group, slug) {
		log.Warn("mcp purge confirmation rejected", "user", p.User, "group", group, "slug", slug)
		return errorResult("purge confirmation is invalid or has expired; call purge_site again to retry"), true
	}
	if res, ok := answer.(*mcpsdk.ElicitResult); !ok || res.Action != "accept" {
		log.Info("site purge declined", "user", p.User, "source", "mcp", "group", group, "slug", slug)
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: fmt.Sprintf(
				"Purge cancelled: %q in group %q was not deleted and is still recoverable.", slug, group)}},
		}, true
	}
	return nil, false
}

// clientCanElicitForms reports whether the connected client can answer a form
// elicitation. Capabilities with neither sub-field set mean a pre-SEP-1036
// client, which the SDK treats as form-capable for backward compatibility.
func clientCanElicitForms(caps *mcpsdk.ClientCapabilities) bool {
	if caps == nil || caps.Elicitation == nil {
		return false
	}
	return caps.Elicitation.Form != nil || caps.Elicitation.URL == nil
}

// purgeToken mints the opaque RequestState carried through a purge
// confirmation. It binds the answer to the principal, the exact target and an
// expiry, all authenticated with the service HMAC key so a client cannot mint
// or retarget one. It is not a capability: purgeSite re-checks CapPurge on the
// retry regardless.
func (s *Service) purgeToken(user, group, slug string, exp time.Time) string {
	unix := strconv.FormatInt(exp.Unix(), 10)
	mac := hmac.New(sha256.New, s.csrfKey)
	// NUL separators keep the fields unambiguous, so that a slug containing the
	// separator cannot be split to impersonate a different group.
	fmt.Fprintf(mac, "purge\x00%s\x00%s\x00%s\x00%s", user, group, slug, unix)
	return unix + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// purgeTokenValid reports whether token is a live purgeToken for this exact
// principal and target.
func (s *Service) purgeTokenValid(token, user, group, slug string) bool {
	unix, _, ok := strings.Cut(token, ".")
	if !ok {
		return false
	}
	exp, err := strconv.ParseInt(unix, 10, 64)
	if err != nil || time.Now().After(time.Unix(exp, 0)) {
		return false
	}
	expected := s.purgeToken(user, group, slug, time.Unix(exp, 0))
	return subtle.ConstantTimeCompare([]byte(expected), []byte(token)) == 1
}

// toolListCacheHint stamps a TTL on tools/list results. See the call site in
// New for why the static tool set is safe to cache.
func toolListCacheHint(next mcpsdk.MethodHandler) mcpsdk.MethodHandler {
	return func(ctx context.Context, method string, req mcpsdk.Request) (mcpsdk.Result, error) {
		res, err := next(ctx, method, req)
		if err != nil {
			return res, err
		}
		if lt, ok := res.(*mcpsdk.ListToolsResult); ok {
			lt.TTLMs = int(toolListTTL.Milliseconds())
			lt.CacheScope = "public"
		}
		return res, nil
	}
}

// reqLog returns the service logger annotated with the MCP client behind a tool
// call. ClientInfo and ProtocolVersion read the per-request `_meta` on protocol
// version 2026-07-28 and up, falling back to the initialize handshake for older
// sessions, so the audit trail records which connector published a site rather
// than just which user did.
func (s *Service) reqLog(req *mcpsdk.CallToolRequest) *slog.Logger {
	if req == nil {
		return s.log
	}
	log := s.log
	if info := req.ClientInfo(); info != nil && info.Name != "" {
		log = log.With("client", info.Name)
		if info.Version != "" {
			log = log.With("client_version", info.Version)
		}
	}
	if v := req.ProtocolVersion(); v != "" {
		log = log.With("mcp_protocol", v)
	}
	return log
}

// principalFromRequest extracts the *auth.Principal that verifyToken stashed in
// the bearer token's Extra map.
func principalFromRequest(req *mcpsdk.CallToolRequest) *auth.Principal {
	if req == nil || req.Extra == nil || req.Extra.TokenInfo == nil {
		return nil
	}
	p, _ := req.Extra.TokenInfo.Extra["principal"].(*auth.Principal)
	return p
}

// verifyToken is the bearer TokenVerifier for the MCP endpoint. It resolves an
// opaque access token to an API-key-equivalent Principal carrying the token's
// grants, which the tool handler authorizes against the target site.
func (s *Service) verifyToken(ctx context.Context, token string, _ *http.Request) (*mcpauth.TokenInfo, error) {
	rec, err := s.store.FindToken(ctx, "access", keys.Hash(token))
	if err != nil {
		return nil, mcpauth.ErrInvalidToken
	}
	s.store.TouchToken(ctx, rec.Hash)
	p := &auth.Principal{
		User:   rec.User,
		Grants: rec.Grants,
		Source: auth.SourceAPIKey,
	}
	return &mcpauth.TokenInfo{
		Scopes:     []string{scopePublish},
		Expiration: rec.ExpiresAt,
		UserID:     rec.User,
		Extra:      map[string]any{"principal": p},
	}, nil
}

// errorResult builds a tool result flagged as an error with a text message.
func errorResult(msg string) *mcpsdk.CallToolResult {
	return &mcpsdk.CallToolResult{
		IsError: true,
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: msg}},
	}
}

// randToken returns a new opaque token and its hex SHA-256 hash.
func randToken() (token, hash string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, keys.Hash(token), nil
}

// randID returns a new opaque client identifier.
func randID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "mcp-" + base64.RawURLEncoding.EncodeToString(b), nil
}

// randConnID returns a new opaque connection identifier.
func randConnID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
