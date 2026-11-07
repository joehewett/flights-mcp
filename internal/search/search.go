// Package search turns a loose request (several origins, several
// destinations, a date window) into Google Flights queries, runs them
// concurrently, and merges the answers into one ranked list.
package search

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/joehewett/flights-mcp/internal/airports"
	"github.com/joehewett/flights-mcp/internal/gflights"
	"github.com/joehewett/flights-mcp/internal/tfs"
)

// Fetcher is what Run needs from a Google Flights client.
type Fetcher interface {
	Search(ctx context.Context, tfs, currency string) (*gflights.Results, error)
	SearchURL(tfs, currency string) string
}

// Request is a flight search in user terms.
type Request struct {
	Origins      []string
	Destinations []string
	DepartFrom   string // YYYY-MM-DD
	DepartTo     string // YYYY-MM-DD, inclusive; empty means DepartFrom
	ReturnFrom   string // optional
	ReturnTo     string // optional, inclusive; empty means ReturnFrom
	// OutboundFlights picks an outbound by flight numbers (e.g. FR155)
	// so a round-trip search returns the matching return flights. It
	// needs exactly one departure date.
	OutboundFlights []string

	Adults        int
	Children      int
	InfantsInSeat int
	InfantsOnLap  int
	Cabin         string
	MaxStops      *int
	Currency      string
	MaxResults    int
	Sort          string // price (default), duration or departure
	Concurrency   int
}

// Offer is one ranked result.
type Offer struct {
	Rank     int                `json:"rank"`
	Price    float64            `json:"price"`
	Currency string             `json:"currency"`
	Summary  string             `json:"summary"`
	Journeys []gflights.Journey `json:"journeys"`
	BookURL  string             `json:"book_url"`
}

// Response is the merged answer.
type Response struct {
	TripType      string   `json:"trip_type"`
	Origins       []string `json:"origins"`
	Destinations  []string `json:"destinations"`
	DatesSearched []string `json:"dates_searched"`
	Passengers    int      `json:"passengers"`
	PriceNote     string   `json:"price_note"`
	TotalFound    int      `json:"total_found"`
	Offers        []Offer  `json:"offers"`
	Errors        []string `json:"errors,omitempty"`
	Note          string   `json:"note,omitempty"`
}

const (
	maxDepartureDays      = 14
	maxReturnDays         = 14
	maxQueries            = 40
	maxAirportsPerSide    = 7
	maxOutboundCandidates = 2000
)

type dateCombo struct {
	depart string
	ret    string
}

