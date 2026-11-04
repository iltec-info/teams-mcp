package trello

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"teams-mcp/audit"
)

const auditPrincipal = "trello-mcp:api-token"

type handler = func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)

// Register wires up the Trello MCP tools. There is deliberately no
// delete_card: removal is archive_card (closed=true), which is reversible
// from the Trello UI.
func Register(s *server.MCPServer, c *Client) {
	s.AddTool(mcp.NewTool("list_boards",
		mcp.WithDescription("List the open Trello boards the token can see"),
		mcp.WithReadOnlyHintAnnotation(true),
	), listBoards(c))

	s.AddTool(mcp.NewTool("list_lists",
		mcp.WithDescription("List the open lists (columns) of a Trello board"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("board_id", mcp.Required(), mcp.Description("Board ID, from list_boards")),
	), listLists(c))

	s.AddTool(mcp.NewTool("list_labels",
		mcp.WithDescription("List the labels defined on a Trello board"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("board_id", mcp.Required(), mcp.Description("Board ID, from list_boards")),
	), listLabels(c))

	s.AddTool(mcp.NewTool("list_cards",
		mcp.WithDescription("List cards on a board or in one list. Give list_id for a single list, otherwise board_id. Output: id, list, name, due, labels, url."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("board_id", mcp.Description("Board ID (used when list_id is not given)")),
		mcp.WithString("list_id", mcp.Description("List ID, from list_lists")),
		mcp.WithString("filter", mcp.Description(`Board-wide only: "open" (default), "closed" (archived) or "all"`)),
	), listCards(c))

	s.AddTool(mcp.NewTool("get_card",
		mcp.WithDescription("Read one card in full: name, description, due date, labels, checklists and the latest comments"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("card_id", mcp.Required(), mcp.Description("Card ID, from list_cards")),
	), getCard(c))

	s.AddTool(mcp.NewTool("create_card",
		mcp.WithDescription("Create a card at the bottom of a list (optionally at the top)"),
		mcp.WithString("list_id", mcp.Required(), mcp.Description("List ID, from list_lists")),
		mcp.WithString("name", mcp.Required(), mcp.Description("Card title")),
		mcp.WithString("desc", mcp.Description("Card description (Markdown)")),
		mcp.WithString("due", mcp.Description("Due date, RFC3339 e.g. 2026-11-10T12:00:00Z")),
		mcp.WithArray("label_ids", mcp.Description("Label IDs, from list_labels"), mcp.WithStringItems()),
		mcp.WithString("pos", mcp.Description(`"top" or "bottom" (default)`)),
	), createCard(c))

	s.AddTool(mcp.NewTool("update_card",
		mcp.WithDescription("Edit a card; only the fields you pass change. Use list_id to move it to another list, closed=true to archive, desc to rewrite the description (empty string clears it), due=\"\" to clear the due date."),
		mcp.WithString("card_id", mcp.Required(), mcp.Description("Card ID")),
		mcp.WithString("name", mcp.Description("New title")),
		mcp.WithString("desc", mcp.Description("New description (Markdown); replaces the old one")),
		mcp.WithString("due", mcp.Description("Due date RFC3339, or empty string to clear")),
		mcp.WithBoolean("due_complete", mcp.Description("Mark the due date complete / not complete")),
		mcp.WithString("list_id", mcp.Description("Move the card to this list")),
		mcp.WithBoolean("closed", mcp.Description("true = archive, false = restore")),
		mcp.WithString("pos", mcp.Description(`"top" or "bottom"`)),
		mcp.WithArray("label_ids", mcp.Description("Replace the card's labels with exactly these label IDs"), mcp.WithStringItems()),
	), updateCard(c))

	s.AddTool(mcp.NewTool("archive_card",
		mcp.WithDescription("Archive (close) a card. Reversible: update_card closed=false restores it."),
		mcp.WithString("card_id", mcp.Required(), mcp.Description("Card ID")),
	), archiveCard(c))

	s.AddTool(mcp.NewTool("add_comment",
		mcp.WithDescription("Add a comment to a card"),
		mcp.WithString("card_id", mcp.Required(), mcp.Description("Card ID")),
		mcp.WithString("text", mcp.Required(), mcp.Description("Comment text (Markdown)")),
	), addComment(c))

	s.AddTool(mcp.NewTool("add_checklist",
		mcp.WithDescription("Add a checklist to a card, optionally pre-filled with items"),
		mcp.WithString("card_id", mcp.Required(), mcp.Description("Card ID")),
		mcp.WithString("name", mcp.Required(), mcp.Description("Checklist title")),
		mcp.WithArray("items", mcp.Description("Item texts, in order"), mcp.WithStringItems()),
	), addChecklist(c))

	s.AddTool(mcp.NewTool("set_check_item",
		mcp.WithDescription("Tick or untick one checklist item (IDs from get_card)"),
		mcp.WithString("card_id", mcp.Required(), mcp.Description("Card ID")),
		mcp.WithString("item_id", mcp.Required(), mcp.Description("Checklist item ID")),
		mcp.WithBoolean("complete", mcp.Required(), mcp.Description("true = done, false = not done")),
	), setCheckItem(c))
}

func fail(err error) (*mcp.CallToolResult, error) { return mcp.NewToolResultError(err.Error()), nil }

func has(req mcp.CallToolRequest, key string) bool {
	_, ok := req.GetArguments()[key]
	return ok
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func labelNames(ls []Label) string {
	var n []string
	for _, l := range ls {
		if l.Name != "" {
			n = append(n, l.Name)
		} else if l.Color != "" {
			n = append(n, l.Color)
		}
	}
	return strings.Join(n, ",")
}

func cardSummary(c *Card) string {
	state := ""
	if c.Closed {
		state = " [archived]"
	}
	return fmt.Sprintf("%s\t%s%s\t%s", c.ID, oneLine(c.Name), state, c.ShortURL)
}

func listBoards(c *Client) handler {
	return func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		boards, err := c.ListBoards(ctx)
		audit.Log(auditPrincipal, "trello.list_boards", "/members/me/boards", err)
		if err != nil {
			return fail(err)
		}
		var b strings.Builder
		for _, x := range boards {
			fmt.Fprintf(&b, "%s\t%s\t%s\n", x.ID, x.Name, x.URL)
		}
		if b.Len() == 0 {
			return mcp.NewToolResultText("no open boards"), nil
		}
		return mcp.NewToolResultText(b.String()), nil
	}
}

func listLists(c *Client) handler {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, err := req.RequireString("board_id")
		if err != nil {
			return fail(err)
		}
		lists, err := c.ListLists(ctx, id)
		audit.Log(auditPrincipal, "trello.list_lists", id, err)
		if err != nil {
			return fail(err)
		}
		var b strings.Builder
		for _, x := range lists {
			fmt.Fprintf(&b, "%s\t%s\n", x.ID, x.Name)
		}
		if b.Len() == 0 {
			return mcp.NewToolResultText("no open lists"), nil
		}
		return mcp.NewToolResultText(b.String()), nil
	}
}

func listLabels(c *Client) handler {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, err := req.RequireString("board_id")
		if err != nil {
			return fail(err)
		}
		labels, err := c.ListLabels(ctx, id)
		audit.Log(auditPrincipal, "trello.list_labels", id, err)
		if err != nil {
			return fail(err)
		}
		var b strings.Builder
		for _, x := range labels {
			fmt.Fprintf(&b, "%s\t%s\t%s\n", x.ID, x.Color, x.Name)
		}
		if b.Len() == 0 {
			return mcp.NewToolResultText("no labels"), nil
		}
		return mcp.NewToolResultText(b.String()), nil
	}
}

func listCards(c *Client) handler {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		listID := req.GetString("list_id", "")
		boardID := req.GetString("board_id", "")
		var (
			cards  []Card
			err    error
			target string
		)
		switch {
		case listID != "":
			target = listID
			cards, err = c.ListCardsByList(ctx, listID)
		case boardID != "":
			filter := req.GetString("filter", "open")
			if filter != "open" && filter != "closed" && filter != "all" {
				return fail(fmt.Errorf(`invalid filter %q: use "open", "closed" or "all"`, filter))
			}
			target = boardID
			cards, err = c.ListCardsByBoard(ctx, boardID, filter)
		default:
			return fail(fmt.Errorf("pass list_id or board_id"))
		}
		audit.Log(auditPrincipal, "trello.list_cards", target, err)
		if err != nil {
			return fail(err)
		}
		if len(cards) == 0 {
			return mcp.NewToolResultText("no cards"), nil
		}
		var b strings.Builder
		for i := range cards {
			x := &cards[i]
			due := ""
			if x.Due != "" {
				due = x.Due
				if x.DueComplete {
					due += " (done)"
				}
			}
			state := ""
			if x.Closed {
				state = " [archived]"
			}
			fmt.Fprintf(&b, "%s\t%s\t%s%s\t%s\t%s\t%s\n", x.ID, x.IDList, oneLine(x.Name), state, due, labelNames(x.Labels), x.ShortURL)
		}
		return mcp.NewToolResultText(b.String()), nil
	}
}

func getCard(c *Client) handler {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, err := req.RequireString("card_id")
		if err != nil {
			return fail(err)
		}
		card, err := c.GetCard(ctx, id)
		audit.Log(auditPrincipal, "trello.get_card", id, err)
		if err != nil {
			return fail(err)
		}
		comments, cerr := c.ListComments(ctx, id, 10)
		audit.Log(auditPrincipal, "trello.list_comments", id, cerr)

		var b strings.Builder
		fmt.Fprintf(&b, "id: %s\nname: %s\nurl: %s\nlist: %s\narchived: %v\n", card.ID, card.Name, card.ShortURL, card.IDList, card.Closed)
		if card.Due != "" {
			fmt.Fprintf(&b, "due: %s (complete: %v)\n", card.Due, card.DueComplete)
		}
		if l := labelNames(card.Labels); l != "" {
			fmt.Fprintf(&b, "labels: %s\n", l)
		}
		fmt.Fprintf(&b, "\n--- description ---\n%s\n", card.Desc)
		for _, cl := range card.Checklists {
			fmt.Fprintf(&b, "\n--- checklist %s (%s) ---\n", cl.Name, cl.ID)
			for _, it := range cl.CheckItems {
				mark := " "
				if it.State == "complete" {
					mark = "x"
				}
				fmt.Fprintf(&b, "[%s] %s\t%s\n", mark, it.Name, it.ID)
			}
		}
		if cerr == nil && len(comments) > 0 {
			b.WriteString("\n--- comments (newest first) ---\n")
			for _, cm := range comments {
				fmt.Fprintf(&b, "%s %s: %s\n", cm.Date, cm.MemberCreator.FullName, cm.Data.Text)
			}
		}
		return mcp.NewToolResultText(b.String()), nil
	}
}

