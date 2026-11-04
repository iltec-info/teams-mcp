// trello-mcp: MCP server (stdio) exposing Trello boards, lists, cards,
// comments and checklists as tools. Credentials are read from
// the OS secret store (Windows Credential Manager); see cmd/credstore.
package main

import (
	"log"

	"github.com/mark3labs/mcp-go/server"

	"teams-mcp/trello"
)

func main() {
	client, err := trello.NewClientFromStore()
	if err != nil {
		log.Fatal(err)
	}

	s := server.NewMCPServer("trello-mcp", "0.1.0")
	trello.Register(s, client)

	if err := server.ServeStdio(s); err != nil {
		log.Fatal(err)
	}
}
