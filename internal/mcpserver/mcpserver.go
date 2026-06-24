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
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

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
		Description: "Publish a self-contained HTML page to this gotifacts instance and " +
			"return its public URL. Provide the full standalone HTML document (inline " +
			"CSS/JS) as `html` and a URL-safe `slug`. Re-publishing the same slug replaces " +
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
		Description: "Restore the most recent archived version of a site, replacing the current live content. Requires versioning to be enabled on the gotifacts instance.",
	}, s.rollbackSite)
	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "restore_site",
		Description: "Bring a soft-deleted (unpublished) site back online by moving its quarantined files back to live and clearing its deleted status. Use this to undo an accidental unpublish within the grace period.",
	}, s.restoreSite)
	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "purge_site",
		Description: "Permanently and immediately delete a soft-deleted (quarantined) site, bypassing the retention TTL. This is irreversible — the site's files are destroyed. Only use when you are certain the site should be gone.",
	}, s.purgeSite)

	streamHandler := mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server { return srv }, nil)
	// No required scope at the middleware: a valid, unexpired token is admitted
	// and the per-capability/target check happens in the tool via Principal.Can.
	s.stream = mcpauth.RequireBearerToken(s.verifyToken, &mcpauth.RequireBearerTokenOptions{
		ResourceMetadataURL: cfg.BaseURL() + "/.well-known/oauth-protected-resource",
	})(streamHandler)

	return s, nil
}

// publishInput mirrors the publishable subset of ingest.Meta plus the HTML body.
type publishInput struct {
	Slug        string   `json:"slug"`
	HTML        string   `json:"html"`
	Title       string   `json:"title,omitempty"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Group       string   `json:"group,omitempty"`
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

	group := strings.TrimSpace(in.Group)
	if group == "" {
		group = s.cfg.MCPGroup
	}
	if strings.TrimSpace(in.HTML) == "" {
		return errorResult("html must not be empty"), publishOutput{}, nil
	}
	if !p.CanPublishTo(group, in.Slug) {
		return errorResult(fmt.Sprintf("this connection is not permitted to publish %q in group %q", in.Slug, group)), publishOutput{}, nil
	}

	meta := ingest.Meta{
		Group:       group,
		Slug:        in.Slug,
		Title:       in.Title,
		Description: in.Description,
		Tags:        in.Tags,
	}
	res, _, err := s.pub.Publish(ctx, meta, ingest.KindIndex, strings.NewReader(in.HTML))
	if err != nil {
		s.log.Warn("mcp publish failed", "user", p.User, "group", group, "slug", in.Slug, "err", err)
		return errorResult("publish failed: " + err.Error()), publishOutput{}, nil
	}
	s.log.Info("site published", "user", p.User, "source", "mcp", "group", res.Group, "slug", res.Slug, "url", res.URL)
	out := publishOutput{URL: res.URL, Group: res.Group, Slug: res.Slug}
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "Published to " + res.URL}},
	}, out, nil
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
		s.log.Warn("mcp unpublish failed", "user", p.User, "group", group, "slug", in.Slug, "err", err)
		return errorResult("unpublish failed: " + err.Error()), struct{}{}, nil
	}
	s.log.Info("site unpublished", "user", p.User, "source", "mcp", "group", group, "slug", in.Slug)
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
		s.log.Warn("mcp update failed", "user", p.User, "group", group, "slug", in.Slug, "err", err)
		return errorResult("update failed: " + err.Error()), updateOutput{}, nil
	}
	s.log.Info("site metadata patched", "user", p.User, "source", "mcp", "group", group, "slug", in.Slug)
	out := updateOutput{Group: group, Slug: in.Slug}
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: fmt.Sprintf("Site %q in group %q has been updated.", in.Slug, group)}},
	}, out, nil
}

// rollbackInput identifies the site to roll back.
type rollbackInput struct {
	Slug  string `json:"slug"`
	Group string `json:"group,omitempty"`
}

// rollbackOutput is the structured result of a successful rollback.
type rollbackOutput struct {
	Group string `json:"group"`
	Slug  string `json:"slug"`
}

// rollbackSite is the MCP tool handler for restoring the previous site version.
func (s *Service) rollbackSite(ctx context.Context, req *mcpsdk.CallToolRequest, in rollbackInput) (*mcpsdk.CallToolResult, rollbackOutput, error) {
	p := principalFromRequest(req)
	if p == nil {
		return errorResult("authentication required"), rollbackOutput{}, nil
	}
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
	if err := s.pub.Rollback(ctx, sp); err != nil {
		s.log.Warn("mcp rollback failed", "user", p.User, "group", group, "slug", in.Slug, "err", err)
		return errorResult("rollback failed: " + err.Error()), rollbackOutput{}, nil
	}
	s.log.Info("site rolled back", "user", p.User, "source", "mcp", "group", group, "slug", in.Slug)
	out := rollbackOutput{Group: group, Slug: in.Slug}
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: fmt.Sprintf("Site %q in group %q has been rolled back to the previous version.", in.Slug, group)}},
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
		s.log.Warn("mcp restore failed", "user", p.User, "group", group, "slug", in.Slug, "err", err)
		return errorResult("restore failed: " + err.Error()), struct{}{}, nil
	}
	s.log.Info("site restored", "user", p.User, "source", "mcp", "group", group, "slug", in.Slug)
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
	if err := s.pub.Purge(ctx, group, in.Slug); err != nil {
		s.log.Warn("mcp purge failed", "user", p.User, "group", group, "slug", in.Slug, "err", err)
		return errorResult("purge failed: " + err.Error()), struct{}{}, nil
	}
	s.log.Info("site purged", "user", p.User, "source", "mcp", "group", group, "slug", in.Slug)
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: fmt.Sprintf("Site %q in group %q has been permanently deleted.", in.Slug, group)}},
	}, struct{}{}, nil
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
