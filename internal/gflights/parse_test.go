package gflights

import (
	"errors"
	"os"
	"testing"
)

func loadFixture(t *testing.T) *Results {
	t.Helper()
	data, err := os.ReadFile("testdata/brs_lon_to_mad_sdr.json")
	if err != nil {
		t.Fatal(err)
	}
	res, err := Parse(data, "GBP")
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestParseCounts(t *testing.T) {
	res := loadFixture(t)
	if len(res.Best) != 3 || len(res.Other) != 62 {
		t.Fatalf("best=%d other=%d", len(res.Best), len(res.Other))
	}
}

func TestParseNonstop(t *testing.T) {
	it := loadFixture(t).Best[0]
	if it.Price != 48 || it.Currency != "GBP" {
		t.Fatalf("price %v %s", it.Price, it.Currency)
	}
	if len(it.Journeys) != 1 {
		t.Fatalf("journeys %d", len(it.Journeys))
	}
	j := it.Journeys[0]
	if j.Origin != "BRS" || j.Destination != "MAD" || j.DepartureTime != "08:20" || j.ArrivalTime != "11:35" || j.DepartureDate != "2026-12-27" || j.DurationMinutes != 135 || j.Stops != 0 {
		t.Fatalf("journey %+v", j)
	}
	if len(j.Segments) != 1 || j.Segments[0].FlightNumber != "FR155" || j.Segments[0].Airline != "Ryanair" || j.Segments[0].Aircraft != "Boeing 737" {
		t.Fatalf("segment %+v", j.Segments)
	}
}

func TestParseConnection(t *testing.T) {
	// Vueling LGW-BCN-MAD with an overnight 530 minute layover.
	var found *Itinerary
	for i := range loadFixture(t).Other {
		it := loadFixture(t).Other[i]
		if it.Journeys[0].Segments[0].FlightNumber == "VY7621" {
			found = &it
			break
		}
	}
	if found == nil {
		t.Fatal("VY7621 not found")
	}
	j := found.Journeys[0]
	if j.Stops != 1 || len(j.Layovers) != 1 || j.Layovers[0].Airport != "BCN" || j.Layovers[0].Minutes != 530 || j.Layovers[0].City != "Barcelona" {
		t.Fatalf("layovers %+v", j.Layovers)
	}
	if j.ArrivalDate != "2026-12-28" || j.DepartureDate != "2026-12-27" {
		t.Fatalf("dates %s -> %s", j.DepartureDate, j.ArrivalDate)
	}
}

func TestParseMidnightArrival(t *testing.T) {
	// FR5996 arrives at [null, 15], which is 00:15.
	for _, it := range loadFixture(t).Other {
		if it.Journeys[0].Segments[0].FlightNumber == "FR5996" {
			if got := it.Journeys[0].ArrivalTime; got != "00:15" {
				t.Fatalf("got %q", got)
			}
			return
		}
	}
	t.Fatal("FR5996 not found")
}

func TestParseMixesOriginsAndDestinations(t *testing.T) {
	origins, dests := map[string]bool{}, map[string]bool{}
	for _, it := range loadFixture(t).All() {
		origins[it.Journeys[0].Origin] = true
		dests[it.Journeys[0].Destination] = true
	}
	for _, c := range []string{"BRS", "STN", "LTN", "LGW"} {
		if !origins[c] {
			t.Errorf("origin %s missing", c)
		}
	}
	for _, c := range []string{"MAD", "SDR"} {
		if !dests[c] {
			t.Errorf("destination %s missing", c)
		}
	}
}

func TestParseErrorFrame(t *testing.T) {
	_, err := Parse([]byte(`[3,null,[["type.googleapis.com/travel.frontend.flights.ErrorResponse"]]]`), "GBP")
	if err == nil || !errors.Is(err, ErrRejected) {
		t.Fatalf("got %v", err)
	}
}

func TestExtractDS1(t *testing.T) {
	html := []byte(`<html><script>AF_initDataCallback({key: 'ds:0', data:[1]});</script><script>AF_initDataCallback({key: 'ds:1', hash: '2', data:[[1,"a]b\"c"],null,[[]]], sideChannel: {}});</script></html>`)
	got, err := ExtractDS1(html)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `[[1,"a]b\"c"],null,[[]]]` {
		t.Fatalf("got %s", got)
	}
}

func TestClock(t *testing.T) {
	cases := map[string]any{
		"17:00": []any{17.0},
		"00:15": []any{nil, 15.0},
		"08:20": []any{8.0, 20.0},
		"00:05": []any{24.0, 5.0},
		"":      nil,
	}
	for want, in := range cases {
		if got := clock(in); got != want {
			t.Errorf("clock(%v) = %q want %q", in, got, want)
		}
	}
}
