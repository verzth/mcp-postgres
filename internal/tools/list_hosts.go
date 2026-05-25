package tools

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ListHostsInput struct{}

type HostInfo struct {
	Name      string `json:"name"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Connected bool   `json:"connected"`
	Error     string `json:"error,omitempty"`
}

func registerListHosts(server *mcp.Server, deps *Deps) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_hosts",
		Description: "List all configured PostgreSQL hosts with their connection status. Disconnected hosts will be retried automatically when you use them.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: true,
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input ListHostsInput) (*mcp.CallToolResult, any, error) {
		var infos []HostInfo
		for _, name := range deps.Config.HostNames() {
			host := deps.Config.Hosts[name]
			connected, lastErr := deps.Pools.HostStatus(name)
			info := HostInfo{
				Name:      name,
				Host:      host.Connection.Host,
				Port:      host.Connection.Port,
				Connected: connected,
			}
			if lastErr != nil {
				info.Error = lastErr.Error()
			}
			infos = append(infos, info)
		}

		data, _ := json.MarshalIndent(infos, "", "  ")
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: string(data)},
			},
		}, nil, nil
	})
}
