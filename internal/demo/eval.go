package demo

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
)

// A small evaluator for dashboard formulas ("100 * errors / hits") and v1
// metric expressions ("sum:a{x}.as_count() / sum:b{x}.as_count() * 100").
// Operands are sets of series keyed by their group tags; a set without
// groups combines with every group of the other side, like Datadog does.
// Functions (top, abs, timeshift…) evaluate to their first argument.

type set struct {
	order []string // group keys in display order
	byKey map[string]namedSeries
}

func single(tags []string, values []float64) set {
	k := strings.Join(tags, ",")
	return set{order: []string{k}, byKey: map[string]namedSeries{k: {tags: tags, values: values}}}
}

func constant(v float64, n int) set {
	vals := make([]float64, n)
	for i := range vals {
		vals[i] = v
	}
	return single(nil, vals)
}

func (s set) grouped() bool { return !(len(s.order) == 1 && s.order[0] == "") }

func combine(a, b set, op byte) set {
	apply := func(x, y []float64) []float64 {
		out := make([]float64, len(x))
		for i := range x {
			var yv float64
			if i < len(y) {
				yv = y[i]
			} else {
				yv = math.NaN()
			}
			switch op {
			case '+':
				out[i] = x[i] + yv
			case '-':
				out[i] = x[i] - yv
			case '*':
				out[i] = x[i] * yv
			case '/':
				if yv == 0 {
					out[i] = math.NaN()
				} else {
					out[i] = x[i] / yv
				}
			}
		}
		return out
	}
	out := set{byKey: map[string]namedSeries{}}
	switch {
	case a.grouped() && !b.grouped():
		other := b.byKey[b.order[0]].values
		for _, k := range a.order {
			out.order = append(out.order, k)
			out.byKey[k] = namedSeries{tags: a.byKey[k].tags, values: apply(a.byKey[k].values, other)}
		}
	case !a.grouped() && b.grouped():
		other := a.byKey[a.order[0]].values
		for _, k := range b.order {
			out.order = append(out.order, k)
			out.byKey[k] = namedSeries{tags: b.byKey[k].tags, values: apply(other, b.byKey[k].values)}
		}
	default:
		for _, k := range a.order {
			bs, ok := b.byKey[k]
			if !ok {
				continue
			}
			out.order = append(out.order, k)
			out.byKey[k] = namedSeries{tags: a.byKey[k].tags, values: apply(a.byKey[k].values, bs.values)}
		}
	}
	return out
}

// evaluator parses and evaluates one expression. leaf resolves a formula
// name or a metric term.
type evaluator struct {
	src  string
	pos  int
	n    int // points per series
	name func(string) (set, bool)
	term func([]string) set
}

func (e *evaluator) eval() (set, error) {
	s, err := e.expr()
	if err != nil {
		return set{}, err
	}
	e.space()
	if e.pos < len(e.src) {
		return set{}, fmt.Errorf("unexpected %q in %q", e.src[e.pos:], e.src)
	}
	return s, nil
}

func (e *evaluator) space() {
	for e.pos < len(e.src) && e.src[e.pos] == ' ' {
		e.pos++
	}
}

func (e *evaluator) expr() (set, error) {
	left, err := e.product()
	if err != nil {
		return set{}, err
	}
	for {
		e.space()
		if e.pos >= len(e.src) || (e.src[e.pos] != '+' && e.src[e.pos] != '-') {
			return left, nil
		}
		op := e.src[e.pos]
		e.pos++
		right, err := e.product()
		if err != nil {
			return set{}, err
		}
		left = combine(left, right, op)
	}
}

func (e *evaluator) product() (set, error) {
	left, err := e.factor()
	if err != nil {
		return set{}, err
	}
	for {
		e.space()
		if e.pos >= len(e.src) || (e.src[e.pos] != '*' && e.src[e.pos] != '/') {
			return left, nil
		}
		op := e.src[e.pos]
		e.pos++
		right, err := e.factor()
		if err != nil {
			return set{}, err
		}
		left = combine(left, right, op)
	}
}

func (e *evaluator) factor() (set, error) {
	e.space()
	if e.pos >= len(e.src) {
		return set{}, fmt.Errorf("incomplete expression %q", e.src)
	}
	c := e.src[e.pos]
	switch {
	case c == '-':
		e.pos++
		f, err := e.factor()
		if err != nil {
			return set{}, err
		}
		return combine(constant(-1, e.n), f, '*'), nil
	case c == '(':
		e.pos++
		s, err := e.expr()
		if err != nil {
			return set{}, err
		}
		e.space()
		if e.pos < len(e.src) && e.src[e.pos] == ')' {
			e.pos++
		}
		return s, nil
	case c >= '0' && c <= '9' || c == '.':
		start := e.pos
		for e.pos < len(e.src) && (e.src[e.pos] >= '0' && e.src[e.pos] <= '9' || e.src[e.pos] == '.') {
			e.pos++
		}
		v, err := strconv.ParseFloat(e.src[start:e.pos], 64)
		if err != nil {
			return set{}, err
		}
		return constant(v, e.n), nil
	}
	// A metric term ("sum:x{…} by {…}.as_count()") starts here?
	if m := reMetricTerm.FindStringSubmatchIndex(e.src[e.pos:]); m != nil && m[0] == 0 {
		groups := make([]string, 6)
		for i := range groups {
			if m[2*i] >= 0 {
				groups[i] = e.src[e.pos+m[2*i] : e.pos+m[2*i+1]]
			}
		}
		e.pos += m[1]
		return e.term(groups), nil
	}
	// A name, maybe a function call.
	start := e.pos
	for e.pos < len(e.src) && (unicode.IsLetter(rune(e.src[e.pos])) || unicode.IsDigit(rune(e.src[e.pos])) || e.src[e.pos] == '_') {
		e.pos++
	}
	ident := e.src[start:e.pos]
	if ident == "" {
		return set{}, fmt.Errorf("unexpected %q in %q", e.src[e.pos:], e.src)
	}
	e.space()
	if e.pos < len(e.src) && e.src[e.pos] == '(' {
		e.pos++
		first, err := e.expr()
		if err != nil {
			return set{}, err
		}
		// Skip the other arguments (numbers, 'quoted' options).
		depth := 1
		for e.pos < len(e.src) && depth > 0 {
			switch e.src[e.pos] {
			case '(':
				depth++
			case ')':
				depth--
			}
			e.pos++
		}
		return first, nil
	}
	if s, ok := e.name(ident); ok {
		return s, nil
	}
	return set{}, fmt.Errorf("unknown query %q", ident)
}