func createCard(c *Client) handler {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		listID, err := req.RequireString("list_id")
		if err != nil {
			return fail(err)
		}
		name, err := req.RequireString("name")
		if err != nil {
			return fail(err)
		}
		q := url.Values{"idList": {listID}, "name": {name}}
		if v := req.GetString("desc", ""); v != "" {
			q.Set("desc", v)
		}
		if v := req.GetString("due", ""); v != "" {
			q.Set("due", v)
		}
		if v := req.GetString("pos", ""); v != "" {
			q.Set("pos", v)
		}
		if ids := req.GetStringSlice("label_ids", nil); len(ids) > 0 {
			q.Set("idLabels", strings.Join(ids, ","))
		}
		card, err := c.CreateCard(ctx, q)
		audit.Log(auditPrincipal, "trello.create_card", name, err)
		if err != nil {
			return fail(err)
		}
		return mcp.NewToolResultText("created\n" + cardSummary(card)), nil
	}
}

func updateCard(c *Client) handler {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, err := req.RequireString("card_id")
		if err != nil {
			return fail(err)
		}
		q := url.Values{}
		var changed []string
		set := func(arg, param string) {
			if has(req, arg) {
				q.Set(param, req.GetString(arg, ""))
				changed = append(changed, arg)
			}
		}
		set("name", "name")
		set("desc", "desc")
		set("list_id", "idList")
		set("pos", "pos")
		if has(req, "due") {
			v := req.GetString("due", "")
			if v == "" {
				v = "null" // Trello's way to clear a due date
			}
			q.Set("due", v)
			changed = append(changed, "due")
		}
		if has(req, "due_complete") {
			q.Set("dueComplete", fmt.Sprint(req.GetBool("due_complete", false)))
			changed = append(changed, "due_complete")
		}
		if has(req, "closed") {
			q.Set("closed", fmt.Sprint(req.GetBool("closed", false)))
			changed = append(changed, "closed")
		}
		if has(req, "label_ids") {
			q.Set("idLabels", strings.Join(req.GetStringSlice("label_ids", nil), ","))
			changed = append(changed, "label_ids")
		}
		if len(changed) == 0 {
			return fail(fmt.Errorf("nothing to update: pass at least one field besides card_id"))
		}
		card, err := c.UpdateCard(ctx, id, q)
		audit.Log(auditPrincipal, "trello.update_card["+strings.Join(changed, ",")+"]", id, err)
		if err != nil {
			return fail(err)
		}
		return mcp.NewToolResultText("updated " + strings.Join(changed, ", ") + "\n" + cardSummary(card)), nil
	}
}

