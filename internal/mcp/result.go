// Package mcp holds the shared HTTP-serving and tool-result helpers for the
// garmin MCP service. It is deliberately transport-agnostic.
package mcp

import (
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// JSONResult wraps raw JSON bytes as an MCP tool result, preserving the exact
// payload an upstream API returned.
func JSONResult(body []byte) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(body)}},
	}
}

// DataResult marshals a value to JSON and returns it as a tool result. Used for
// write-tool previews/confirmations that a server constructs itself.
func DataResult(value any) (*mcp.CallToolResult, any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, nil, fmt.Errorf("encoding result: %w", err)
	}
	return JSONResult(encoded), nil, nil
}
