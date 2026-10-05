# teams-mcp

An MCP server exposing Microsoft Teams (via Microsoft Graph) as tools for Claude Code, plus a
scheduled job that scrapes a Teams "daily digest" bot channel for @mention-tagged action items
and syncs them to a Trello board as cards (deduped against the whole board).

## Layout

- `main.go` — full MCP server (`list_teams`, `list_channels`, `read_channel`, `list_chats`,
  `read_chat`, `send_message`), stdio transport, delegated device-code auth. `read_chat` and
  `read_channel` accept optional `since` (`today`, `week`, `month`, `YYYY-MM-DD`, or RFC3339)
  and `limit` (newest-first cap). For `read_chat` these push down to Graph as
  `$filter`/`$orderby`/`$top` on `lastModifiedDateTime`, so a "today only" read is one cheap
  call instead of the full history. Channel message lists accept only `$top`/`$expand`, so
  `read_channel` still fetches every page and applies `since` client-side — the output shrinks,
  the fetch doesn't.
- `cmd/teams-mcp-readonly` — same server without `send_message`, for sharing with colleagues.
- `cmd/sync` — the scheduled Teams-digest-to-Trello sync job. Uses app-only (client-credentials,
  certificate-based) auth instead of device-code, since it runs unattended.
- `cmd/provision-cert` — one-time tool that generates a non-exportable RSA key directly inside
  the Windows CNG certificate store (Microsoft Software Key Storage Provider) and self-signs a
  certificate for it, for the sync job's app-only auth. The private key never exists as a file.
- `auth/` — delegated (device-code) MSAL auth, used by the interactive MCP server.
- `authapp/` — app-only (client-credentials, cert-signed) MSAL auth, used by `cmd/sync`.
- `graph/` — thin Microsoft Graph v1.0 client (pagination-aware).
- `tools/` — MCP tool definitions/handlers.
- `audit/` — structured (JSON-lines) audit log of every Graph/Trello read or write, tagged by
  which identity (delegated user vs. service principal) performed it.
- `internal/config/` — deployment-specific identifiers (tenant, team/channel, Trello board, cert
  thumbprint). Copy `config.go.example` to `config.go` and fill in your own; `config.go` is
  gitignored.

## Setup

1. **Delegated app registration** (interactive MCP use): register a public-client Azure AD app,
   grant delegated Graph permissions (`Chat.Read`, `ChannelMessage.Send`,
   `ChannelMessage.Read.All`, `Team.ReadBasic.All`, `Channel.ReadBasic.All`), consent once as
   tenant admin so any org member can sign in without further consent prompts.
2. **Service app registration** (unattended sync job): register a second Azure AD app, grant it
   the *Application* (not delegated) versions of `ChannelMessage.Read.All`, `Team.ReadBasic.All`,
   `Channel.ReadBasic.All`, with admin consent. No client secret — attach the public certificate
   produced by `cmd/provision-cert` instead.
3. Copy `internal/config/config.go.example` to `internal/config/config.go`, fill in both app
   IDs, your tenant ID, the target Teams team/channel, Trello board/list, and the assignee name
   to filter on.
4. `go build -o bin/provision-cert.exe ./cmd/provision-cert && ./bin/provision-cert.exe` once,
   to generate the sync job's signing key + cert.
5. Create `~/.teams_service_identity` (service app's client ID on line 1, tenant ID on line 2)
   and `~/.trello_creds` (Trello API key on line 1, token on line 2) — kept as local files, not
   env vars, since env vars set via `setx` don't reliably reach either the MCP host's pooled
   subprocess or a Windows Task Scheduler job (both cache a stale environment snapshot).
6. `go build -o bin/teams-mcp.exe .` and register it as an MCP server (stdio transport) in your
   Claude Code config.
7. `go build -o bin/teams-trello-sync.exe ./cmd/sync`, then schedule it (Task Scheduler, cron,
   etc.) to run periodically.

## Known limitations

- Task extraction in `cmd/sync` is a regex scrape of one specific digest bot's Markdown-in-HTML
  format. It will silently drop tasks if that formatting changes — see the comment above
  `taskLineRe` in `cmd/sync/main.go`.
- `authapp`'s Windows-CNG-backed signing only works on Windows (uses `certtostore`, a CGo-free
  but Windows-syscall-based library). The delegated `auth` package and the readonly MCP binary
  are cross-platform; the sync job's app-only path is not.
