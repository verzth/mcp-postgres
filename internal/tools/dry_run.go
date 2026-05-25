package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/verzth/mcp-postgres/internal/access"
)

type DryRunInput struct {
	Host     string `json:"host,omitempty" jsonschema:"host name from config, omit for default"`
	Database string `json:"database" jsonschema:"the database to execute against"`
	SQL      string `json:"sql" jsonschema:"the write query (INSERT/UPDATE/DELETE) or DDL (ALTER/CREATE/DROP) to preview"`
}

type DryRunResult struct {
	DryRunID         string   `json:"dry_run_id"`
	Host             string   `json:"host"`
	Kind             string   `json:"kind"`
	Database         string   `json:"database"`
	Tables           []string `json:"tables"`
	EstimatedRows    int64    `json:"estimated_rows"`
	ExplainPlan      string   `json:"explain_plan"`
	Query            string   `json:"query"`
	ExpiresInSeconds int      `json:"expires_in_seconds"`
	Message          string   `json:"message"`
}

func registerDryRun(server *mcp.Server, deps *Deps) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "dry_run",
		Description: "Preview a write query (INSERT/UPDATE/DELETE) or DDL statement (ALTER/CREATE/DROP) without executing it. " +
			"Returns an estimated impact and a dry_run_id that can be passed to confirm_execute to actually run the query. " +
			"This is the ONLY way to execute write operations - all mutations must go through dry_run first.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: true,
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input DryRunInput) (*mcp.CallToolResult, any, error) {
		pool, checker, hostName, err := resolveHost(deps, input.Host)
		if err != nil {
			return nil, nil, err
		}

		if !checker.IsVisible(input.Database) {
			return nil, nil, fmt.Errorf("database %s is not accessible", input.Database)
		}

		classified := access.ClassifySQL(input.SQL)

		if classified.Kind.IsRead() {
			return nil, nil, fmt.Errorf("dry_run is for write/DDL operations only, use the 'query' tool for SELECT statements")
		}
		if classified.Kind == access.KindOther {
			return nil, nil, fmt.Errorf("unsupported SQL statement type")
		}

		for _, table := range classified.Tables {
			if classified.Kind.IsWrite() && !checker.CanWrite(input.Database, table) {
				return nil, nil, fmt.Errorf("no write access to %s.%s", input.Database, table)
			}
			if classified.Kind.IsDDL() && !checker.CanAdmin(input.Database, table) {
				return nil, nil, fmt.Errorf("no admin access to %s.%s (required for DDL operations)", input.Database, table)
			}
		}

		var explainText string
		var estimatedRows int64

		if !classified.Kind.IsDDL() {
			explainRows, err := pool.QueryContext(ctx, input.Database, "EXPLAIN "+input.SQL)
			if err != nil {
				explainText = fmt.Sprintf("EXPLAIN failed: %v", err)
			} else {
				data, _ := json.MarshalIndent(explainRows, "", "  ")
				explainText = string(data)

				// Parse first row's "QUERY PLAN" text for "rows=N" hint.
				for _, row := range explainRows {
					for _, v := range row {
						s := fmt.Sprintf("%v", v)
						if idx := strings.Index(s, "rows="); idx >= 0 {
							rest := s[idx+len("rows="):]
							var n int64
							fmt.Sscanf(rest, "%d", &n)
							if n > 0 {
								estimatedRows = n
								break
							}
						}
					}
					if estimatedRows > 0 {
						break
					}
				}
			}
		} else {
			explainText = "EXPLAIN not available for DDL statements"
		}

		id := deps.DryRuns.Add(
			hostName,
			input.Database,
			input.SQL,
			string(classified.Kind),
			classified.Tables,
			explainText,
			estimatedRows,
		)

		result := DryRunResult{
			DryRunID:         id,
			Host:             hostName,
			Kind:             string(classified.Kind),
			Database:         input.Database,
			Tables:           classified.Tables,
			EstimatedRows:    estimatedRows,
			ExplainPlan:      explainText,
			Query:            input.SQL,
			ExpiresInSeconds: deps.Config.DryRun.TimeoutSeconds,
			Message:          fmt.Sprintf("Query previewed successfully. Use confirm_execute with dry_run_id '%s' to execute this %s query.", id, classified.Kind),
		}

		data, _ := json.MarshalIndent(result, "", "  ")
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: string(data)},
			},
		}, nil, nil
	})
}
