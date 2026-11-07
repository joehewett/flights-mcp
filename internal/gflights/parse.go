package gflights

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
)

// Results is one decoded page.
type Results struct {
	Best  []Itinerary `json:"best"`
	Other []Itinerary `json:"other"`
}

// All returns best then other.
func (r *Results) All() []Itinerary {
	out := make([]Itinerary, 0, len(r.Best)+len(r.Other))
	out = append(out, r.Best...)
	return append(out, r.Other...)
}

// Itinerary is one priced option. A one-way search has one journey; a
// round trip has one journey until an outbound is selected, then two.
type Itinerary struct {
	Price        float64   `json:"price"`
	Currency     string    `json:"currency"`
	Journeys     []Journey `json:"journeys"`
	BookingToken string    `json:"-"`
}

// Journey is one direction, possibly with connections.
type Journey struct {
	Origin          string    `json:"origin"`
	Destination     string    `json:"destination"`
	DepartureDate   string    `json:"departure_date"`
	DepartureTime   string    `json:"departure_time"`
	ArrivalDate     string    `json:"arrival_date"`
	ArrivalTime     string    `json:"arrival_time"`
	DurationMinutes int       `json:"duration_minutes"`
	Stops           int       `json:"stops"`
	Airlines        []string  `json:"airlines"`
	Segments        []Segment `json:"segments"`
	Layovers        []Layover `json:"layovers,omitempty"`
}

// Segment is one flight.
type Segment struct {
	Carrier         string `json:"carrier"`
	FlightNumber    string `json:"flight_number"`
	Airline         string `json:"airline"`
	OperatedBy      string `json:"operated_by,omitempty"`
	Origin          string `json:"origin"`
	Destination     string `json:"destination"`
	DepartureDate   string `json:"departure_date"`
	DepartureTime   string `json:"departure_time"`
	ArrivalDate     string `json:"arrival_date"`
	ArrivalTime     string `json:"arrival_time"`
	DurationMinutes int    `json:"duration_minutes"`
	Aircraft        string `json:"aircraft,omitempty"`
}

// Layover is time on the ground between segments.
type Layover struct {
	Airport string `json:"airport"`
	City    string `json:"city,omitempty"`
	Minutes int    `json:"minutes"`
}

// ErrNoFlights means the page rendered but listed nothing.
var ErrNoFlights = errors.New("google flights listed no flights for this search")

// ErrLayout means the embedded data was not in the shape this parser
// knows. Google changed the page, or served a different one.
var ErrLayout = errors.New("google flights page data was not in the expected layout")

const ds1Marker = "AF_initDataCallback({key: 'ds:1'"

// ExtractDS1 pulls the ds:1 data array out of a results page.
func ExtractDS1(html []byte) ([]byte, error) {
	page := string(html)
	i := strings.Index(page, ds1Marker)
	if i < 0 {
		if strings.Contains(page, "ErrorResponse") {
			return nil, ErrRejected
		}
		return nil, fmt.Errorf("%w: no ds:1 block", ErrLayout)
	}
	j := strings.Index(page[i:], "data:")
	if j < 0 {
		return nil, fmt.Errorf("%w: ds:1 has no data", ErrLayout)
	}
	start := i + j + len("data:")
	for start < len(page) && (page[start] == ' ' || page[start] == '\n') {
		start++
	}
	if start >= len(page) || page[start] != '[' {
		return nil, fmt.Errorf("%w: ds:1 data is not an array", ErrLayout)
	}
	depth, inStr := 0, false
	for k := start; k < len(page); k++ {
		c := page[k]
		if inStr {
			switch c {
			case '\\':
				k++
			case '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return []byte(page[start : k+1]), nil
			}
		}
	}
	return nil, fmt.Errorf("%w: ds:1 data is unterminated", ErrLayout)
}

