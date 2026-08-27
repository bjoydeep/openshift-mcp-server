package acmsearch

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/containers/kubernetes-mcp-server/pkg/api"
	"github.com/containers/kubernetes-mcp-server/pkg/config"
	searchauth "github.com/stolostron/search-mcp-server/pkg/auth"
)

// Config holds ACM search toolset configuration.
type Config struct {
	// DatabaseURL is the PostgreSQL connection string for the ACM search database.
	// Required. Example: "postgres://user:pass@host:5432/search"
	DatabaseURL string `toml:"database_url"`

	// EnableAuth enables ACM RBAC enforcement.
	// Defaults to true when KUBERNETES_SERVICE_HOST is set (in-cluster), false otherwise.
	// Set explicitly to override the auto-detection.
	EnableAuth *bool `toml:"enable_auth,omitempty"`

	// ServiceAccountTokenPath is the path to the server's service account token,
	// used to call the Kubernetes TokenReview API when validating user credentials.
	// Defaults to "/var/run/secrets/kubernetes.io/serviceaccount/token".
	ServiceAccountTokenPath string `toml:"service_account_token_path,omitempty"`

	// KubernetesURL is the Kubernetes API server URL.
	// Optional; auto-detected from KUBERNETES_SERVICE_HOST/PORT when running in-cluster.
	KubernetesURL string `toml:"kubernetes_url,omitempty"`

	// SkipTLSVerify disables TLS certificate verification for Kubernetes API calls.
	// Use only in development environments.
	SkipTLSVerify bool `toml:"skip_tls_verify,omitempty"`
}

var _ api.ExtendedConfig = (*Config)(nil)

func (c *Config) Validate() error {
	if c.DatabaseURL == "" {
		return fmt.Errorf("acm-search toolset requires database_url")
	}
	return nil
}

// resolveEnableAuth returns the effective enable_auth value:
// explicit config takes precedence; otherwise auto-detects from KUBERNETES_SERVICE_HOST.
func (c *Config) resolveEnableAuth() bool {
	if c.EnableAuth != nil {
		return *c.EnableAuth
	}
	return os.Getenv("KUBERNETES_SERVICE_HOST") != ""
}

// toAuthConfig builds an auth.AuthConfig for token validation and RBAC resolution.
func (c *Config) toAuthConfig() *searchauth.AuthConfig {
	saTokenPath := c.ServiceAccountTokenPath
	if saTokenPath == "" {
		saTokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	}
	return searchauth.NewAuthConfigFromServerValues(
		c.resolveEnableAuth(),
		30*time.Second,
		true,
		5*time.Minute,
		c.KubernetesURL,
		"",
		saTokenPath,
		"",
		c.SkipTLSVerify,
		5*time.Minute,
		"database",
	)
}

func acmSearchConfigParser(_ context.Context, primitive toml.Primitive, md toml.MetaData) (api.ExtendedConfig, error) {
	var cfg Config
	if err := md.PrimitiveDecode(primitive, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func init() {
	config.RegisterToolsetConfig("acm-search", acmSearchConfigParser)
}
