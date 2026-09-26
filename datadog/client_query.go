package datadog

import (
	"encoding/json"
	"fmt"
)

// The v2 formula & functions query API — what dashboards use. Queries and
// formulas are passed through as the widget definitions hold them, so any
// data source a widget uses (metrics, logs, spans, events, apm_metrics…)
// works without mirroring every shape in Go.

// QueryUnit is how Datadog describes a value's unit.
type QueryUnit struct {
	Family      string  `json:"family"`
	Name        string  `json:"name"`
	ShortName   string  `json:"short_name"`
	ScaleFactor float64 `json:"scale_factor"`
}

// FormulaRequest asks for one set of queries combined by formulas.
type FormulaRequest struct {
	From, To int64 // epoch milliseconds
	Interval int64 // ms between points (timeseries only; 0 = let Datadog pick)
	Queries  []map[string]any
	Formulas []map[string]any
}

func (r FormulaRequest) body(kind string) map[string]any {
	attrs := map[string]any{"from": r.From, "to": r.To, "queries": r.Queries}
	if len(r.Formulas) > 0 {
		attrs["formulas"] = r.Formulas
	}
	if r.Interval > 0 && kind == "timeseries_request" {
		attrs["interval"] = r.Interval
	}
	return map[string]any{"data": map[string]any{"type": kind, "attributes": attrs}}
}

// TimeseriesResult holds one series per formula (and per group).
type TimeseriesResult struct {
	Times  []int64            `json:"times"`
	Series []TimeseriesSeries `json:"series"`
}

// TimeseriesSeries is one line: its group tags, the formula it came from
// and its values (nil where there's no data), aligned with Times.
type TimeseriesSeries struct {
	GroupTags  []string   `json:"group_tags"`
	QueryIndex int        `json:"query_index"`
	Unit       *QueryUnit `json:"unit,omitempty"`
	Values     []*float64 `json:"values"`
}

// QueryTimeseries runs POST /api/v2/query/timeseries.
func (c *Client) QueryTimeseries(r FormulaRequest) (*TimeseriesResult, error) {
	data, err := c.do("POST", "/api/v2/query/timeseries", r.body("timeseries_request"))
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data struct {
			Attributes struct {
				Series []struct {
					GroupTags  []string          `json:"group_tags"`
					QueryIndex int               `json:"query_index"`
					Unit       []json.RawMessage `json:"unit"`
				} `json:"series"`
				Times  []int64      `json:"times"`
				Values [][]*float64 `json:"values"`
			} `json:"attributes"`
		} `json:"data"`
		Errors []any `json:"errors"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	a := resp.Data.Attributes
	out := &TimeseriesResult{Times: a.Times}
	for i, s := range a.Series {
		ts := TimeseriesSeries{GroupTags: s.GroupTags, QueryIndex: s.QueryIndex, Unit: firstUnit(s.Unit)}
		if i < len(a.Values) {
			ts.Values = a.Values[i]
		}
		out.Series = append(out.Series, ts)
	}
	return out, nil
}

// ScalarResult is a table: group columns (one per group-by) and number
// columns (one per formula), aligned by row.
type ScalarResult struct {
	Columns []ScalarColumn
}

// ScalarColumn is either a group column (Groups set) or a number column
// (Values set).
type ScalarColumn struct {
	Name   string
	Type   string // "group" or "number"
	Groups [][]string
	Values []*float64
	Unit   *QueryUnit
}

// Rows is how many rows the table has.
func (r *ScalarResult) Rows() int {
	n := 0
	for _, c := range r.Columns {
		n = max(n, len(c.Groups), len(c.Values))
	}
	return n
}

// QueryScalar runs POST /api/v2/query/scalar.
func (c *Client) QueryScalar(r FormulaRequest) (*ScalarResult, error) {
	data, err := c.do("POST", "/api/v2/query/scalar", r.body("scalar_request"))
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data struct {
			Attributes struct {
				Columns []struct {
					Name   string          `json:"name"`
					Type   string          `json:"type"`
					Values json.RawMessage `json:"values"`
					Meta   struct {
						Unit []json.RawMessage `json:"unit"`
					} `json:"meta"`
				} `json:"columns"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	out := &ScalarResult{}
	for _, col := range resp.Data.Attributes.Columns {
		sc := ScalarColumn{Name: col.Name, Type: col.Type, Unit: firstUnit(col.Meta.Unit)}
		switch col.Type {
		case "group":
			if err := json.Unmarshal(col.Values, &sc.Groups); err != nil {
				return nil, fmt.Errorf("unexpected group column %q: %w", col.Name, err)
			}
		default:
			if err := json.Unmarshal(col.Values, &sc.Values); err != nil {
				return nil, fmt.Errorf("unexpected number column %q: %w", col.Name, err)
			}
		}
		out.Columns = append(out.Columns, sc)
	}
	return out, nil
}

// firstUnit reads the first element of Datadog's [unit, per_unit] pair.
func firstUnit(raw []json.RawMessage) *QueryUnit {
	if len(raw) == 0 || string(raw[0]) == "null" {
		return nil
	}
	var u QueryUnit
	if json.Unmarshal(raw[0], &u) != nil || u.Name == "" {
		return nil
	}
	return &u
}
