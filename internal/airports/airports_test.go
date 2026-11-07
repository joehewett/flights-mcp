package airports

import (
	"reflect"
	"testing"
)

func TestResolve(t *testing.T) {
	cases := map[string][]string{
		"BRS":       {"BRS"},
		"brs":       {"BRS"},
		"bristol":   {"BRS"},
		"Bristol":   {"BRS"},
		"LON":       {"LHR", "LGW", "STN", "LTN", "LCY", "SEN"},
		"london":    {"LHR", "LGW", "STN", "LTN", "LCY", "SEN"},
		"SDR":       {"SDR"},
		"santander": {"SDR"},
		"madrid":    {"MAD"},
	}
	for in, want := range cases {
		got, err := Resolve(in)
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%q: got %v want %v", in, got, want)
		}
	}
	if _, err := Resolve("xyzzy-nowhere"); err == nil {
		t.Error("expected an error for an unknown place")
	}
}

func TestGet(t *testing.T) {
	a, ok := Get("mad")
	if !ok || a.City != "Madrid" || a.Country != "ES" {
		t.Fatalf("got %+v", a)
	}
}

func TestSearchRanksLargeFirst(t *testing.T) {
	hits := Search("london", 3)
	if len(hits) == 0 || hits[0].Size != "large" || hits[0].Country != "GB" {
		t.Fatalf("got %+v", hits)
	}
}
