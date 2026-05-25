package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/verzth/mcp-postgres/internal/access"
)

type QueryInput struct {
	Host     string `json:"host,omitempty" jsonschema:"host name from config, omit for default"`
	Database string `json:"database" jsonschema:"the database to query"`
	SQL      string `json:"sql" jsonschema:"SELECT query to execute"`
	Limit    int    `json:"limit,omitempty" jsonschema:"max rows to return (default 100, max 1000)"`
}

func registerQuery(server *mcp.Server, deps *Deps) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "query",
		Description: "Execute a SELECT (or WITH ... SELECT) query against a PostgreSQL database. Only read queries are allowed. Results are limited to 1000 rows maximum.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: true,
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input QueryInput) (*mcp.CallToolResult, any, error) {
		pool, checker, _, err := resolveHost(deps, input.Host)
		if err != nil {
			return nil, nil, err
		}

		if !checker.IsVisible(input.Database) {
			return nil, nil, fmt.Errorf("database %s is not accessible", input.Database)
		}

		classified := access.ClassifySQL(input.SQL)
		if !classified.Kind.IsRead() {
			return nil, nil, fmt.Errorf("only SELECT queries are allowed, got %s", classified.Kind)
		}

		for _, table := range classified.Tables {
			if !checker.CanRead(input.Database, table) {
				return nil, nil, fmt.Errorf("no read access to %s.%s", input.Database, table)
			}
		}

		limit := input.Limit
		if limit <= 0 {
			limit = 100
		}
		if limit > 1000 {
			limit = 1000
		}

		query := input.SQL
		upperSQL := strings.ToUpper(strings.TrimSpace(query))
		if !strings.Contains(upperSQL, "LIMIT") {
			query = fmt.Sprintf("%s LIMIT %d", strings.TrimRight(strings.TrimSpace(query), ";"), limit)
		}

		rows, err := pool.QueryContext(ctx, input.Database, query)
		if err != nil {
			return nil, nil, fmt.Errorf("query: %w", err)
		}

		data, _ := json.MarshalIndent(rows, "", "  ")
		result := fmt.Sprintf("Returned %d rows:\n%s", len(rows), string(data))

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: result},
			},
		}, nil, nil
	})
}
