// Command flights-mcp is an MCP server for flight search over Google
// Flights. With no arguments it serves MCP on stdio. It also has
// `search` and `airports` subcommands for use from a terminal.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/joehewett/flights-mcp/internal/airports"
	"github.com/joehewett/flights-mcp/internal/gflights"
	"github.com/joehewett/flights-mcp/internal/search"
)

const version = "0.1.0"

func main() {
	log.SetFlags(0)
	log.SetOutput(os.Stderr)
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "search":
			runSearchCLI(os.Args[2:])
			return
		case "airports":
			runAirportsCLI(os.Args[2:])
			return
		case "serve":
		case "version":
			fmt.Println(version)
			return
		default:
			log.Fatalf("usage: flights-mcp [serve|search|airports|version]")
		}
	}
	if err := serve(); err != nil {
		log.Fatal(err)
	}
}

// SearchInput is the MCP schema for search_flights.
type SearchInput struct {
	Origins          []string `json:"origins" jsonschema:"Departure airports. IATA codes (BRS), metro codes (LON) or city names (bristol, london). Several are searched together."`
	Destinations     []string `json:"destinations" jsonschema:"Arrival airports in the same forms as origins."`
	DepartureDate    string   `json:"departure_date" jsonschema:"First departure date, YYYY-MM-DD."`
	DepartureDateEnd string   `json:"departure_date_end,omitempty" jsonschema:"Last departure date to try, YYYY-MM-DD, inclusive. Every day from departure_date to this date is searched (at most 14)."`
	ReturnDate       string   `json:"return_date,omitempty" jsonschema:"Return date for a round trip, YYYY-MM-DD. Leave empty for one way."`
	ReturnDateEnd    string   `json:"return_date_end,omitempty" jsonschema:"Last return date to try, inclusive (at most 14 days)."`
	OutboundFlights  []string `json:"outbound_flights,omitempty" jsonschema:"Round trips only: flight numbers of a chosen outbound (e.g. [\"FR155\"]) to get its return options. Needs one departure date and one return date."`
	Adults           int      `json:"adults,omitempty" jsonschema:"Adult passengers, default 1."`
	Children         int      `json:"children,omitempty" jsonschema:"Children aged 2 to 11."`
	InfantsInSeat    int      `json:"infants_in_seat,omitempty" jsonschema:"Infants with their own seat."`
	InfantsOnLap     int      `json:"infants_on_lap,omitempty" jsonschema:"Infants on a lap."`
	Cabin            string   `json:"cabin,omitempty" jsonschema:"economy (default), premium_economy, business or first."`
	MaxStops         *int     `json:"max_stops,omitempty" jsonschema:"0 for nonstop only, 1 for at most one stop, and so on. Default: any."`
	Currency         string   `json:"currency,omitempty" jsonschema:"ISO currency for prices, default GBP."`
	MaxResults       int      `json:"max_results,omitempty" jsonschema:"How many offers to return after sorting, default 25."`
	Sort             string   `json:"sort,omitempty" jsonschema:"price (default), duration or departure."`
}

// AirportsInput is the MCP schema for lookup_airports.
type AirportsInput struct {
	Query string `json:"query" jsonschema:"An IATA code, city or airport name, e.g. 'santander' or 'LON'."`
}

// AirportsOutput lists matches.
type AirportsOutput struct {
	Matches []airports.Airport `json:"matches"`
	Metro   []string           `json:"metro_members,omitempty"`
	Note    string             `json:"note,omitempty"`
}

func toRequest(in SearchInput) search.Request {
	return search.Request{
		Origins: in.Origins, Destinations: in.Destinations,
		DepartFrom: in.DepartureDate, DepartTo: in.DepartureDateEnd,
		ReturnFrom: in.ReturnDate, ReturnTo: in.ReturnDateEnd,
		OutboundFlights: in.OutboundFlights,
		Adults:          in.Adults, Children: in.Children, InfantsInSeat: in.InfantsInSeat, InfantsOnLap: in.InfantsOnLap,
		Cabin: in.Cabin, MaxStops: in.MaxStops, Currency: in.Currency,
		MaxResults: in.MaxResults, Sort: in.Sort,
	}
}