// Run executes the request.
func Run(ctx context.Context, f Fetcher, req Request) (*Response, error) {
	origins, err := resolveAll(req.Origins, "origin")
	if err != nil {
		return nil, err
	}
	dests, err := resolveAll(req.Destinations, "destination")
	if err != nil {
		return nil, err
	}
	for _, o := range origins {
		for _, d := range dests {
			if o == d {
				return nil, fmt.Errorf("%s is both an origin and a destination", o)
			}
		}
	}
	combos, err := dateCombos(req)
	if err != nil {
		return nil, err
	}
	cabin, err := tfs.ParseCabin(req.Cabin)
	if err != nil {
		return nil, err
	}
	if req.Currency == "" {
		req.Currency = "GBP"
	}
	req.Currency = strings.ToUpper(req.Currency)
	if req.Adults == 0 && req.Children == 0 && req.InfantsInSeat == 0 && req.InfantsOnLap == 0 {
		req.Adults = 1
	}
	if req.MaxResults <= 0 {
		req.MaxResults = 25
	}
	if req.Concurrency <= 0 {
		req.Concurrency = 4
	}
	roundTrip := req.ReturnFrom != ""
	if len(req.OutboundFlights) > 0 && (!roundTrip || len(combos) != 1) {
		return nil, errors.New("outbound_flights needs a round trip with one departure date and one return date")
	}

	originChunks := chunk(origins, maxAirportsPerSide)
	destChunks := chunk(dests, maxAirportsPerSide)
	type job struct {
		combo dateCombo
		from  []string
		to    []string
	}
	var jobs []job
	for _, c := range combos {
		for _, oc := range originChunks {
			for _, dc := range destChunks {
				jobs = append(jobs, job{c, oc, dc})
			}
		}
	}
	if len(jobs) > maxQueries {
		return nil, fmt.Errorf("this search needs %d Google Flights queries; narrow the dates or airports to stay under %d", len(jobs), maxQueries)
	}

	base := tfs.Query{
		Adults: req.Adults, Children: req.Children,
		InfantsInSeat: req.InfantsInSeat, InfantsOnLap: req.InfantsOnLap,
		Cabin: cabin, Kind: tfs.OneWay,
	}
	if roundTrip {
		base.Kind = tfs.RoundTrip
	}
	buildQuery := func(j job, selected []tfs.Selected) tfs.Query {
		q := base
		q.Legs = []tfs.Leg{{Date: j.combo.depart, From: j.from, To: j.to, MaxStops: req.MaxStops, Selected: selected}}
		if roundTrip {
			q.Legs = append(q.Legs, tfs.Leg{Date: j.combo.ret, From: j.to, To: j.from, MaxStops: req.MaxStops})
		}
		return q
	}

	// A chosen outbound: find it first, then ask for its returns.
	var (
		selected []tfs.Selected
		outbound *gflights.Journey
	)
	if len(req.OutboundFlights) > 0 {
		outbound, selected, err = findOutbound(ctx, f, buildQuery(jobs[0], nil), req.Currency, req.OutboundFlights)
		if err != nil {
			return nil, err
		}
	}

	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		sem  = make(chan struct{}, req.Concurrency)
		its  []gflights.Itinerary
		errs []string
	)
	for _, j := range jobs {
		j := j
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			label := fmt.Sprintf("%s -> %s on %s", strings.Join(j.from, "/"), strings.Join(j.to, "/"), j.combo.depart)
			if j.combo.ret != "" {
				label += " returning " + j.combo.ret
			}
			enc, err := tfs.Encode(buildQuery(j, selected))
			if err != nil {
				mu.Lock()
				errs = append(errs, label+": "+err.Error())
				mu.Unlock()
				return
			}
			res, err := f.Search(ctx, enc, req.Currency)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if errors.Is(err, gflights.ErrNoFlights) {
					return
				}
				errs = append(errs, label+": "+err.Error())
				return
			}
			for _, it := range res.All() {
				if outbound != nil && len(it.Journeys) == 1 {
					// The page lists returns for the chosen outbound; show both.
					it.Journeys = []gflights.Journey{*outbound, it.Journeys[0]}
				}
				its = append(its, it)
			}
		}()
	}
	wg.Wait()

	its = dedupe(its)
	sortOffers(its, req.Sort)
	resp := &Response{
		TripType:     "one_way",
		Origins:      labels(origins),
		Destinations: labels(dests),
		Passengers:   req.Adults + req.Children + req.InfantsInSeat + req.InfantsOnLap,
		TotalFound:   len(its),
		Errors:       errs,
	}
	if roundTrip {
		resp.TripType = "round_trip"
	}
	resp.PriceNote = fmt.Sprintf("Prices are the total for all %d passenger(s) in %s as shown by Google Flights, not per person.", resp.Passengers, req.Currency)
	if roundTrip && len(selected) == 0 {
		resp.PriceNote += " Each round-trip price covers the outbound shown plus the cheapest matching return; call again with outbound_flights set to see the return options for one outbound."
	}
	for _, c := range combos {
		s := c.depart
		if c.ret != "" {
			s += " / " + c.ret
		}
		resp.DatesSearched = append(resp.DatesSearched, s)
	}
	if len(its) > req.MaxResults {
		resp.Note = fmt.Sprintf("Showing the first %d of %d results after sorting by %s.", req.MaxResults, len(its), sortName(req.Sort))
		its = its[:req.MaxResults]
	}
	for i, it := range its {
		resp.Offers = append(resp.Offers, Offer{
			Rank:     i + 1,
			Price:    it.Price,
			Currency: it.Currency,
			Summary:  summarize(it),
			Journeys: it.Journeys,
			BookURL:  bookURL(f, base, req, it),
		})
	}
	if len(resp.Offers) == 0 && len(errs) == 0 {
		resp.Note = "Google Flights listed no flights for these airports and dates."
	}
	return resp, nil
}

