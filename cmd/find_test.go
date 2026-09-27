package cmd

import (
	"reflect"
	"testing"
)

func TestWords(t *testing.T) {
	for in, want := range map[string][]string{
		"CheckoutService":            {"checkout", "service"},
		"GET /v1/me/orders/:id":      {"get", "v1", "me", "orders", "id"},
		"acme-website":               {"acme", "website"},
		"trace.fastify.request.hits": {"trace", "fastify", "request", "hits"},
	} {
		if got := words(in); !reflect.DeepEqual(got, want) {
			t.Errorf("words(%q) = %q, want %q", in, got, want)
		}
	}
	if got := queryWords("the web service de pagos"); !reflect.DeepEqual(got, []string{"web", "pagos"}) {
		t.Errorf("queryWords = %q", got)
	}
}

func TestMatchScore(t *testing.T) {
	q := func(s string) []string { return queryWords(s) }
	good := []struct{ query, name string }{
		{"checkout", "checkout"},
		{"checkout", "checkout-api"},
		{"web store", "web-store"},
		{"webstore", "web-store"},
		{"order", "GET /v1/me/orders"},       // plural
		{"orders", "GET /v1/order/:id"},      // singular
		{"website", "acme-website"},          // part of the name
		{"paymnets", "payments-api"},         // typo
		{"store web", "web-store"},           // order doesn't matter
		{"mongo", "mongodb.query"},           // prefix
		{"api orders", "api GET /v1/orders"}, // both words
	}
	for _, c := range good {
		if s := matchScore(q(c.query), c.name); s < findThreshold {
			t.Errorf("%q should match %q (score %.2f)", c.query, c.name, s)
		}
	}
	bad := []struct{ query, name string }{
		{"checkout", "check-data-consistency"},
		{"api", "payments"},
		{"orders", "customers"},
		{"web store", "web-admin"}, // only one of the words
	}
	for _, c := range bad {
		if s := matchScore(q(c.query), c.name); s >= findThreshold {
			t.Errorf("%q shouldn't match %q (score %.2f)", c.query, c.name, s)
		}
	}
	if exact, prefix := matchScore(q("api"), "api"), matchScore(q("api"), "apigateway"); exact <= prefix {
		t.Errorf("an exact name must beat a prefix: %.2f vs %.2f", exact, prefix)
	}
}

func TestEndpointScoreNeedsTheEndpointsOwnWords(t *testing.T) {
	if s := endpointScore(queryWords("api"), "api", "GET /v1/status"); s >= findThreshold {
		t.Errorf("every endpoint of service api matched \"api\" (%.2f)", s)
	}
	if s := endpointScore(queryWords("api orders"), "api", "GET /v1/orders"); s < findThreshold {
		t.Errorf("\"api orders\" should find api's orders endpoint (%.2f)", s)
	}
}

func TestBestMatchPrefersAScope(t *testing.T) {
	ms := []findMatch{
		{Kind: "monitor", Name: "checkout errors", Score: 1},
		{Kind: "service", Name: "checkout-api", Score: 0.8, weight: 1000},
		{Kind: "endpoint", Name: "POST /checkout", Service: "web", Score: 0.8, weight: 10},
	}
	if b := bestMatch(ms); b == nil || b.Name != "checkout-api" {
		t.Errorf("best = %+v, want the service (a scope to investigate, busier on a tie)", b)
	}
	if b := bestMatch(ms[:1]); b == nil || b.Kind != "monitor" {
		t.Errorf("without a service or endpoint, the best of anything: %+v", b)
	}
	if bestMatch(nil) != nil {
		t.Error("nothing to pick from")
	}
}

func TestEditDistance(t *testing.T) {
	for _, c := range []struct {
		a, b string
		d    int
	}{{"payments", "paymnets", 2}, {"orders", "order", 1}, {"", "abc", 3}, {"same", "same", 0}} {
		if got := editDistance(c.a, c.b); got != c.d {
			t.Errorf("editDistance(%q, %q) = %d, want %d", c.a, c.b, got, c.d)
		}
	}
}
