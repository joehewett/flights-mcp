// Package tfs encodes the Google Flights `tfs` URL parameter.
//
// The parameter is a base64 protobuf message. Field numbers come from
// observing the URLs the Google Flights page emits. There is no public
// schema, so each field written here has been checked against a live
// search.
package tfs

import (
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
)

// Cabin is the seat class.
type Cabin int

const (
	Economy        Cabin = 1
	PremiumEconomy Cabin = 2
	Business       Cabin = 3
	First          Cabin = 4
)

// ParseCabin maps a user string to a Cabin. Empty means Economy.
func ParseCabin(s string) (Cabin, error) {
	switch s {
	case "", "economy":
		return Economy, nil
	case "premium_economy", "premium-economy", "premium":
		return PremiumEconomy, nil
	case "business":
		return Business, nil
	case "first":
		return First, nil
	}
	return 0, fmt.Errorf("unknown cabin %q (use economy, premium_economy, business or first)", s)
}

// TripKind is the itinerary shape.
type TripKind int

const (
	RoundTrip TripKind = 1
	OneWay    TripKind = 2
	MultiCity TripKind = 3
)

// Selected names one flight the user has already chosen on a leg.
// Google then returns options that include it, which is how a deep
// link to one itinerary and a return-flight search both work.
type Selected struct {
	Origin      string
	Date        string // YYYY-MM-DD
	Destination string
	Carrier     string // IATA carrier code, e.g. FR
	Number      string // bare flight number, e.g. 155
}

// Leg is one direction of travel. From and To may each list several
// airports; Google searches every pair in one request.
type Leg struct {
	Date     string // YYYY-MM-DD
	From     []string
	To       []string
	MaxStops *int // nil means any number of stops
	Selected []Selected
}

// Query is a full search.
type Query struct {
	Legs          []Leg
	Adults        int
	Children      int
	InfantsInSeat int
	InfantsOnLap  int
	Cabin         Cabin
	Kind          TripKind
}

// Message field numbers.
const (
	fInfoLeg        = 3
	fInfoPassengers = 8
	fInfoSeat       = 9
	fInfoTrip       = 19

	fLegDate     = 2
	fLegSelected = 4
	fLegMaxStops = 5
	fLegFrom     = 13
	fLegTo       = 14

	fAirportCode = 2

	fSelOrigin  = 1
	fSelDate    = 2
	fSelDest    = 3
	fSelCarrier = 5
	fSelNumber  = 6

	passengerAdult        = 1
	passengerChild        = 2
	passengerInfantInSeat = 3
	passengerInfantOnLap  = 4
)

var (
	dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	iataRe = regexp.MustCompile(`^[A-Z0-9]{3}$`)
)

// Encode returns the base64 tfs value for q.
func Encode(q Query) (string, error) {
	if len(q.Legs) == 0 {
		return "", errors.New("tfs: query has no legs")
	}
	if q.Kind == 0 {
		return "", errors.New("tfs: trip kind not set")
	}
	adults := q.Adults
	if adults == 0 && q.Children == 0 && q.InfantsInSeat == 0 && q.InfantsOnLap == 0 {
		adults = 1
	}
	if q.Cabin == 0 {
		q.Cabin = Economy
	}
	var out []byte
	for i, leg := range q.Legs {
		b, err := encodeLeg(leg)
		if err != nil {
			return "", fmt.Errorf("tfs: leg %d: %w", i+1, err)
		}
		out = append(out, lenDelim(fInfoLeg, b)...)
	}
	var pax []byte
	pax = append(pax, repeat(passengerAdult, adults)...)
	pax = append(pax, repeat(passengerChild, q.Children)...)
	pax = append(pax, repeat(passengerInfantInSeat, q.InfantsInSeat)...)
	pax = append(pax, repeat(passengerInfantOnLap, q.InfantsOnLap)...)
	out = append(out, lenDelim(fInfoPassengers, pax)...)
	out = append(out, varintField(fInfoSeat, int(q.Cabin))...)
	out = append(out, varintField(fInfoTrip, int(q.Kind))...)
	return base64.StdEncoding.EncodeToString(out), nil
}

func encodeLeg(leg Leg) ([]byte, error) {
	if !dateRe.MatchString(leg.Date) {
		return nil, fmt.Errorf("date %q is not YYYY-MM-DD", leg.Date)
	}
	if len(leg.From) == 0 || len(leg.To) == 0 {
		return nil, errors.New("needs at least one origin and one destination")
	}
	var b []byte
	b = append(b, str(fLegDate, leg.Date)...)
	if leg.MaxStops != nil {
		if *leg.MaxStops < 0 {
			return nil, errors.New("max stops must not be negative")
		}
		b = append(b, varintField(fLegMaxStops, *leg.MaxStops)...)
	}
	for _, code := range leg.From {
		if !iataRe.MatchString(code) {
			return nil, fmt.Errorf("origin %q is not an IATA code", code)
		}
		b = append(b, lenDelim(fLegFrom, str(fAirportCode, code))...)
	}
	for _, code := range leg.To {
		if !iataRe.MatchString(code) {
			return nil, fmt.Errorf("destination %q is not an IATA code", code)
		}
		b = append(b, lenDelim(fLegTo, str(fAirportCode, code))...)
	}
	for _, s := range leg.Selected {
		if s.Origin == "" || s.Destination == "" || s.Carrier == "" || s.Number == "" || !dateRe.MatchString(s.Date) {
			return nil, fmt.Errorf("selected flight %+v is incomplete", s)
		}
		var sb []byte
		sb = append(sb, str(fSelOrigin, s.Origin)...)
		sb = append(sb, str(fSelDate, s.Date)...)
		sb = append(sb, str(fSelDest, s.Destination)...)
		sb = append(sb, str(fSelCarrier, s.Carrier)...)
		sb = append(sb, str(fSelNumber, s.Number)...)
		b = append(b, lenDelim(fLegSelected, sb)...)
	}
	return b, nil
}

func repeat(v byte, n int) []byte {
	out := make([]byte, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, v)
	}
	return out
}

const (
	wireVarint = 0
	wireLen    = 2
)

func varint(n int) []byte {
	if n < 0 {
		panic("tfs: negative varint")
	}
	var out []byte
	for {
		b := byte(n & 0x7f)
		n >>= 7
		if n != 0 {
			out = append(out, b|0x80)
			continue
		}
		return append(out, b)
	}
}

func key(field, wire int) []byte { return varint(field<<3 | wire) }

func lenDelim(field int, payload []byte) []byte {
	out := key(field, wireLen)
	out = append(out, varint(len(payload))...)
	return append(out, payload...)
}

func str(field int, s string) []byte { return lenDelim(field, []byte(s)) }

func varintField(field, v int) []byte { return append(key(field, wireVarint), varint(v)...) }
