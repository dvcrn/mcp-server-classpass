# mcp-server-classpass

An MCP server for [ClassPass](https://classpass.com). It lets an AI assistant find classes, wellness appointments, and food near you, check prices and open times, and book or cancel with your credits.

Deploy this server directly to [MCP Nest](https://mcpnest.dev)

<a href="https://mcpnest.dev/deploy?server=mcp-server-classpass&package-manager=npx&env[CLASSPASS_USERNAME]=&env[CLASSPASS_PASSWORD]=">
    <img src="https://mcpnest.dev/images/deploy-on-mcpnest.png" alt="Deploy on MCP Nest" height="32" />
  </a>

## Tools

| Tool | What it does |
|---|---|
| `get_account` | Your plan, remaining credits, and upcoming reservation count |
| `list_reservations` | Upcoming reservations, or past ones with `include_past` |
| `search_classes` | Classes with bookable time slots near a place, over up to 4 days, filtered by activity, start time, and credit cost |
| `search_venues` | Venues by name, activity, or tag, nearest first, including food and drink |
| `search_locations` | Place lookup, to pick an exact area for a search |
| `list_tags` | ClassPass categories and tags, e.g. Food and Drink > Coffee |
| `get_venue` | Venue details, rating, recent reviews, and the classes it offers |
| `list_slots` | Upcoming time slots for one class or a whole venue |
| `get_slot` | One time slot: description, price, availability, cancellation policy |
| `book_slot` | Book a time slot with credits |
| `cancel_reservation` | Cancel a reservation; asks for confirmation before a late cancellation |
| `list_bookmarks`, `add_bookmark`, `remove_bookmark` | Manage saved venues |

Searches default to the city your ClassPass account is set to. Times are in the venue's local timezone.

## Authentication

Set one of these in the `env` of the entries below.

With your ClassPass email and password, the server logs in on first use and logs in again when the session expires:

```json
"env": { "CLASSPASS_USERNAME": "you@example.com", "CLASSPASS_PASSWORD": "your-password" }
```

With a token instead, the server never sees your password. The token is the value after `Token ` in the `Authorization` header the ClassPass app sends, which you can read with a proxy such as Proxyman:

```json
"env": { "CLASSPASS_TOKEN": "your-token" }
```

## Install

Pick one method and add its entry to `mcpServers` in your client configuration.

### npx, without installing

```json
"classpass": {
  "command": "npx",
  "args": ["-y", "mcp-server-classpass"],
  "env": { "CLASSPASS_TOKEN": "your-token" }
}
```

### npm, mise, or Go

Install with one of:

```sh
npm install -g mcp-server-classpass
mise use -g go:github.com/dvcrn/mcp-server-classpass@latest
go install github.com/dvcrn/mcp-server-classpass@latest
```

Then:

```json
"classpass": {
  "command": "mcp-server-classpass",
  "env": { "CLASSPASS_TOKEN": "your-token" }
}
```

### Binary

Download the archive for your platform from the [GitHub releases](https://github.com/dvcrn/mcp-server-classpass/releases), extract it, and use the binary's full path as `command` in the entry above.


## Development

```sh
mise run build
mise run test
mise run lint
```

## License

[AGPL-3.0](LICENSE)