func archiveCard(c *Client) handler {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, err := req.RequireString("card_id")
		if err != nil {
			return fail(err)
		}
		card, err := c.UpdateCard(ctx, id, url.Values{"closed": {"true"}})
		audit.Log(auditPrincipal, "trello.archive_card", id, err)
		if err != nil {
			return fail(err)
		}
		return mcp.NewToolResultText("archived\n" + cardSummary(card)), nil
	}
}

func addComment(c *Client) handler {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, err := req.RequireString("card_id")
		if err != nil {
			return fail(err)
		}
		text, err := req.RequireString("text")
		if err != nil {
			return fail(err)
		}
		err = c.AddComment(ctx, id, text)
		audit.Log(auditPrincipal, "trello.add_comment", id, err)
		if err != nil {
			return fail(err)
		}
		return mcp.NewToolResultText("comment added to " + id), nil
	}
}

func addChecklist(c *Client) handler {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, err := req.RequireString("card_id")
		if err != nil {
			return fail(err)
		}
		name, err := req.RequireString("name")
		if err != nil {
			return fail(err)
		}
		cl, err := c.CreateChecklist(ctx, id, name)
		audit.Log(auditPrincipal, "trello.add_checklist", id, err)
		if err != nil {
			return fail(err)
		}
		var b strings.Builder
		fmt.Fprintf(&b, "checklist %s\t%s\n", cl.ID, cl.Name)
		for _, text := range req.GetStringSlice("items", nil) {
			it, err := c.AddCheckItem(ctx, cl.ID, text)
			audit.Log(auditPrincipal, "trello.add_check_item", cl.ID, err)
			if err != nil {
				fmt.Fprintf(&b, "FAILED %q: %v\n", text, err)
				continue
			}
			fmt.Fprintf(&b, "item %s\t%s\n", it.ID, it.Name)
		}
		return mcp.NewToolResultText(b.String()), nil
	}
}

func setCheckItem(c *Client) handler {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		cardID, err := req.RequireString("card_id")
		if err != nil {
			return fail(err)
		}
		itemID, err := req.RequireString("item_id")
		if err != nil {
			return fail(err)
		}
		done, err := req.RequireBool("complete")
		if err != nil {
			return fail(err)
		}
		state := "incomplete"
		if done {
			state = "complete"
		}
		err = c.SetCheckItemState(ctx, cardID, itemID, state)
		audit.Log(auditPrincipal, "trello.set_check_item", itemID, err)
		if err != nil {
			return fail(err)
		}
		return mcp.NewToolResultText("item " + itemID + " -> " + state), nil
	}
}
