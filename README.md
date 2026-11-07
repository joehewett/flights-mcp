# flights-mcp

An MCP server that searches flights on Google Flights. One Go binary, no
API key, no account.

Ask Claude:

> Find me a flight to Spain from Bristol or London to Madrid or Santander
> around 26 to 28 December.

Claude calls `search_flights` once with both origins, both destinations
and the three-day window. The server runs one Google Flights request per
date, merges the results and returns them ranked by price with a booking
link per offer.

## Install

```sh
make install
```

This builds `bin/flights-mcp` and registers it with Claude Code for all
projects (`claude mcp add --scope user flights -- <path>`). For another
MCP client, point it at the binary with no arguments.

## Tools

`search_flights`

| Field | Meaning |
| --- | --- |
| `origins`, `destinations` | IATA codes, metro codes (LON) or city names. Several per side. |
| `departure_date`, `departure_date_end` | A date or an inclusive window of up to 14 days. |
| `return_date`, `return_date_end` | Same for the return. Empty means one way. |
| `outbound_flights` | Round trips: flight numbers of a chosen outbound, to list its returns. |
| `adults`, `children`, `infants_in_seat`, `infants_on_lap` | Passengers. Default one adult. |
| `cabin`, `max_stops`, `currency`, `max_results`, `sort` | Filters. Sort by price, duration or departure. |

Each offer has a one-line `summary`, full `journeys` with every segment,
and a `book_url`. Prices are the total for all passengers.

`lookup_airports` finds codes by city or airport name. It works offline
from an embedded OurAirports table.

## Booking

The server does not buy tickets. `book_url` opens Google Flights with
that itinerary selected, which lists the airline and agency booking
options. Click through to pay.

## Command line

```sh
bin/flights-mcp search --from bristol,london --to madrid,santander \
  --date 2026-12-26 --date-end 2026-12-28
bin/flights-mcp search --from BRS --to MAD --date 2026-12-27 --return 2027-01-03
bin/flights-mcp search --from BRS --to MAD --date 2026-12-27 --return 2027-01-03 --outbound FR155
bin/flights-mcp airports santander
```

Add `--json` for the same structure the MCP tool returns.

## How it works

Google Flights encodes a search in a `tfs` URL parameter: a base64
protobuf with dates, airports, passengers and cabin. `internal/tfs`
writes that message. `internal/gflights` requests the public results
page and reads the `ds:1` data the page embeds for its own script.
`internal/search` fans a request out across dates and airport groups and
merges the answers.

There is no public schema for either side. The field numbers and array
positions come from observing live pages, and `internal/gflights/testdata`
holds a captured page for the parser tests. When Google changes the
page, searches fail with a layout error rather than returning wrong data.

## Limits

- Google may rate-limit a machine that searches heavily. The server runs
  at most four requests at once and at most 40 per call.
- Prices are what Google displayed at that moment, not a held fare.
