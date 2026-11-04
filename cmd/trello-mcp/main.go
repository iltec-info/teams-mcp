// trello-mcp: MCP server (stdio) exposing Trello boards, lists, cards,
// comments and checklists as tools. Credentials are read from
// ~/.trello_creds (API key on line 1, token on line 2).
package main

import (
	"log"

	"github.com/mark3labs/mcp-go/server"

	"teams-mcp/trello"
)

func main() {
	client, err := trello.NewClientFromFile()
	if err != nil {
		log.Fatal(err)
	}

	s := server.NewMCPServer("trello-mcp", "0.1.0")
	trello.Register(s, client)

	if err := server.ServeStdio(s); err != nil {
		log.Fatal(err)
	}
}
