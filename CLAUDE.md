# mcp-server-classpass

## ClassPass API notes

- Cloudflare blocks curl over HTTP/2 with a bot challenge (403). Use `curl --http1.1` for manual probing. Go's default `net/http` transport is accepted as-is.
- Proxyman redacts `Authorization`, cookies, and the `/v2/auth/login` response body in captures. Confirm auth-related shapes against the live API.
- Probe with the token from fnox: `fnox exec -- curl --http1.1 -H "Authorization: Token $CLASSPASS_TOKEN" ...` plus the app headers in `classpass/client.go`.
