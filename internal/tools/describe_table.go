package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type DescribeTableInput struct {
	Host     string `json:"host,omitempty" jsonschema:"host name from config, omit for default"`
	Database string `json:"database" jsonschema:"the database name"`
	Table    string `json:"table" jsonschema:"the table name"`
	Schema   string `json:"schema,omitempty" jsonschema:"schema name (default: public)"`
}

type TableDescription struct {
	Schema      string           `json:"schema"`
	Table       string           `json:"table"`
	Columns     []map[string]any `json:"columns"`
	Indexes     []map[string]any `json:"indexes"`
	ForeignKeys []map[string]any `json:"foreign_keys"`
	Constraints []map[string]any `json:"constraints"`
}

const describeColumnsSQL = `SELECT
  column_name,
  ordinal_position,
  data_type,
  udt_name,
  character_maximum_length,
  numeric_precision,
  numeric_scale,
  is_nullable,
  column_default,
  is_identity,
  identity_generation
FROM information_schema.columns
WHERE table_schema = $1 AND table_name = $2
ORDER BY ordinal_position`

const describeIndexesSQL = `SELECT indexname, indexdef
FROM pg_indexes
WHERE schemaname = $1 AND tablename = $2
ORDER BY indexname`

const describeForeignKeysSQL = `SELECT
  tc.constraint_name,
  kcu.column_name,
  ccu.table_schema AS referenced_schema,
  ccu.table_name   AS referenced_table,
  ccu.column_name  AS referenced_column,
  rc.update_rule,
  rc.delete_rule
FROM information_schema.table_constraints tc
JOIN information_schema.key_column_usage kcu
  ON tc.constraint_name = kcu.constraint_name
 AND tc.table_schema   = kcu.table_schema
JOIN information_schema.constraint_column_usage ccu
  ON ccu.constraint_name = tc.constraint_name
 AND ccu.table_schema    = tc.table_schema
JOIN information_schema.referential_constraints rc
  ON rc.constraint_name = tc.constraint_name
 AND rc.constraint_schema = tc.table_schema
WHERE tc.constraint_type = 'FOREIGN KEY'
  AND tc.table_schema = $1
  AND tc.table_name   = $2
ORDER BY tc.constraint_name, kcu.ordinal_position`

const describeConstraintsSQL = `SELECT
  conname AS constraint_name,
  contype AS constraint_type,
  pg_get_constraintdef(oid) AS definition
FROM pg_constraint
WHERE conrelid = ($1 || '.' || $2)::regclass
ORDER BY conname`

func registerDescribeTable(server *mcp.Server, deps *Deps) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "describe_table",
		Description: "Show columns, indexes, foreign keys, and constraints for a PostgreSQL table. Defaults to the 'public' schema if not specified.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: true,
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input DescribeTableInput) (*mcp.CallToolResult, any, error) {
		pool, checker, _, err := resolveHost(deps, input.Host)
		if err != nil {
			return nil, nil, err
		}

		if !checker.CanRead(input.Database, input.Table) {
			return nil, nil, fmt.Errorf("no read access to %s.%s", input.Database, input.Table)
		}

		schema := input.Schema
		if schema == "" {
			schema = "public"
		}

		desc := TableDescription{Schema: schema, Table: input.Table}

		cols, err := pool.QueryContext(ctx, input.Database, describeColumnsSQL, schema, input.Table)
		if err != nil {
			return nil, nil, fmt.Errorf("describe columns: %w", err)
		}
		desc.Columns = cols

		idx, err := pool.QueryContext(ctx, input.Database, describeIndexesSQL, schema, input.Table)
		if err != nil {
			return nil, nil, fmt.Errorf("describe indexes: %w", err)
		}
		desc.Indexes = idx

		fks, err := pool.QueryContext(ctx, input.Database, describeForeignKeysSQL, schema, input.Table)
		if err != nil {
			return nil, nil, fmt.Errorf("describe foreign keys: %w", err)
		}
		desc.ForeignKeys = fks

		cons, err := pool.QueryContext(ctx, input.Database, describeConstraintsSQL, schema, input.Table)
		if err != nil {
			// regclass cast can fail on missing tables — surface as empty rather than fatal
			desc.Constraints = nil
		} else {
			desc.Constraints = cons
		}

		data, _ := json.MarshalIndent(desc, "", "  ")
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: string(data)},
			},
		}, nil, nil
	})
}
