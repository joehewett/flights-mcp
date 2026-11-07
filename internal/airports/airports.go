// Package airports is an offline IATA lookup built from the OurAirports
// dataset (public domain). It covers airports with scheduled service.
package airports

import (
	"bytes"
	_ "embed"
	"encoding/csv"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
)

//go:embed airports.csv
var raw []byte

// Airport is one row of the table.
type Airport struct {
	IATA    string  `json:"iata"`
	Name    string  `json:"name"`
	City    string  `json:"city"`
	Country string  `json:"country"`
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
	Size    string  `json:"size"` // large, medium or small
}

// Metros maps IATA metropolitan codes to member airports. Google accepts
// several airports per side of a search, so a metro expands client-side.
var Metros = map[string][]string{
	"BJS": {"PEK", "PKX"},
	"BUE": {"EZE", "AEP"},
	"CHI": {"ORD", "MDW"},
	"JKT": {"CGK", "HLP"},
	"LON": {"LHR", "LGW", "STN", "LTN", "LCY", "SEN"},
	"MIL": {"MXP", "LIN", "BGY"},
	"MOW": {"SVO", "DME", "VKO"},
	"NYC": {"JFK", "EWR", "LGA"},
	"OSA": {"KIX", "ITM"},
	"PAR": {"CDG", "ORY"},
	"REK": {"KEF", "RKV"},
	"RIO": {"GIG", "SDU"},
	"ROM": {"FCO", "CIA"},
	"SAO": {"GRU", "CGH", "VCP"},
	"SEL": {"ICN", "GMP"},
	"STO": {"ARN", "BMA"},
	"TYO": {"NRT", "HND"},
	"WAS": {"IAD", "DCA", "BWI"},
	"YMQ": {"YUL", "YMX"},
	"YTO": {"YYZ", "YTZ"},
}

// metroByCity lets a city name resolve to its whole metro.
var metroByCity = map[string]string{
	"beijing": "BJS", "buenos aires": "BUE", "chicago": "CHI", "jakarta": "JKT",
	"london": "LON", "milan": "MIL", "moscow": "MOW", "new york": "NYC",
	"osaka": "OSA", "paris": "PAR", "reykjavik": "REK", "rio de janeiro": "RIO",
	"rome": "ROM", "sao paulo": "SAO", "são paulo": "SAO", "seoul": "SEL",
	"stockholm": "STO", "tokyo": "TYO", "washington": "WAS", "montreal": "YMQ",
	"toronto": "YTO",
}

var (
	once   sync.Once
	all    []Airport
	byIATA map[string]Airport
)

func load() {
	once.Do(func() {
		r := csv.NewReader(bytes.NewReader(raw))
		if _, err := r.Read(); err != nil { // header
			panic(fmt.Sprintf("airports: bad header: %v", err))
		}
		byIATA = make(map[string]Airport, 4096)
		for {
			row, err := r.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				panic(fmt.Sprintf("airports: bad row: %v", err))
			}
			lat, _ := strconv.ParseFloat(row[4], 64)
			lon, _ := strconv.ParseFloat(row[5], 64)
			a := Airport{
				IATA: row[0], Name: row[1], City: row[2], Country: row[3],
				Lat: lat, Lon: lon, Size: strings.TrimSuffix(row[6], "_airport"),
			}
			all = append(all, a)
			byIATA[a.IATA] = a
		}
	})
}

// Get returns the airport for an IATA code.
func Get(code string) (Airport, bool) {
	load()
	a, ok := byIATA[strings.ToUpper(strings.TrimSpace(code))]
	return a, ok
}

func sizeRank(s string) int {
	switch s {
	case "large":
		return 0
	case "medium":
		return 1
	}
	return 2
}

// Search finds airports by code, city or name. Larger airports rank first.
func Search(query string, limit int) []Airport {
	load()
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil
	}
	type hit struct {
		a     Airport
		score int
	}
	var hits []hit
	for _, a := range all {
		score := -1
		city := strings.ToLower(a.City)
		name := strings.ToLower(a.Name)
		switch {
		case strings.ToLower(a.IATA) == q:
			score = 0
		case city == q:
			score = 1
		case strings.HasPrefix(city, q):
			score = 2
		case len(q) >= 4 && strings.Contains(name, q):
			score = 3
		case len(q) >= 4 && strings.Contains(city, q):
			score = 4
		}
		if score >= 0 {
			hits = append(hits, hit{a, score*3 + sizeRank(a.Size)})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score < hits[j].score })
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]Airport, len(hits))
	for i, h := range hits {
		out[i] = h.a
	}
	return out
}

// Resolve turns a user term into airport codes. It accepts an IATA
// airport code, an IATA metro code, or a city name. A city with several
// large or medium airports resolves to all of them.
func Resolve(term string) ([]string, error) {
	load()
	t := strings.TrimSpace(term)
	if t == "" {
		return nil, fmt.Errorf("empty airport term")
	}
	upper := strings.ToUpper(t)
	if len(upper) == 3 {
		if members, ok := Metros[upper]; ok {
			return append([]string(nil), members...), nil
		}
		if _, ok := byIATA[upper]; ok {
			return []string{upper}, nil
		}
	}
	lower := strings.ToLower(t)
	if metro, ok := metroByCity[lower]; ok {
		return append([]string(nil), Metros[metro]...), nil
	}
	var codes []string
	for _, a := range all {
		if strings.EqualFold(a.City, t) && a.Size != "small" {
			codes = append(codes, a.IATA)
		}
	}
	if len(codes) == 0 {
		for _, a := range all {
			if strings.EqualFold(a.City, t) {
				codes = append(codes, a.IATA)
			}
		}
	}
	if len(codes) > 0 {
		sort.Slice(codes, func(i, j int) bool {
			return sizeRank(byIATA[codes[i]].Size) < sizeRank(byIATA[codes[j]].Size)
		})
		return codes, nil
	}
	hits := Search(t, 3)
	if len(hits) == 1 || (len(hits) > 1 && strings.Contains(strings.ToLower(hits[0].Name), lower)) {
		return []string{hits[0].IATA}, nil
	}
	if len(hits) > 1 {
		var opts []string
		for _, h := range hits {
			opts = append(opts, fmt.Sprintf("%s (%s, %s)", h.IATA, h.Name, h.City))
		}
		return nil, fmt.Errorf("%q is ambiguous: %s", term, strings.Join(opts, "; "))
	}
	return nil, fmt.Errorf("no airport matches %q", term)
}

// Label describes a code for humans, e.g. "BRS (Bristol)".
func Label(code string) string {
	if a, ok := Get(code); ok && a.City != "" {
		city, _, _ := strings.Cut(a.City, ",")
		return fmt.Sprintf("%s (%s)", a.IATA, strings.TrimSpace(city))
	}
	return code
}
