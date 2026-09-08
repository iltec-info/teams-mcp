// Read-only variant for sharing with colleagues: exposes list_teams,
// list_channels, and read_channel, but not send_message. Uses the same
// tenant-wide-consented app registration as the full binary, so each
// colleague only needs their own one-time device-code sign-in — no new
// Azure AD app registration or admin consent required.
package main

import (
	"context"
	"log"

	"github.com/mark3labs/mcp-go/server"

	"teams-mcp/auth"
	"teams-mcp/graph"
	"teams-mcp/tools"
)

func main() {
	client := graph.NewClient(func(ctx context.Context) (string, error) {
		return auth.GetToken(ctx, "", "")
	})

	s := server.NewMCPServer("teams-mcp-readonly", "0.1.0")
	tools.Register(s, client, true)

	if err := server.ServeStdio(s); err != nil {
		log.Fatal(err)
	}
}