// Parse decodes the ds:1 data array.
func Parse(data []byte, currency string) (*Results, error) {
	var top []any
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrLayout, err)
	}
	if status, ok := errorFrame(top); ok {
		return nil, fmt.Errorf("%w (status %d)", ErrRejected, status)
	}
	res := &Results{}
	var seenSlots bool
	for i, slot := range []int{2, 3} {
		group, ok := at(top, slot).([]any)
		if !ok {
			continue
		}
		seenSlots = true
		rows := itineraryRows(group)
		for _, row := range rows {
			it, ok := parseItinerary(row, currency)
			if !ok {
				continue
			}
			if i == 0 {
				res.Best = append(res.Best, it)
			} else {
				res.Other = append(res.Other, it)
			}
		}
	}
	if len(res.Best)+len(res.Other) == 0 {
		if seenSlots || len(top) > 7 {
			return nil, ErrNoFlights
		}
		return nil, fmt.Errorf("%w: no itinerary groups", ErrLayout)
	}
	return res, nil
}

// errorFrame detects [status, null, [[".../ErrorResponse" ...]]].
func errorFrame(top []any) (int, bool) {
	if len(top) != 3 || top[1] != nil {
		return 0, false
	}
	status, ok := asInt(top[0])
	if !ok {
		return 0, false
	}
	var found bool
	var walk func(v any, depth int)
	walk = func(v any, depth int) {
		if found || depth > 3 {
			return
		}
		switch x := v.(type) {
		case string:
			if strings.HasPrefix(x, "type.googleapis.com/") && strings.HasSuffix(x, "ErrorResponse") {
				found = true
			}
		case []any:
			for _, e := range x {
				walk(e, depth+1)
			}
		}
	}
	walk(top[2], 0)
	return status, found
}

func itineraryRows(group []any) []any {
	if len(group) == 0 {
		return nil
	}
	if first, ok := group[0].([]any); ok {
		for _, item := range first {
			if looksLikeItinerary(item) {
				return first
			}
		}
	}
	var rows []any
	for _, item := range group {
		if looksLikeItinerary(item) {
			rows = append(rows, item)
		}
	}
	return rows
}

func looksLikeItinerary(v any) bool {
	row, ok := v.([]any)
	if !ok || len(row) < 2 {
		return false
	}
	return len(journeyFlights(row[0])) > 0
}

// journeyFlights finds the flight arrays inside an itinerary head. A
// single journey is the head itself; several journeys are nested.
func journeyFlights(head any) [][]any {
	if f, ok := head.([]any); ok && looksLikeFlight(f) {
		return [][]any{f}
	}
	var found [][]any
	var walk func(v any, depth int)
	walk = func(v any, depth int) {
		list, ok := v.([]any)
		if !ok {
			return
		}
		if looksLikeFlight(list) {
			found = append(found, list)
			return
		}
		if depth >= 3 {
			return
		}
		for _, e := range list {
			walk(e, depth+1)
		}
	}
	walk(head, 0)
	return found
}

func looksLikeFlight(f []any) bool {
	if len(f) < 10 {
		return false
	}
	names, ok := f[1].([]any)
	if !ok || len(names) == 0 {
		return false
	}
	if _, ok := names[0].(string); !ok {
		return false
	}
	if _, ok := f[2].([]any); !ok {
		return false
	}
	_, ok = asInt(f[9])
	return ok
}

func parseItinerary(v any, currency string) (Itinerary, bool) {
	row := v.([]any)
	flights := journeyFlights(row[0])
	fare, _ := row[1].([]any)
	price, ok := farePrice(fare)
	if !ok {
		return Itinerary{}, false
	}
	it := Itinerary{Price: price, Currency: currency}
	if len(fare) > 1 {
		it.BookingToken, _ = fare[1].(string)
	}
	for _, f := range flights {
		it.Journeys = append(it.Journeys, parseJourney(f))
	}
	return it, true
}

func farePrice(fare []any) (float64, bool) {
	if len(fare) == 0 {
		return 0, false
	}
	pair, ok := fare[0].([]any)
	if !ok || len(pair) < 2 {
		return 0, false
	}
	n, ok := pair[1].(float64)
	if !ok || math.IsNaN(n) || n < 0 {
		return 0, false
	}
	return n, true
}

