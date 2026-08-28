package acmsearch

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/google/jsonschema-go/jsonschema"
	"k8s.io/utils/ptr"

	"github.com/containers/kubernetes-mcp-server/pkg/api"
	searchauth "github.com/stolostron/search-mcp-server/pkg/auth"
	searchconfig "github.com/stolostron/search-mcp-server/pkg/config"
	"github.com/stolostron/search-mcp-server/pkg/database"
	"github.com/stolostron/search-mcp-server/pkg/findresources"
)

// acmManager holds long-lived resources initialized once per server lifetime.
type acmManager struct {
	dbConn    *database.DatabaseConnection
	dbQueries *database.DatabaseQueries
	validator *searchauth.KubernetesValidator
	resolver  *searchauth.RBACResolver
}

var (
	mgrOnce sync.Once
	mgrInst *acmManager
	mgrErr  error
)

func getOrInitManager(ctx context.Context, cfg *Config) (*acmManager, error) {
	mgrOnce.Do(func() {
		mgrInst, mgrErr = initManager(ctx, cfg)
	})
	return mgrInst, mgrErr
}

func initManager(ctx context.Context, cfg *Config) (*acmManager, error) {
	dbCfg := searchconfig.DefaultConfig()
	dbCfg.ConnectionString = cfg.DatabaseURL

	dbConn, err := database.NewDatabaseConnectionWithConfig(ctx, dbCfg)
	if err != nil {
		return nil, fmt.Errorf("ACM search DB connection failed: %w", err)
	}
	dbQueries := database.NewDatabaseQueries(dbConn)

	var validator *searchauth.KubernetesValidator
	var resolver *searchauth.RBACResolver

	if cfg.resolveEnableAuth() {
		authCfg := cfg.toAuthConfig()
		k8sCfg, err := authCfg.GetKubernetesConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to build K8s config for ACM search auth: %w", err)
		}
		validator = searchauth.NewKubernetesValidator(k8sCfg)
		resolver = searchauth.NewRBACResolver(authCfg, dbConn)
	}

	return &acmManager{
		dbConn:    dbConn,
		dbQueries: dbQueries,
		validator: validator,
		resolver:  resolver,
	}, nil
}

func initFindResources() []api.ServerTool {
	return []api.ServerTool{
		{
			Tool: api.Tool{
				Name:        "acm_find_resources",
				Description: "Find and analyze Kubernetes resources across all ACM managed clusters with filtering, counting, and health analysis",
				Annotations: api.ToolAnnotations{
					Title:         "ACM: Find Resources",
					ReadOnlyHint:  ptr.To(true),
					OpenWorldHint: ptr.To(true),
				},
				InputSchema: &jsonschema.Schema{
					Type: "object",
					Properties: map[string]*jsonschema.Schema{
						"kind": {
							Type:        "string",
							Description: "Resource kind(s) to search for. Single kind (e.g. \"Pod\") or comma-separated (e.g. \"Pod,ConfigMap,Service\")",
						},
						"name": {
							Type:        "string",
							Description: "Resource name or wildcard pattern, e.g. \"nginx*\"",
						},
						"namespace": {
							Type:        "string",
							Description: "Namespace(s) to search in. Single (e.g. \"default\") or comma-separated (e.g. \"default,kube-system\")",
						},
						"cluster": {
							Type:        "string",
							Description: "Target cluster(s) to search in. Single (e.g. \"prod-cluster\") or comma-separated (e.g. \"prod,staging\")",
						},
						"labelSelector": {
							Type:        "string",
							Description: "Kubernetes label selector, e.g. \"app=nginx,env!=test\"",
						},
						"clusterSelector": {
							Type:        "string",
							Description: "Filter clusters by labels, e.g. \"env=prod,cloud=AWS\"",
						},
						"status": {
							Type:        "string",
							Description: "Resource status(es) to filter by. Single (e.g. \"Running\") or comma-separated (e.g. \"Failed,Pending\")",
						},
						"textSearch": {
							Type:        "string",
							Description: "Full-text search across all resource fields",
						},
						"ageNewerThan": {
							Type:        "string",
							Description: "Return resources created within this duration, e.g. \"1h\", \"2d\", \"1w\"",
						},
						"ageOlderThan": {
							Type:        "string",
							Description: "Return resources older than this duration, e.g. \"1h\", \"2d\", \"1w\"",
						},
						"outputMode": {
							Type:        "string",
							Description: "Output format: \"list\" (default), \"count\", \"summary\", or \"health\"",
							Enum:        []any{"list", "count", "summary", "health"},
						},
						"groupBy": {
							Type:        "string",
							Description: "Group results by: \"status\", \"namespace\", \"cluster\", \"kind\", or \"label:<key>\"",
						},
						"limit": {
							Type:        "integer",
							Description: "Maximum number of results to return (default 50, max 1000)",
						},
						"sortBy": {
							Type:        "string",
							Description: "Sort field: \"name\", \"created\", \"namespace\", or \"cluster\"",
						},
						"sortOrder": {
							Type:        "string",
							Description: "Sort order: \"asc\" (default) or \"desc\"",
							Enum:        []any{"asc", "desc"},
						},
					},
				},
			},
			Handler: findResourcesHandler,
		},
	}
}

