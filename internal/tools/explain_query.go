package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ExplainQueryInput struct {
	Host     string `json:"host,omitempty" jsonschema:"host name from config, omit for default"`
	Database string `json:"database" jsonschema:"the database to query"`
	SQL      string `json:"sql" jsonschema:"the SQL query to explain"`
}

func registerExplainQuery(server *mcp.Server, deps *Deps) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "explain_query",
		Description: "Run EXPLAIN on a SQL query to show the PostgreSQL execution plan. Useful for analyzing query performance.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: true,
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input ExplainQueryInput) (*mcp.CallToolResult, any, error) {
		pool, checker, _, err := resolveHost(deps, input.Host)
		if err != nil {
			return nil, nil, err
		}

		if !checker.IsVisible(input.Database) {
			return nil, nil, fmt.Errorf("database %s is not accessible", input.Database)
		}

		explainSQL := "EXPLAIN " + input.SQL
		rows, err := pool.QueryContext(ctx, input.Database, explainSQL)
		if err != nil {
			return nil, nil, fmt.Errorf("explain: %w", err)
		}

		data, _ := json.MarshalIndent(rows, "", "  ")
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: string(data)},
			},
		}, nil, nil
	})
}
