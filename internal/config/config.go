// Package config loads and validates the gotifacts runtime configuration.
//
// Configuration is sourced exclusively from environment variables (no config
// file), populated into a [Config] struct with sane defaults, and validated via
// [Config.Validate].
package config

import (
	"fmt"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
)

// Default values applied when the corresponding environment variable is unset.
const (
	DefaultListenAddr        = ":8080"
	DefaultDataDir           = "/data"
	DefaultForwardAuthHeader = "Remote-User"
	DefaultMaxUploadBytes    = 64 << 20  // 64 MiB
	DefaultMaxExtractBytes   = 256 << 20 // 256 MiB
	DefaultMaxExtractEntries = 10000
	DefaultVersioningKeep    = 5
)

// Config holds the fully-resolved runtime configuration.
type Config struct {
	// ListenAddr is the TCP address the HTTP server binds to.
	ListenAddr string
	// DataDir is the writable volume root holding the DB and site files.
	DataDir string
	// DBPath is the SQLite database file path.
	DBPath string
	// BaseDomain is the apex domain; sites live on sub-labels of it. Required.
	BaseDomain string
	// ForwardAuthHeader is the request header carrying the proxy-asserted user.
	ForwardAuthHeader string
	// AdminUsers are forward-auth principals granted admin scope.
	AdminUsers []string
	// TrustedProxies are CIDRs whose source IPs may assert the auth header.
	TrustedProxies []netip.Prefix
	// MaxUploadBytes caps the size of an ingest multipart request body.
	MaxUploadBytes int64
	// MaxExtractBytes caps total decompressed bytes from an archive.
	MaxExtractBytes int64
	// MaxExtractEntries caps the number of entries extracted from an archive.
	MaxExtractEntries int
	// VersioningEnabled retains prior site versions on replace and enables rollback.
	VersioningEnabled bool
	// VersioningKeep is the number of historical versions retained per site.
	VersioningKeep int
}

// SitesDir returns the directory holding served site content.
func (c *Config) SitesDir() string { return c.DataDir + "/sites" }

// TmpDir returns the staging directory on the same volume as SitesDir.
func (c *Config) TmpDir() string { return c.DataDir + "/.tmp" }

// VersionsDir returns the directory holding retained site versions.
func (c *Config) VersionsDir() string { return c.DataDir + "/.versions" }

// IsAdminUser reports whether user is in the admin allowlist (case-insensitive).
func (c *Config) IsAdminUser(user string) bool {
	if user == "" {
		return false
	}
	for _, u := range c.AdminUsers {
		if strings.EqualFold(u, user) {
			return true
		}
	}
	return false
}

