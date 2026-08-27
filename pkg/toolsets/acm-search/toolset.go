package acmsearch

import (
	"slices"

	"github.com/containers/kubernetes-mcp-server/pkg/api"
	"github.com/containers/kubernetes-mcp-server/pkg/toolsets"
)

// Toolset provides ACM fleet-wide resource search via the ACM search database.
type Toolset struct{}

var _ api.Toolset = (*Toolset)(nil)

func (t *Toolset) GetName() string {
	return "acm-search"
}

func (t *Toolset) GetDescription() string {
	return "Tools for searching and analyzing Kubernetes resources across ACM managed clusters"
}

func (t *Toolset) GetTools(_ api.FilteringProvider) []api.ServerTool {
	return slices.Concat(
		initFindResources(),
	)
}

func (t *Toolset) GetPrompts() []api.ServerPrompt {
	return nil
}

func (t *Toolset) GetResources() []api.ServerResource {
	return nil
}

func (t *Toolset) GetResourceTemplates() []api.ServerResourceTemplate {
	return nil
}

func init() {
	toolsets.Register(&Toolset{})
}