func serve() error {
	client := gflights.New()
	server := mcp.NewServer(&mcp.Implementation{Name: "flights", Version: version}, &mcp.ServerOptions{
		Instructions: "Search flights on Google Flights (no account needed). Use search_flights with several origins, destinations and a date window in one call. Prices are totals for all passengers. The server cannot complete a purchase: give the person each offer's book_url, which opens Google Flights with that itinerary selected and its booking options listed.",
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_flights",
		Description: "Search Google Flights for one-way or round-trip fares. Accepts several origins, several destinations and a departure date window in one call, merges everything and ranks it. Returns a summary line, full segments and a book_url per offer. Prices are the total for all passengers.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolp(true)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in SearchInput) (*mcp.CallToolResult, *search.Response, error) {
		resp, err := search.Run(ctx, client, toRequest(in))
		if err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil, nil
		}
		return textResult(resp), resp, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "lookup_airports",
		Description: "Find IATA airport codes by city or airport name, or expand a metro code such as LON. Offline. search_flights already accepts city names, so this is for checking which airports a name covers.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(_ context.Context, _ *mcp.CallToolRequest, in AirportsInput) (*mcp.CallToolResult, *AirportsOutput, error) {
		out := lookup(in.Query)
		return textResult(out), out, nil
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return server.Run(ctx, &mcp.StdioTransport{})
}

func lookup(query string) *AirportsOutput {
	out := &AirportsOutput{}
	if members, ok := airports.Metros[strings.ToUpper(strings.TrimSpace(query))]; ok {
		out.Metro = members
		for _, m := range members {
			if a, ok := airports.Get(m); ok {
				out.Matches = append(out.Matches, a)
			}
		}
		return out
	}
	out.Matches = airports.Search(query, 10)
	if len(out.Matches) == 0 {
		out.Note = "No airport matched. Try the city name in English or a different spelling."
	}
	return out
}

func textResult(v any) *mcp.CallToolResult {
	b, _ := json.MarshalIndent(v, "", "  ")
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}
}

func boolp(b bool) *bool { return &b }

func runSearchCLI(args []string) {
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	from := fs.String("from", "", "origins, comma separated (codes or city names)")
	to := fs.String("to", "", "destinations, comma separated")
	date := fs.String("date", "", "departure date YYYY-MM-DD")
	dateEnd := fs.String("date-end", "", "last departure date to try")
	ret := fs.String("return", "", "return date for a round trip")
	retEnd := fs.String("return-end", "", "last return date to try")
	outbound := fs.String("outbound", "", "chosen outbound flight numbers, comma separated (round trip)")
	adults := fs.Int("adults", 1, "adults")
	children := fs.Int("children", 0, "children")
	cabin := fs.String("cabin", "economy", "cabin")
	stops := fs.Int("max-stops", -1, "max stops, -1 for any")
	currency := fs.String("currency", "GBP", "currency")
	limit := fs.Int("limit", 25, "max results")
	sortBy := fs.String("sort", "price", "price, duration or departure")
	asJSON := fs.Bool("json", false, "print JSON")
	fs.Parse(args)

	in := SearchInput{
		Origins: splitList(*from), Destinations: splitList(*to),
		DepartureDate: *date, DepartureDateEnd: *dateEnd, ReturnDate: *ret, ReturnDateEnd: *retEnd,
		OutboundFlights: splitList(*outbound),
		Adults:          *adults, Children: *children, Cabin: *cabin, Currency: *currency, MaxResults: *limit, Sort: *sortBy,
	}
	if *stops >= 0 {
		in.MaxStops = stops
	}
	resp, err := search.Run(context.Background(), gflights.New(), toRequest(in))
	if err != nil {
		log.Fatal(err)
	}
	if *asJSON {
		b, _ := json.MarshalIndent(resp, "", "  ")
		fmt.Println(string(b))
		return
	}
	fmt.Printf("%s from %s to %s on %s\n%s\n\n", resp.TripType, strings.Join(resp.Origins, ", "), strings.Join(resp.Destinations, ", "), strings.Join(resp.DatesSearched, ", "), resp.PriceNote)
	for _, o := range resp.Offers {
		fmt.Printf("%2d. %s\n    %s\n", o.Rank, o.Summary, o.BookURL)
	}
	if resp.Note != "" {
		fmt.Println(resp.Note)
	}
	for _, e := range resp.Errors {
		fmt.Fprintln(os.Stderr, "error:", e)
	}
}

func runAirportsCLI(args []string) {
	if len(args) == 0 {
		log.Fatal("usage: flights-mcp airports <query>")
	}
	out := lookup(strings.Join(args, " "))
	for _, a := range out.Matches {
		fmt.Printf("%s  %-45s %s, %s (%s)\n", a.IATA, a.Name, a.City, a.Country, a.Size)
	}
	if out.Note != "" {
		fmt.Println(out.Note)
	}
}

func splitList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