// TrustsAddr reports whether addr is within a trusted-proxy CIDR.
func (c *Config) TrustsAddr(addr netip.Addr) bool {
	addr = addr.Unmap()
	for _, p := range c.TrustedProxies {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// Load builds a Config from the process environment, applying defaults.
func Load() (*Config, error) {
	c := &Config{
		ListenAddr:        envOr("GOTIFACTS_LISTEN_ADDR", DefaultListenAddr),
		DataDir:           envOr("GOTIFACTS_DATA_DIR", DefaultDataDir),
		BaseDomain:        strings.ToLower(strings.TrimSpace(os.Getenv("GOTIFACTS_BASE_DOMAIN"))),
		ForwardAuthHeader: envOr("GOTIFACTS_FORWARD_AUTH_HEADER", DefaultForwardAuthHeader),
		AdminUsers:        splitList(os.Getenv("GOTIFACTS_ADMIN_USERS")),
		MaxUploadBytes:    DefaultMaxUploadBytes,
		MaxExtractBytes:   DefaultMaxExtractBytes,
		MaxExtractEntries: DefaultMaxExtractEntries,
		VersioningKeep:    DefaultVersioningKeep,
	}
	c.DBPath = envOr("GOTIFACTS_DB_PATH", c.DataDir+"/gotifacts.db")

	var err error
	if c.TrustedProxies, err = parseCIDRs(os.Getenv("GOTIFACTS_TRUSTED_PROXIES")); err != nil {
		return nil, fmt.Errorf("GOTIFACTS_TRUSTED_PROXIES: %w", err)
	}
	if c.MaxUploadBytes, err = envInt64("GOTIFACTS_MAX_UPLOAD_BYTES", DefaultMaxUploadBytes); err != nil {
		return nil, err
	}
	if c.MaxExtractBytes, err = envInt64("GOTIFACTS_MAX_EXTRACT_BYTES", DefaultMaxExtractBytes); err != nil {
		return nil, err
	}
	if c.MaxExtractEntries, err = envInt("GOTIFACTS_MAX_EXTRACT_ENTRIES", DefaultMaxExtractEntries); err != nil {
		return nil, err
	}
	if c.VersioningKeep, err = envInt("GOTIFACTS_VERSIONING_KEEP", DefaultVersioningKeep); err != nil {
		return nil, err
	}
	if c.VersioningEnabled, err = envBool("GOTIFACTS_VERSIONING_ENABLED", false); err != nil {
		return nil, err
	}
	return c, nil
}

// Validate returns all configuration errors, or nil if the config is usable.
func (c *Config) Validate() []error {
	var errs []error
	if c.BaseDomain == "" {
		errs = append(errs, fmt.Errorf("GOTIFACTS_BASE_DOMAIN is required"))
	} else if strings.HasPrefix(c.BaseDomain, ".") || strings.Contains(c.BaseDomain, "..") {
		errs = append(errs, fmt.Errorf("GOTIFACTS_BASE_DOMAIN %q is malformed", c.BaseDomain))
	}
	if c.ListenAddr == "" {
		errs = append(errs, fmt.Errorf("GOTIFACTS_LISTEN_ADDR must not be empty"))
	}
	if c.DataDir == "" {
		errs = append(errs, fmt.Errorf("GOTIFACTS_DATA_DIR must not be empty"))
	}
	if c.ForwardAuthHeader == "" {
		errs = append(errs, fmt.Errorf("GOTIFACTS_FORWARD_AUTH_HEADER must not be empty"))
	}
	if c.MaxUploadBytes <= 0 {
		errs = append(errs, fmt.Errorf("GOTIFACTS_MAX_UPLOAD_BYTES must be positive"))
	}
	if c.MaxExtractBytes <= 0 {
		errs = append(errs, fmt.Errorf("GOTIFACTS_MAX_EXTRACT_BYTES must be positive"))
	}
	if c.MaxExtractEntries <= 0 {
		errs = append(errs, fmt.Errorf("GOTIFACTS_MAX_EXTRACT_ENTRIES must be positive"))
	}
	if c.VersioningEnabled && c.VersioningKeep <= 0 {
		errs = append(errs, fmt.Errorf("GOTIFACTS_VERSIONING_KEEP must be positive when versioning is enabled"))
	}
	if len(c.AdminUsers) == 0 && len(c.TrustedProxies) == 0 {
		errs = append(errs, fmt.Errorf("no admins reachable: set GOTIFACTS_ADMIN_USERS and GOTIFACTS_TRUSTED_PROXIES, or create an admin key via the CLI"))
	}
	return errs
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func splitList(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parseCIDRs(v string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, part := range splitList(v) {
		// Accept bare IPs as well as CIDRs.
		if !strings.Contains(part, "/") {
			addr, err := netip.ParseAddr(part)
			if err != nil {
				return nil, fmt.Errorf("invalid address %q: %w", part, err)
			}
			out = append(out, netip.PrefixFrom(addr.Unmap(), addr.BitLen()))
			continue
		}
		p, err := netip.ParsePrefix(part)
		if err != nil {
			// Tolerate host bits being set (e.g. 10.0.0.5/24).
			if _, ipnet, e2 := net.ParseCIDR(part); e2 == nil {
				masked, _ := netip.AddrFromSlice(ipnet.IP)
				ones, _ := ipnet.Mask.Size()
				out = append(out, netip.PrefixFrom(masked.Unmap(), ones))
				continue
			}
			return nil, fmt.Errorf("invalid CIDR %q: %w", part, err)
		}
		out = append(out, p.Masked())
	}
	return out, nil
}

func envInt64(key string, def int64) (int64, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return n, nil
}

func envInt(key string, def int) (int, error) {
	n, err := envInt64(key, int64(def))
	return int(n), err
}

func envBool(key string, def bool) (bool, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s: %w", key, err)
	}
	return b, nil
}
