package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

var (
	mtagsJSON bool
	mtagsKey  string
	mtagsMax  int
)

// metricTags is what a metric can be filtered and grouped by.
type metricTags struct {
	Metric string              `json:"metric"`
	Series int                 `json:"series,omitempty"` // distinct time series
	Tags   map[string][]string `json:"tags"`
}

func fetchMetricTags(metric string) (*metricTags, error) {
	resp, err := client.Raw("GET", "/api/v2/metrics/"+url.PathEscape(metric)+"/all-tags", nil, nil)
	if err != nil {
		return nil, err
	}
	if resp.Status >= 400 {
		return nil, fmt.Errorf("tags of %s: HTTP %d", metric, resp.Status)
	}
	var body struct {
		Data struct {
			Attributes struct {
				Tags []string `json:"tags"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		return nil, err
	}
	mt := &metricTags{Metric: metric, Tags: map[string][]string{}}
	for _, t := range body.Data.Attributes.Tags {
		k, v, _ := strings.Cut(t, ":")
		mt.Tags[k] = append(mt.Tags[k], v)
	}
	for k := range mt.Tags {
		sort.Strings(mt.Tags[k])
	}
	if vr, err := client.Raw("GET", "/api/v2/metrics/"+url.PathEscape(metric)+"/volumes", nil, nil); err == nil && vr.Status < 400 {
		var v struct {
			Data struct {
				Attributes struct {
					DistinctVolume int `json:"distinct_volume"`
				} `json:"attributes"`
			} `json:"data"`
		}
		if json.Unmarshal(vr.Body, &v) == nil {
			mt.Series = v.Data.Attributes.DistinctVolume
		}
	}
	return mt, nil
}

var metricsTagsCmd = &cobra.Command{
	Use:   "tags <metric>",
	Short: "What a metric can be filtered and grouped by: tag keys and values",
	Long: `List a metric's tag keys with their values, and how many distinct series
it has: what goes inside {…} and by {…} in a query, with the values that
actually exist (env:production, not env:prod). Keys with many values make
many series when grouped: pick them with care in dashboards and monitors.

Examples:
  datadog metrics tags trace.http.request.hits
  datadog metrics tags system.cpu.user --key host
  datadog metrics tags aws.elb.request_count --json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		mt, err := fetchMetricTags(args[0])
		if err != nil {
			return err
		}
		if mtagsKey != "" {
			mt.Tags = map[string][]string{mtagsKey: mt.Tags[mtagsKey]}
		}
		if mtagsJSON {
			return printJSON(mt)
		}
		keys := make([]string, 0, len(mt.Tags))
		for k := range mt.Tags {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			return tagRank(keys[i]) < tagRank(keys[j]) || (tagRank(keys[i]) == tagRank(keys[j]) && keys[i] < keys[j])
		})
		fmt.Printf("%s", mt.Metric)
		if mt.Series > 0 {
			fmt.Printf(" · %d series", mt.Series)
		}
		fmt.Println()
		for _, k := range keys {
			vals := mt.Tags[k]
			shown := vals
			if mtagsMax > 0 && len(shown) > mtagsMax {
				shown = shown[:mtagsMax]
			}
			more := ""
			if len(vals) > len(shown) {
				more = fmt.Sprintf(" … +%d", len(vals)-len(shown))
			}
			fmt.Printf("  %s (%d): %s%s\n", k, len(vals), strings.Join(shown, ", "), more)
		}
		return nil
	},
}

// tagRank puts the tags people filter by first.
func tagRank(k string) int {
	switch k {
	case "env":
		return 0
	case "service":
		return 1
	case "version", "resource_name", "http.status_code", "status":
		return 2
	case "host", "kube_deployment", "pod_name", "region", "availability-zone":
		return 3
	}
	return 4
}

var (
	reQueryMetric = regexp.MustCompile(`(?:\w+:)?([a-zA-Z_][\w.]*)\{([^}]*)\}`)
)

// noDataHint explains an empty query when it's a tag that doesn't exist:
// "env:prod isn't a value of env for trace.x (it has: production)". The
// most common reason an agent's query comes back empty.
func noDataHint(query string) string {
	var hints []string
	seen := map[string]bool{}
	for _, m := range reQueryMetric.FindAllStringSubmatch(query, -1) {
		metric, scope := m[1], m[2]
		if seen[metric] {
			continue
		}
		seen[metric] = true
		mt, err := fetchMetricTags(metric)
		if err != nil && strings.Contains(err.Error(), "HTTP 404") {
			mt, err = &metricTags{Metric: metric}, nil
		}
		if err != nil {
			continue
		}
		if len(mt.Tags) == 0 {
			hints = append(hints, fmt.Sprintf("%s has no data recently (or doesn't exist): try 'datadog metrics search %s'", metric, lastSegment(metric)))
			continue
		}
		for _, part := range strings.Split(scope, ",") {
			part = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(part), "!"))
			k, v, ok := strings.Cut(part, ":")
			if !ok || strings.ContainsAny(v, "*$") {
				continue
			}
			vals, known := mt.Tags[k]
			if !known {
				hints = append(hints, fmt.Sprintf("%s has no tag %q (it has: %s)", metric, k, strings.Join(firstKeys(mt.Tags, 8), ", ")))
				continue
			}
			found := false
			for _, x := range vals {
				if x == v {
					found = true
				}
			}
			if !found {
				hints = append(hints, fmt.Sprintf("%s:%s isn't a value of %s for %s (it has: %s)", k, v, k, metric, strings.Join(firstN(vals, 8), ", ")))
			}
		}
	}
	return strings.Join(hints, "\n")
}

func lastSegment(metric string) string {
	parts := strings.Split(metric, ".")
	if len(parts) > 1 {
		return strings.Join(parts[:len(parts)-1], ".")
	}
	return metric
}

func firstN(v []string, n int) []string {
	if len(v) > n {
		return append(append([]string(nil), v[:n]...), fmt.Sprintf("… +%d", len(v)-n))
	}
	return v
}

func firstKeys(m map[string][]string, n int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return tagRank(keys[i]) < tagRank(keys[j]) || (tagRank(keys[i]) == tagRank(keys[j]) && keys[i] < keys[j])
	})
	return firstN(keys, n)
}

func init() {
	metricsTagsCmd.Flags().BoolVar(&mtagsJSON, "json", false, "Output as JSON")
	metricsTagsCmd.Flags().StringVar(&mtagsKey, "key", "", "Only this tag key")
	metricsTagsCmd.Flags().IntVar(&mtagsMax, "max", 12, "Values to show per key (0 = all)")
	metricsCmd.AddCommand(metricsTagsCmd)
}
