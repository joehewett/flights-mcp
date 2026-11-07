package search

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/joehewett/flights-mcp/internal/gflights"
)

type fake struct {
	mu      sync.Mutex
	calls   []string
	results map[string]*gflights.Results // keyed by substring of tfs
	all     *gflights.Results
}

func (f *fake) Search(_ context.Context, tfs, _ string) (*gflights.Results, error) {
	f.mu.Lock()
	f.calls = append(f.calls, tfs)
	f.mu.Unlock()
	if f.all != nil {
		return f.all, nil
	}
	return nil, gflights.ErrNoFlights
}

func (f *fake) SearchURL(tfs, currency string) string { return "https://example/?tfs=" + tfs }

func seg(num, from, to, date, dep, arr string) gflights.Segment {
	return gflights.Segment{Carrier: num[:2], FlightNumber: num, Airline: "X", Origin: from, Destination: to, DepartureDate: date, DepartureTime: dep, ArrivalDate: date, ArrivalTime: arr, DurationMinutes: 120}
}

func it(price float64, segs ...gflights.Segment) gflights.Itinerary {
	j := gflights.Journey{Origin: segs[0].Origin, Destination: segs[len(segs)-1].Destination, DepartureDate: segs[0].DepartureDate, DepartureTime: segs[0].DepartureTime, ArrivalDate: segs[len(segs)-1].ArrivalDate, ArrivalTime: segs[len(segs)-1].ArrivalTime, DurationMinutes: 120 * len(segs), Stops: len(segs) - 1, Airlines: []string{"X"}, Segments: segs}
	return gflights.Itinerary{Price: price, Currency: "GBP", Journeys: []gflights.Journey{j}}
}

func TestRunFansOutOverDatesAndMerges(t *testing.T) {
	f := &fake{all: &gflights.Results{
		Best:  []gflights.Itinerary{it(48, seg("FR155", "BRS", "MAD", "2027-12-27", "08:20", "11:35"))},
		Other: []gflights.Itinerary{it(96, seg("FR2612", "STN", "SDR", "2027-12-27", "13:15", "16:10")), it(48, seg("FR155", "BRS", "MAD", "2027-12-27", "08:20", "11:35"))},
	}}
	resp, err := Run(context.Background(), f, Request{
		Origins: []string{"bristol", "london"}, Destinations: []string{"MAD", "santander"},
		DepartFrom: "2027-12-26", DepartTo: "2027-12-28",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 3 {
		t.Fatalf("expected one query per date, got %d", len(f.calls))
	}
	if resp.TotalFound != 2 {
		t.Fatalf("dedupe failed: %d offers", resp.TotalFound)
	}
	if resp.Offers[0].Price != 48 || resp.Offers[1].Price != 96 {
		t.Fatalf("not sorted by price: %+v", resp.Offers)
	}
	if !strings.Contains(resp.Offers[0].Summary, "FR155") || !strings.Contains(resp.Offers[0].BookURL, "tfs=") {
		t.Fatalf("summary/book url: %+v", resp.Offers[0])
	}
	if len(resp.Origins) != 7 || resp.Origins[0] != "BRS (Bristol)" {
		t.Fatalf("origins %v", resp.Origins)
	}
}

func TestRunRejectsBadInput(t *testing.T) {
	f := &fake{}
	bad := []Request{
		{Origins: []string{"BRS"}, Destinations: []string{"MAD"}, DepartFrom: "2020-01-01"},
		{Origins: []string{"BRS"}, Destinations: []string{"MAD"}, DepartFrom: "2027-01-01", DepartTo: "2027-02-01"},
		{Origins: []string{"BRS"}, Destinations: []string{"BRS"}, DepartFrom: "2027-01-01"},
		{Origins: []string{"nowhere-at-all"}, Destinations: []string{"MAD"}, DepartFrom: "2027-01-01"},
		{Origins: []string{"BRS"}, Destinations: []string{"MAD"}, DepartFrom: "2027-01-01", OutboundFlights: []string{"FR155"}},
		{Origins: []string{"BRS"}, Destinations: []string{"MAD"}, DepartFrom: "2027-01-10", ReturnFrom: "2027-01-05"},
	}
	for i, r := range bad {
		if _, err := Run(context.Background(), f, r); err == nil {
			t.Errorf("case %d: expected error", i)
		}
	}
}

func TestRunRoundTripCombos(t *testing.T) {
	f := &fake{}
	resp, err := Run(context.Background(), f, Request{
		Origins: []string{"BRS"}, Destinations: []string{"MAD"},
		DepartFrom: "2027-12-26", DepartTo: "2027-12-27", ReturnFrom: "2027-12-30", ReturnTo: "2027-12-31",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 4 || resp.TripType != "round_trip" || len(resp.DatesSearched) != 4 {
		t.Fatalf("calls=%d type=%s dates=%v", len(f.calls), resp.TripType, resp.DatesSearched)
	}
}
