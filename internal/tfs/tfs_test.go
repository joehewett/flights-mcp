package tfs

import "testing"

func intp(i int) *int { return &i }

// Expected values are tfs strings that Google Flights accepted in live
// searches and that returned the matching itineraries.
func TestEncode(t *testing.T) {
	cases := []struct {
		name string
		q    Query
		want string
	}{
		{
			name: "one way single airport",
			q: Query{Kind: OneWay, Adults: 1, Legs: []Leg{{
				Date: "2026-12-27", From: []string{"BRS"}, To: []string{"MAD"}, MaxStops: intp(1),
			}}},
			want: "GhwSCjIwMjYtMTItMjcoAWoFEgNCUlNyBRIDTUFEQgEBSAGYAQI=",
		},
		{
			name: "one way several airports",
			q: Query{Kind: OneWay, Adults: 1, Legs: []Leg{{
				Date: "2026-12-27", From: []string{"BRS", "LHR", "LGW", "STN", "LTN"}, To: []string{"MAD", "SDR"}, MaxStops: intp(1),
			}}},
			want: "Gj8SCjIwMjYtMTItMjcoAWoFEgNCUlNqBRIDTEhSagUSA0xHV2oFEgNTVE5qBRIDTFROcgUSA01BRHIFEgNTRFJCAQFIAZgBAg==",
		},
		{
			name: "two adults",
			q: Query{Kind: OneWay, Adults: 2, Legs: []Leg{{
				Date: "2026-12-27", From: []string{"BRS"}, To: []string{"MAD"}, MaxStops: intp(1),
			}}},
			want: "GhwSCjIwMjYtMTItMjcoAWoFEgNCUlNyBRIDTUFEQgIBAUgBmAEC",
		},
		{
			name: "selected flight",
			q: Query{Kind: OneWay, Adults: 1, Legs: []Leg{{
				Date: "2026-12-27", From: []string{"BRS"}, To: []string{"MAD"}, MaxStops: intp(1),
				Selected: []Selected{{Origin: "BRS", Date: "2026-12-27", Destination: "MAD", Carrier: "FR", Number: "155"}},
			}}},
			want: "Gj0SCjIwMjYtMTItMjcoAWoFEgNCUlNyBRIDTUFEIh8KA0JSUxIKMjAyNi0xMi0yNxoDTUFEKgJGUjIDMTU1QgEBSAGYAQI=",
		},
		{
			name: "any stops",
			q: Query{Kind: OneWay, Adults: 1, Legs: []Leg{{
				Date: "2026-12-27", From: []string{"BRS"}, To: []string{"MAD"},
			}}},
			want: "GhoSCjIwMjYtMTItMjdqBRIDQlJTcgUSA01BREIBAUgBmAEC",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Encode(tc.q)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}

func TestEncodeRejectsBadInput(t *testing.T) {
	bad := []Query{
		{Kind: OneWay},
		{Kind: OneWay, Legs: []Leg{{Date: "27/12/2026", From: []string{"BRS"}, To: []string{"MAD"}}}},
		{Kind: OneWay, Legs: []Leg{{Date: "2026-12-27", From: []string{"bristol"}, To: []string{"MAD"}}}},
		{Kind: OneWay, Legs: []Leg{{Date: "2026-12-27", From: []string{"BRS"}}}},
		{Legs: []Leg{{Date: "2026-12-27", From: []string{"BRS"}, To: []string{"MAD"}}}},
	}
	for i, q := range bad {
		if _, err := Encode(q); err == nil {
			t.Errorf("case %d: expected an error", i)
		}
	}
}
