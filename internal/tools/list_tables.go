package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ListTablesInput struct {
	Host     string `json:"host,omitempty" jsonschema:"host name from config, omit for default"`
	Database string `json:"database" jsonschema:"the database name"`
	Schema   string `json:"schema,omitempty" jsonschema:"optional schema filter (default: all user schemas)"`
}

type TableInfo struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Access string `json:"access"`
}

const listTablesSQL = `SELECT table_schema, table_name, table_type
FROM information_schema.tables
WHERE table_schema NOT IN ('pg_catalog', 'information_schema')
  AND table_schema NOT LIKE 'pg_toast%'
  AND table_schema NOT LIKE 'pg_temp_%'
ORDER BY table_schema, table_name`

const listTablesSchemaSQL = `SELECT table_schema, table_name, table_type
FROM information_schema.tables
WHERE table_schema = $1
ORDER BY table_name`

func registerListTables(server *mcp.Server, deps *Deps) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_tables",
		Description: "List all tables and views in a PostgreSQL database with their access levels. Returns schema, name, and type for each table.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: true,
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input ListTablesInput) (*mcp.CallToolResult, any, error) {
		pool, checker, _, err := resolveHost(deps, input.Host)
		if err != nil {
			return nil, nil, err
		}

		if !checker.IsVisible(input.Database) {
			return nil, nil, fmt.Errorf("database %s is not accessible", input.Database)
		}

		var rows []map[string]any
		if input.Schema != "" {
			rows, err = pool.QueryContext(ctx, input.Database, listTablesSchemaSQL, input.Schema)
		} else {
			rows, err = pool.QueryContext(ctx, input.Database, listTablesSQL)
		}
		if err != nil {
			return nil, nil, fmt.Errorf("list tables: %w", err)
		}

		var tables []TableInfo
		for _, row := range rows {
			name := fmt.Sprintf("%v", row["table_name"])
			tables = append(tables, TableInfo{
				Schema: fmt.Sprintf("%v", row["table_schema"]),
				Name:   name,
				Type:   fmt.Sprintf("%v", row["table_type"]),
				Access: checker.TableAccess(input.Database, name).String(),
			})
		}

		data, _ := json.MarshalIndent(tables, "", "  ")
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: string(data)},
			},
		}, nil, nil
	})
}