func findOutbound(ctx context.Context, f Fetcher, q tfs.Query, currency string, flights []string) (*gflights.Journey, []tfs.Selected, error) {
	enc, err := tfs.Encode(q)
	if err != nil {
		return nil, nil, err
	}
	res, err := f.Search(ctx, enc, currency)
	if err != nil {
		return nil, nil, fmt.Errorf("looking up the outbound: %w", err)
	}
	want := make([]string, len(flights))
	for i, fl := range flights {
		want[i] = strings.ToUpper(strings.ReplaceAll(fl, " ", ""))
	}
	for _, it := range res.All() {
		if len(it.Journeys) == 0 {
			continue
		}
		segs := it.Journeys[0].Segments
		if len(segs) != len(want) {
			continue
		}
		match := true
		for i, s := range segs {
			if s.FlightNumber != want[i] {
				match = false
				break
			}
		}
		if match {
			j := it.Journeys[0]
			return &j, selectedFrom(segs), nil
		}
	}
	return nil, nil, fmt.Errorf("no outbound in the results matches flights %s", strings.Join(flights, ", "))
}

func selectedFrom(segs []gflights.Segment) []tfs.Selected {
	out := make([]tfs.Selected, 0, len(segs))
	for _, s := range segs {
		out = append(out, tfs.Selected{
			Origin: s.Origin, Date: s.DepartureDate, Destination: s.Destination,
			Carrier: s.Carrier, Number: strings.TrimPrefix(s.FlightNumber, s.Carrier),
		})
	}
	return out
}

// bookURL is a Google Flights link with this itinerary's flights already
// selected, which lands on its booking options.
func bookURL(f Fetcher, base tfs.Query, req Request, it gflights.Itinerary) string {
	q := base
	for i, j := range it.Journeys {
		leg := tfs.Leg{Date: j.DepartureDate, From: []string{j.Origin}, To: []string{j.Destination}, Selected: selectedFrom(j.Segments)}
		if leg.Date == "" && len(j.Segments) > 0 {
			leg.Date = j.Segments[0].DepartureDate
		}
		q.Legs = append(q.Legs, leg)
		if i == 0 && base.Kind == tfs.RoundTrip && len(it.Journeys) == 1 {
			// Return not chosen yet: point the link at the return list.
			q.Legs = append(q.Legs, tfs.Leg{Date: req.ReturnFrom, From: []string{j.Destination}, To: []string{j.Origin}})
		}
	}
	enc, err := tfs.Encode(q)
	if err != nil {
		return ""
	}
	return f.SearchURL(enc, req.Currency)
}

func resolveAll(terms []string, what string) ([]string, error) {
	if len(terms) == 0 {
		return nil, fmt.Errorf("at least one %s is required", what)
	}
	seen := map[string]bool{}
	var out []string
	for _, t := range terms {
		codes, err := airports.Resolve(t)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", what, err)
		}
		for _, c := range codes {
			if !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
	}
	return out, nil
}

func labels(codes []string) []string {
	out := make([]string, len(codes))
	for i, c := range codes {
		out[i] = airports.Label(c)
	}
	return out
}

func chunk(items []string, size int) [][]string {
	var out [][]string
	for len(items) > size {
		out = append(out, items[:size])
		items = items[size:]
	}
	return append(out, items)
}

func parseDay(s, what string) (time.Time, error) {
	d, err := time.Parse("2006-01-02", strings.TrimSpace(s))
	if err != nil {
		return time.Time{}, fmt.Errorf("%s %q must be YYYY-MM-DD", what, s)
	}
	return d, nil
}

func dateRange(from, to, what string, maxDays int) ([]string, error) {
	if from == "" {
		return nil, fmt.Errorf("%s date is required", what)
	}
	start, err := parseDay(from, what)
	if err != nil {
		return nil, err
	}
	end := start
	if to != "" {
		if end, err = parseDay(to, what+" end"); err != nil {
			return nil, err
		}
	}
	if end.Before(start) {
		return nil, fmt.Errorf("%s window ends before it starts", what)
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	if start.Before(today) {
		return nil, fmt.Errorf("%s date %s is in the past (today is %s)", what, from, today.Format("2006-01-02"))
	}
	var days []string
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		days = append(days, d.Format("2006-01-02"))
		if len(days) > maxDays {
			return nil, fmt.Errorf("%s window is longer than %d days", what, maxDays)
		}
	}
	return days, nil
}

