package main

import (
	"context"
	"flag"
	"log"

	"github.com/mark3labs/mcp-go/server"

	"teams-mcp/auth"
	"teams-mcp/graph"
	"teams-mcp/tools"
)

func main() {
	clientID := flag.String("client-id", "", "Azure AD app (client) ID")
	tenantID := flag.String("tenant-id", "", "Azure AD tenant ID")
	flag.Parse()

	client := graph.NewClient(func(ctx context.Context) (string, error) {
		return auth.GetToken(ctx, *clientID, *tenantID)
	})

	s := server.NewMCPServer("teams-mcp", "0.1.0")
	tools.Register(s, client, false)

	if err := server.ServeStdio(s); err != nil {
		log.Fatal(err)
	}
}
