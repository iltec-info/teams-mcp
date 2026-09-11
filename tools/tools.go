package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"teams-mcp/audit"
	"teams-mcp/graph"
)

const auditPrincipal = "delegated:device-code"

// Register wires up the MCP tools. When readOnly is true, send_message is
// omitted entirely — decided at compile time (via separate cmd/ binaries)
// rather than a runtime flag, since args/env passed through the MCP host
// that launches this binary have proven unreliable to depend on.
func Register(s *server.MCPServer, client *graph.Client, readOnly bool) {
	s.AddTool(mcp.NewTool("list_teams",
		mcp.WithDescription("List the Microsoft Teams the signed-in user has joined"),
	), listTeamsHandler(client))

	s.AddTool(mcp.NewTool("list_channels",
		mcp.WithDescription("List the channels within a Microsoft Teams team"),
		mcp.WithString("team_id", mcp.Required(), mcp.Description("Team ID, from list_teams")),
	), listChannelsHandler(client))

	s.AddTool(mcp.NewTool("read_channel",
		mcp.WithDescription("Read recent messages from a Microsoft Teams channel"),
		mcp.WithString("team_id", mcp.Required(), mcp.Description("Team ID, from list_teams")),
		mcp.WithString("channel_id", mcp.Required(), mcp.Description("Channel ID within the team")),
	), readChannelHandler(client))

	s.AddTool(mcp.NewTool("list_chats",
		mcp.WithDescription("List the signed-in user's 1:1 and group Teams chats (not channels)"),
	), listChatsHandler(client))

	s.AddTool(mcp.NewTool("read_chat",
		mcp.WithDescription("Read recent messages from a 1:1 or group Teams chat"),
		mcp.WithString("chat_id", mcp.Required(), mcp.Description("Chat ID, from list_chats")),
	), readChatHandler(client))

	if readOnly {
		return
	}

	s.AddTool(mcp.NewTool("send_message",
		mcp.WithDescription("Send a message to a Microsoft Teams channel"),
		mcp.WithString("team_id", mcp.Required(), mcp.Description("Team ID, from list_teams")),
		mcp.WithString("channel_id", mcp.Required(), mcp.Description("Channel ID within the team")),
		mcp.WithString("message", mcp.Required(), mcp.Description("Message text to send")),
	), sendMessageHandler(client))
}

func listTeamsHandler(client *graph.Client) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		teamsList, err := client.ListJoinedTeams(ctx)
		audit.Log(auditPrincipal, "graph.list_teams", "/me/joinedTeams", err)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if len(teamsList) == 0 {
			return mcp.NewToolResultText("no joined teams found"), nil
		}
		var b strings.Builder
		for _, t := range teamsList {
			fmt.Fprintf(&b, "%s\t%s\n", t.ID, t.DisplayName)
		}
		return mcp.NewToolResultText(b.String()), nil
	}
}

func listChannelsHandler(client *graph.Client) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		teamID, err := req.RequireString("team_id")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		channels, err := client.ListChannels(ctx, teamID)
		audit.Log(auditPrincipal, "graph.list_channels", teamID, err)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if len(channels) == 0 {
			return mcp.NewToolResultText("no channels found"), nil
		}
		var b strings.Builder
		for _, c := range channels {
			fmt.Fprintf(&b, "%s\t%s\n", c.ID, c.DisplayName)
		}
		return mcp.NewToolResultText(b.String()), nil
	}
}

func readChannelHandler(client *graph.Client) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		teamID, err := req.RequireString("team_id")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		channelID, err := req.RequireString("channel_id")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		messages, err := client.ReadChannelMessages(ctx, teamID, channelID)
		audit.Log(auditPrincipal, "graph.read_channel", teamID+"/"+channelID, err)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatMessages(messages)), nil
	}
}

func formatMessages(messages []graph.Message) string {
	if len(messages) == 0 {
		return "no messages found"
	}
	var b strings.Builder
	for _, m := range messages {
		fmt.Fprintf(&b, "[%s] %s: %s\n", m.CreatedDateTime, m.From.User.DisplayName, m.Body.Content)
		for _, mn := range m.Mentions {
			if mn.Mentioned.User.DisplayName != "" {
				fmt.Fprintf(&b, "    @mentions: %s\n", mn.Mentioned.User.DisplayName)
			}
		}
	}
	return b.String()
}

func listChatsHandler(client *graph.Client) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		chats, err := client.ListChats(ctx)
		audit.Log(auditPrincipal, "graph.list_chats", "/me/chats", err)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if len(chats) == 0 {
			return mcp.NewToolResultText("no chats found"), nil
		}
		var b strings.Builder
		for _, c := range chats {
			label := c.Topic
			if label == "" {
				names := make([]string, 0, len(c.Members))
				for _, m := range c.Members {
					if m.DisplayName != "" {
						names = append(names, m.DisplayName)
					}
				}
				label = strings.Join(names, ", ")
			}
			fmt.Fprintf(&b, "%s\t[%s] %s\n", c.ID, c.ChatType, label)
		}
		return mcp.NewToolResultText(b.String()), nil
	}
}

func readChatHandler(client *graph.Client) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		chatID, err := req.RequireString("chat_id")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		messages, err := client.ReadChatMessages(ctx, chatID)
		audit.Log(auditPrincipal, "graph.read_chat", chatID, err)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatMessages(messages)), nil
	}
}

func sendMessageHandler(client *graph.Client) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		teamID, err := req.RequireString("team_id")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		channelID, err := req.RequireString("channel_id")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		message, err := req.RequireString("message")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		err = client.SendChannelMessage(ctx, teamID, channelID, message)
		audit.Log(auditPrincipal, "graph.send_message", teamID+"/"+channelID, err)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText("message sent"), nil
	}
}