func findResourcesHandler(params api.ToolHandlerParams) (*api.ToolCallResult, error) {
	ctx := params.Context

	cfg, ok := getConfig(params)
	if !ok {
		return api.NewToolCallResult("", fmt.Errorf("acm-search toolset is not configured: add [toolset_configs.acm-search] with database_url to your config")), nil
	}

	mgr, err := getOrInitManager(ctx, cfg)
	if err != nil {
		return api.NewToolCallResult("", fmt.Errorf("acm-search initialization failed: %w", err)), nil
	}

	userCtx, err := buildUserContext(ctx, params, mgr, cfg)
	if err != nil {
		return api.NewToolCallResult("", fmt.Errorf("acm-search auth failed: %w", err)), nil
	}

	args := parseArgs(params.GetArguments())

	core := findresources.NewFindResourcesCore(mgr.dbQueries)
	result, err := core.FindResources(ctx, args, userCtx)
	if err != nil {
		return api.NewToolCallResult("", fmt.Errorf("acm-search query failed: %w", err)), nil
	}

	out, err := json.Marshal(result)
	if err != nil {
		return api.NewToolCallResult("", fmt.Errorf("failed to marshal acm-search result: %w", err)), nil
	}
	return api.NewToolCallResultFull(string(out), result, nil), nil
}

// getConfig retrieves the toolset config from params, returning false if not configured.
func getConfig(params api.ToolHandlerParams) (*Config, bool) {
	raw, ok := params.GetToolsetConfig("acm-search")
	if !ok {
		return nil, false
	}
	cfg, ok := raw.(*Config)
	return cfg, ok
}

// buildUserContext validates the caller's bearer token and resolves ACM RBAC permissions.
// Returns nil when auth is disabled (unrestricted access — suitable for local dev only).
func buildUserContext(ctx context.Context, params api.ToolHandlerParams, mgr *acmManager, cfg *Config) (*searchauth.UserContext, error) {
	if !cfg.resolveEnableAuth() {
		return nil, nil
	}

	token := params.RESTConfig().BearerToken
	if token == "" {
		return nil, fmt.Errorf("no bearer token available; ensure cluster_auth = \"passthrough\" is set")
	}

	validationResult, err := mgr.validator.ValidateBearerToken(ctx, "Bearer "+token)
	if err != nil {
		return nil, fmt.Errorf("token validation failed: %w", err)
	}
	if !validationResult.Valid {
		return nil, fmt.Errorf("token is not valid: %s", validationResult.Error)
	}
	userCtx := validationResult.User

	queryFilters, err := mgr.resolver.ResolveUserPermissions(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("RBAC resolution failed: %w", err)
	}
	userCtx.QueryFilters = queryFilters

	return userCtx, nil
}

// toStringOrSlice normalises a raw MCP value for polymorphic string-or-array fields.
// JSON unmarshaling into map[string]any produces []interface{} for arrays, but the
// findresources core only understands string and []string, so we convert here.
func toStringOrSlice(v any) any {
	switch val := v.(type) {
	case string:
		return val
	case []string:
		return val
	case []any:
		result := make([]string, 0, len(val))
		for _, item := range val {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result
	}
	return nil
}

// parseArgs maps raw MCP tool arguments to findresources.FindResourcesArgs.
func parseArgs(raw map[string]any) findresources.FindResourcesArgs {
	args := findresources.FindResourcesArgs{}

	if v, ok := raw["kind"]; ok {
		args.Kind = toStringOrSlice(v)
	}
	if v, ok := raw["name"].(string); ok {
		args.Name = v
	}
	if v, ok := raw["namespace"]; ok {
		args.Namespace = toStringOrSlice(v)
	}
	if v, ok := raw["cluster"]; ok {
		args.Cluster = toStringOrSlice(v)
	}
	if v, ok := raw["labelSelector"].(string); ok {
		args.LabelSelector = v
	}
	if v, ok := raw["clusterSelector"].(string); ok {
		args.ClusterSelector = v
	}
	if v, ok := raw["status"]; ok {
		args.Status = toStringOrSlice(v)
	}
	if v, ok := raw["textSearch"].(string); ok {
		args.TextSearch = v
	}
	if v, ok := raw["ageNewerThan"].(string); ok {
		args.AgeNewerThan = v
	}
	if v, ok := raw["ageOlderThan"].(string); ok {
		args.AgeOlderThan = v
	}
	if v, ok := raw["outputMode"].(string); ok {
		args.OutputMode = v
	}
	if v, ok := raw["groupBy"].(string); ok {
		args.GroupBy = v
	}
	if v, ok := raw["limit"].(float64); ok {
		args.Limit = int(v)
	}
	if v, ok := raw["sortBy"].(string); ok {
		args.SortBy = v
	}
	if v, ok := raw["sortOrder"].(string); ok {
		args.SortOrder = v
	}

	return args
}
