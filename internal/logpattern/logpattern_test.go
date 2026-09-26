package logpattern

import "testing"

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"Contact synced for userId: K7QX2M9ZPA.":                                  "Contact synced for userId: <id>.",
		"GET /api/users/ana@example.com → 400 in 250ms":                           "GET /api/users/<email> → <num> in <num>ms",
		"order 0f8fad5b-d9cb-469f-a165-70867728950e failed after 3 retries":       "order <uuid> failed after <num> retries",
		"connect ECONNREFUSED 10.0.3.7:27017":                                     "connect ECONNREFUSED <ip>",
		"job started at 2026-05-19T04:00:12.123Z (took 1.5s)":                     "job started at <time> (took <num>s)",
		"customer 64b7f0c2a1e4d3b2c1a09f8e not found":                             "customer <hex> not found",
		"calling https://api.example.com/v1/users/42?token=abc":                   "calling https://api.example.com/<path>",
		"Unknown plan \"premium-xl\" for customer":                                "Unknown plan \"<str>\" for customer",
		"TypeError: Cannot read properties of undefined\n    at handler (x.js:1)": "TypeError: Cannot read properties of undefined",
		"HTTP/2 upgrade on utf8 v1 endpoint":                                      "HTTP/<num> upgrade on utf8 v1 endpoint",
	} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q)\n got %q\nwant %q", in, got, want)
		}
	}
}

func TestClustererMergesWhatRulesMiss(t *testing.T) {
	c := NewClusterer()
	c.Add("api|error", "Payment declined for customer Alice Smith: card expired", 1)
	c.Add("api|error", "Payment declined for customer Bob Jones: card expired", 3)
	c.Add("api|error", "Payment declined for customer Carol King: card expired", 2)
	c.Add("api|error", "Shipment 12 delayed", 4)
	c.Add("api|error", "Shipment 13 delayed", 5)
	c.Add("web|error", "Shipment 14 delayed", 6) // other service: its own group
	g := c.Groups()
	if len(g) != 3 {
		t.Fatalf("want 3 groups, got %d: %+v", len(g), g)
	}
	if g[0].Template != "Payment declined for customer <*> <*>: card expired" || g[0].Count != 3 {
		t.Errorf("names should fold into <*>: %q ×%d", g[0].Template, g[0].Count)
	}
	if g[0].First != 1 || g[0].Last != 3 {
		t.Errorf("first/last = %d/%d, want 1/3", g[0].First, g[0].Last)
	}
	if c.Match("api|error", "Payment declined for customer Dan Brown: card expired") != g[0] {
		t.Errorf("Match should find the folded group")
	}
	if c.Match("api|error", "Totally different thing happened here today") != nil {
		t.Errorf("Match should not invent a group")
	}
}

func TestWildcardKeepsTheSharedParts(t *testing.T) {
	for _, c := range [][3]string{
		{"orderId=TGVX,", "orderId=ABCD,", "orderId=<*>,"},
		{"orderId=<*>,", "orderId=ZZZ,", "orderId=<*>,"},
		{"(alice)", "(bob)", "(<*>)"},
		{"alice", "bob", "<*>"},
		{"user:alice", "user:alfred", "user:<*>"},
	} {
		if got := wildcard(c[0], c[1]); got != c[2] {
			t.Errorf("wildcard(%q, %q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
}

func TestDifferentActionsDontFold(t *testing.T) {
	c := NewClusterer()
	c.Add("k", "Could not find contact for email: a@b.co", 1)
	c.Add("k", "Could not update contact for userId: K7QX2M9ZPA", 2)
	if n := len(c.Groups()); n != 2 {
		t.Errorf("find vs update are different events, got %d groups", n)
	}
}

func TestShortMessagesDontFold(t *testing.T) {
	c := NewClusterer()
	c.Add("k", "user created", 1)
	c.Add("k", "user deleted", 2)
	if n := len(c.Groups()); n != 2 {
		t.Errorf("two-word messages differing in a word are different events, got %d groups", n)
	}
}