func parseJourney(f []any) Journey {
	j := Journey{
		Origin:        asString(at(f, 3)),
		Destination:   asString(at(f, 6)),
		DepartureDate: ymd(at(f, 4)),
		DepartureTime: clock(at(f, 5)),
		ArrivalDate:   ymd(at(f, 7)),
		ArrivalTime:   clock(at(f, 8)),
	}
	j.DurationMinutes, _ = asInt(at(f, 9))
	for _, n := range asList(at(f, 1)) {
		if s, ok := n.(string); ok && s != "" {
			j.Airlines = append(j.Airlines, s)
		}
	}
	for _, l := range asList(at(f, 2)) {
		leg, ok := l.([]any)
		if !ok {
			continue
		}
		j.Segments = append(j.Segments, parseSegment(leg))
	}
	if len(j.Segments) > 0 {
		j.Stops = len(j.Segments) - 1
	}
	for _, l := range asList(at(f, 13)) {
		entry, ok := l.([]any)
		if !ok || len(entry) == 0 {
			continue
		}
		mins, ok := asInt(entry[0])
		if !ok {
			continue
		}
		j.Layovers = append(j.Layovers, Layover{
			Minutes: mins,
			Airport: asString(at(entry, 1)),
			City:    asString(at(entry, 5)),
		})
	}
	// Fill gaps from segments when the summary slots are empty.
	if len(j.Segments) > 0 {
		first, last := j.Segments[0], j.Segments[len(j.Segments)-1]
		if j.Origin == "" {
			j.Origin = first.Origin
		}
		if j.Destination == "" {
			j.Destination = last.Destination
		}
		if j.DepartureDate == "" {
			j.DepartureDate = first.DepartureDate
		}
		if j.DepartureTime == "" {
			j.DepartureTime = first.DepartureTime
		}
		if j.ArrivalDate == "" {
			j.ArrivalDate = last.ArrivalDate
		}
		if j.ArrivalTime == "" {
			j.ArrivalTime = last.ArrivalTime
		}
	}
	return j
}

func parseSegment(leg []any) Segment {
	s := Segment{
		OperatedBy:    asString(at(leg, 2)),
		Origin:        asString(at(leg, 3)),
		Destination:   asString(at(leg, 6)),
		DepartureTime: clock(at(leg, 8)),
		ArrivalTime:   clock(at(leg, 10)),
		Aircraft:      asString(at(leg, 17)),
		DepartureDate: ymd(at(leg, 20)),
		ArrivalDate:   ymd(at(leg, 21)),
	}
	s.DurationMinutes, _ = asInt(at(leg, 11))
	if id := asList(at(leg, 22)); len(id) >= 2 {
		s.Carrier = asString(id[0])
		s.FlightNumber = s.Carrier + asString(id[1])
		if len(id) > 3 {
			s.Airline = asString(id[3])
		}
	}
	if s.Airline == "" {
		s.Airline = s.Carrier
	}
	return s
}

// --- loose accessors over decoded JSON ---

func at(v any, i int) any {
	list, ok := v.([]any)
	if !ok || i < 0 || i >= len(list) {
		return nil
	}
	return list[i]
}

func asList(v any) []any {
	list, _ := v.([]any)
	return list
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func asInt(v any) (int, bool) {
	f, ok := v.(float64)
	if !ok || f != math.Trunc(f) {
		return 0, false
	}
	return int(f), true
}

// ymd formats [y, m, d].
func ymd(v any) string {
	l := asList(v)
	if len(l) < 3 {
		return ""
	}
	y, ok1 := asInt(l[0])
	m, ok2 := asInt(l[1])
	d, ok3 := asInt(l[2])
	if !ok1 || !ok2 || !ok3 || y < 100 {
		return ""
	}
	return fmt.Sprintf("%04d-%02d-%02d", y, m, d)
}

// clock formats [h, m]. Google omits zero values, so [17] is 17:00 and
// [null, 15] is 00:15. Hours of 24 or more mean the next day.
func clock(v any) string {
	l := asList(v)
	if len(l) == 0 {
		return ""
	}
	h, _ := asInt(l[0])
	m := 0
	if len(l) > 1 {
		m, _ = asInt(l[1])
	}
	if h < 0 || m < 0 || m > 59 || h > 47 {
		return ""
	}
	return fmt.Sprintf("%02d:%02d", h%24, m)
}
