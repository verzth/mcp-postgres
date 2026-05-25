package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/verzth/mcp-postgres/internal/access"
)

type ConfirmExecuteInput struct {
	DryRunID string `json:"dry_run_id" jsonschema:"the ID returned from a previous dry_run call"`
}

type ExecuteResult struct {
	Success      bool   `json:"success"`
	RowsAffected int64  `json:"rows_affected"`
	Host         string `json:"host"`
	Database     string `json:"database"`
	Kind         string `json:"kind"`
	Query        string `json:"query"`
}

func registerConfirmExecute(server *mcp.Server, deps *Deps) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "confirm_execute",
		Description: "Execute a previously previewed query by its dry_run_id. " +
			"You must call dry_run first to preview the query before executing it. " +
			"Dry-run IDs expire after the configured timeout (default 5 minutes).",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: ptrBool(true),
			ReadOnlyHint:    false,
			IdempotentHint:  false,
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input ConfirmExecuteInput) (*mcp.CallToolResult, any, error) {
		entry, err := deps.DryRuns.Get(input.DryRunID)
		if err != nil {
			return nil, nil, fmt.Errorf("dry-run lookup: %w", err)
		}

		// Resolve the host from the stored entry
		pool, checker, _, err := resolveHost(deps, entry.Host)
		if err != nil {
			deps.DryRuns.Remove(input.DryRunID)
			return nil, nil, fmt.Errorf("host from dry-run no longer available: %w", err)
		}

		// Re-verify permissions
		classified := access.ClassifySQL(entry.Query)
		for _, table := range classified.Tables {
			if classified.Kind.IsWrite() && !checker.CanWrite(entry.Database, table) {
				deps.DryRuns.Remove(input.DryRunID)
				return nil, nil, fmt.Errorf("write access to %s.%s has been revoked since dry-run", entry.Database, table)
			}
			if classified.Kind.IsDDL() && !checker.CanAdmin(entry.Database, table) {
				deps.DryRuns.Remove(input.DryRunID)
				return nil, nil, fmt.Errorf("admin access to %s.%s has been revoked since dry-run", entry.Database, table)
			}
		}

		sqlResult, err := pool.ExecContext(ctx, entry.Database, entry.Query)
		if err != nil {
			return nil, nil, fmt.Errorf("execute: %w", err)
		}

		rowsAffected, _ := sqlResult.RowsAffected()
		deps.DryRuns.Remove(input.DryRunID)

		result := ExecuteResult{
			Success:      true,
			RowsAffected: rowsAffected,
			Host:         entry.Host,
			Database:     entry.Database,
			Kind:         entry.Kind,
			Query:        entry.Query,
		}

		data, _ := json.MarshalIndent(result, "", "  ")
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: string(data)},
			},
		}, nil, nil
	})
}