func dateCombos(req Request) ([]dateCombo, error) {
	departs, err := dateRange(req.DepartFrom, req.DepartTo, "departure", maxDepartureDays)
	if err != nil {
		return nil, err
	}
	if req.ReturnFrom == "" {
		if req.ReturnTo != "" {
			return nil, errors.New("return_date_end was given without return_date")
		}
		var out []dateCombo
		for _, d := range departs {
			out = append(out, dateCombo{depart: d})
		}
		return out, nil
	}
	returns, err := dateRange(req.ReturnFrom, req.ReturnTo, "return", maxReturnDays)
	if err != nil {
		return nil, err
	}
	var out []dateCombo
	for _, d := range departs {
		for _, r := range returns {
			if r < d {
				continue
			}
			out = append(out, dateCombo{depart: d, ret: r})
		}
	}
	if len(out) == 0 {
		return nil, errors.New("every return date is before every departure date")
	}
	return out, nil
}

func itineraryKey(it gflights.Itinerary) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%.2f|", it.Price)
	for _, j := range it.Journeys {
		for _, s := range j.Segments {
			b.WriteString(s.FlightNumber)
			b.WriteByte('@')
			b.WriteString(s.DepartureDate)
			b.WriteByte(',')
		}
		b.WriteByte(';')
	}
	return b.String()
}

func dedupe(its []gflights.Itinerary) []gflights.Itinerary {
	seen := map[string]bool{}
	out := its[:0]
	for _, it := range its {
		k := itineraryKey(it)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, it)
	}
	return out
}

func totalDuration(it gflights.Itinerary) int {
	n := 0
	for _, j := range it.Journeys {
		n += j.DurationMinutes
	}
	return n
}

func departureKey(it gflights.Itinerary) string {
	if len(it.Journeys) == 0 {
		return ""
	}
	return it.Journeys[0].DepartureDate + " " + it.Journeys[0].DepartureTime
}

func sortName(s string) string {
	switch s {
	case "duration", "departure":
		return s
	}
	return "price"
}

func sortOffers(its []gflights.Itinerary, by string) {
	sort.SliceStable(its, func(i, j int) bool {
		a, b := its[i], its[j]
		switch sortName(by) {
		case "duration":
			if totalDuration(a) != totalDuration(b) {
				return totalDuration(a) < totalDuration(b)
			}
		case "departure":
			if departureKey(a) != departureKey(b) {
				return departureKey(a) < departureKey(b)
			}
		}
		if a.Price != b.Price {
			return a.Price < b.Price
		}
		if totalDuration(a) != totalDuration(b) {
			return totalDuration(a) < totalDuration(b)
		}
		return departureKey(a) < departureKey(b)
	})
}

func fmtDuration(mins int) string {
	h, m := mins/60, mins%60
	switch {
	case h == 0:
		return fmt.Sprintf("%dm", m)
	case m == 0:
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh%02dm", h, m)
}

func summarize(it gflights.Itinerary) string {
	var parts []string
	for _, j := range it.Journeys {
		stops := "nonstop"
		switch {
		case j.Stops == 1:
			stops = "1 stop"
		case j.Stops > 1:
			stops = fmt.Sprintf("%d stops", j.Stops)
		}
		if len(j.Layovers) > 0 {
			var via []string
			for _, l := range j.Layovers {
				via = append(via, fmt.Sprintf("%s %s", l.Airport, fmtDuration(l.Minutes)))
			}
			stops += " via " + strings.Join(via, ", ")
		}
		var flights []string
		for _, s := range j.Segments {
			flights = append(flights, s.FlightNumber)
		}
		arr := j.ArrivalTime
		if j.ArrivalDate != "" && j.ArrivalDate != j.DepartureDate {
			arr += " (+1 day)"
		}
		parts = append(parts, fmt.Sprintf("%s %s %s -> %s %s, %s, %s, %s %s",
			j.DepartureDate, j.Origin, j.DepartureTime, j.Destination, arr,
			fmtDuration(j.DurationMinutes), stops, strings.Join(j.Airlines, "/"), strings.Join(flights, "+")))
	}
	return fmt.Sprintf("%s: %.0f %s", strings.Join(parts, " | "), it.Price, it.Currency)
}
