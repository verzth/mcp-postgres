package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ListDatabasesInput struct {
	Host string `json:"host,omitempty" jsonschema:"host name from config, omit for default"`
}

type DatabaseInfo struct {
	Host   string `json:"host"`
	Name   string `json:"name"`
	Access string `json:"access"`
}

func registerListDatabases(server *mcp.Server, deps *Deps) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_databases",
		Description: "List all accessible PostgreSQL databases on this host with their access levels",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: true,
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input ListDatabasesInput) (*mcp.CallToolResult, any, error) {
		pool, checker, hostName, err := resolveHost(deps, input.Host)
		if err != nil {
			return nil, nil, err
		}

		allDBs, err := pool.DiscoverDatabases(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("discover databases: %w", err)
		}

		visible := checker.FilterDatabases(allDBs)
		var infos []DatabaseInfo
		for _, db := range visible {
			infos = append(infos, DatabaseInfo{
				Host:   hostName,
				Name:   db,
				Access: checker.DatabaseAccess(db).String(),
			})
		}

		data, _ := json.MarshalIndent(infos, "", "  ")
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: string(data)},
			},
		}, nil, nil
	})
}
