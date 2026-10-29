# FoodTruck Bench RSS

Unofficial Atom feed for https://foodtruckbench.com/blog, which has no feed of its own. A small Go service scrapes the blog, keeps entry metadata (title, summary, author, tag, dates) in a JSON file, and serves them at `/atom.xml`. Each item links to the original post; the full text is not republished.

Public URL: https://food-truck-bench-feed.iankulin.com/atom.xml

## Configuration

| Var | Default | Notes |
|---|---|---|
| `PUBLIC_URL` | (required) | Public base URL, used for the feed `id` and `self` link |
| `SOURCE_URL` | `https://foodtruckbench.com/blog` | Listing URL |
| `POLL_INTERVAL` | `3h` | Go duration, minimum `1m` |
| `DATA_FILE` | `data/foodtruckbench.json` | Entry store (`/data/...` in the image) |
| `LISTEN_ADDR` | `:8080` | |

## Run

    docker compose up -d --build

`compose.yaml` assumes an external Docker network named `traefik`, an entrypoint `websecure` and a cert resolver `letsencrypt`; adjust to your Traefik setup.

Locally: `PUBLIC_URL=http://localhost:8080 go run .`

## Rate limiting

Each client address gets a token bucket (`RATE_LIMIT` requests/minute, default 60, burst 10; 0 disables) and receives `429` with `Retry-After` when empty. The client address is the direct peer unless that peer is within `TRUSTED_PROXIES` (default: loopback and private ranges), in which case `X-Forwarded-For` is read right to left and the first untrusted address is used, so spoofed entries added by clients are ignored. Narrow `TRUSTED_PROXIES` to your proxy's network if other containers share it. If Traefik sits behind another proxy/CDN, add that proxy's range too. IPv6 clients are bucketed per /64.

## Endpoints

- `/atom.xml`: the feed (503 until the first poll has stored something)
- `/healthz`: 200 once the store has entries. Independent of upstream availability; there is no container healthcheck because the image is distroless

## Operations

- **Volume permissions:** the image's `/data` is owned by uid 65532, so a named volume works as-is. A bind mount needs `chown 65532:65532` on the host directory.
- **Force a refresh:** delete the data file (or `docker compose down -v`) and restart. There is no refresh endpoint.
- **Behaviour:** each poll fetches the listing, fetches new articles, and refetches an article only when its listing card has changed (silent edits that leave the card alone are not picked up). A failed listing fetch (error, or zero valid cards) never touches the store. Entries that disappear from the listing are kept.
- **Dates** come from each article's `BlogPosting` JSON-LD. Many articles have none, so the page-header date is used instead (month precision, treated as the 1st of the month UTC, flagged `date_approx` in the store). A later refetch that finds JSON-LD upgrades the date; otherwise `Published` is frozen once set. An article with neither is skipped and retried every poll.

## Development

    go vet ./... && go test -race ./...

## Legal
- AI tools have been used in the production of this software
- MIT licence
- No affiliation with foodtruckbench.com, check their terms before using
- there's probably a public instance at foodtruckbf.iankulin.com/atom.xml
