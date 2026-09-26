package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/ngavilan-dogfy/datadog-cli/datadog"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
)

var (
	apiMethod   string
	apiFields   []string
	apiRaw      []string
	apiQuery    []string
	apiInput    string
	apiYes      bool
	apiInclude  bool
	apiSilent   bool
	apiCompactJ bool
)

var apiCmd = &cobra.Command{
	Use:   "api <path>",
	Short: "Call any Datadog API endpoint with your keys and site",
	Long: `Send a request to any Datadog API endpoint, authenticated with the active
profile's keys and site: everything the API offers, not only what the other
commands cover. Like 'gh api'.

Reads run right away: GET, and the searches and queries Datadog sends as
POST (paths ending in /search or /aggregate, and /api/v2/query/…). Anything
else can change your Datadog: it asks first, or needs --yes when there's no
terminal. Read-only profiles refuse it.

Body:
  -f key=value     a string field
  -F key=value     a typed field: numbers, true/false, null and JSON ({…} / […])
  --input file     the whole body from a file (- reads stdin)
Nested fields use dots: -F data.attributes.filter.query='"service:api"'.
With GET, -f and -F become query parameters instead.

Output:
  The JSON body, pretty-printed in a terminal (--compact for one line).
  HTTP errors print the body and exit 1. --include adds the status line and
  headers.

Examples:
  datadog api /api/v1/validate
  datadog api /api/v2/current_user
  datadog api /api/v1/monitor -q group_states=all -q monitor_tags=service:api
  datadog api /api/v2/metrics/trace.http.request.hits/all-tags
  datadog api /api/v2/logs/analytics/aggregate --input query.json
  datadog api /api/v1/service_dependencies -q env=prod
  datadog api -X POST /api/v1/monitor/123/mute --yes`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := args[0]
		if strings.HasPrefix(path, "http") {
			u, err := url.Parse(path)
			if err != nil {
				return err
			}
			path = u.RequestURI()
		}
		query := url.Values{}
		for _, q := range apiQuery {
			k, v, ok := strings.Cut(q, "=")
			if !ok {
				return fmt.Errorf("-q %q: use key=value", q)
			}
			query.Add(k, v)
		}
		body := map[string]any{}
		addField := func(kv string, typed bool) error {
			k, v, ok := strings.Cut(kv, "=")
			if !ok || k == "" {
				return fmt.Errorf("%q: use key=value", kv)
			}
			var val any = v
			if typed {
				val = typedValue(v)
			}
			setPath(body, strings.Split(k, "."), val)
			return nil
		}
		for _, f := range apiFields {
			if err := addField(f, false); err != nil {
				return err
			}
		}
		for _, f := range apiRaw {
			if err := addField(f, true); err != nil {
				return err
			}
		}

		method := strings.ToUpper(apiMethod)
		var payload []byte
		switch {
		case apiInput != "":
			var err error
			if apiInput == "-" {
				payload, err = io.ReadAll(os.Stdin)
			} else {
				payload, err = os.ReadFile(apiInput)
			}
			if err != nil {
				return fmt.Errorf("--input: %w", err)
			}
			if method == "" {
				method = "POST"
			}
		case len(body) > 0 && (method == "" || method == "GET"):
			if method == "" && len(apiFields)+len(apiRaw) > 0 && looksLikeQuery(path) {
				method = "GET"
			}
			if method == "GET" {
				for _, k := range sortedKeys(body) {
					query.Set(k, fmt.Sprint(body[k]))
				}
			} else {
				method = "POST"
				payload, _ = json.Marshal(body)
			}
		case len(body) > 0:
			payload, _ = json.Marshal(body)
		}
		if method == "" {
			method = "GET"
		}

		if !datadog.ReadOnlyRequest(method, path) {
			if cfg != nil && cfg.ReadOnly {
				return fmt.Errorf("profile %q is read-only: %s %s would change your Datadog", cfg.Name, method, path)
			}
			if !apiYes {
				if !interactive() {
					return fmt.Errorf("%s %s can change your Datadog: add --yes to send it", method, path)
				}
				ok := false
				if err := ask(huh.NewConfirm().Title(method + " " + path).
					Description("This can change your Datadog.").Affirmative("Send it").Negative("Cancel").Value(&ok)); err != nil || !ok {
					return quietError{fmt.Errorf("cancelled")}
				}
			}
		}

		resp, err := client.Raw(method, path, query, payload)
		if err != nil {
			return err
		}
		if apiInclude {
			fmt.Printf("HTTP %d\n", resp.Status)
			for _, k := range sortedHeaderKeys(resp.Header) {
				fmt.Printf("%s: %s\n", k, strings.Join(resp.Header[k], ", "))
			}
			fmt.Println()
		}
		if !apiSilent && len(resp.Body) > 0 {
			out := resp.Body
			var buf bytes.Buffer
			switch {
			case apiCompactJ || !isTTY():
				if json.Compact(&buf, resp.Body) == nil {
					out = buf.Bytes()
				}
			default:
				if json.Indent(&buf, resp.Body, "", "  ") == nil {
					out = buf.Bytes()
				}
			}
			os.Stdout.Write(out)
			if !bytes.HasSuffix(out, []byte("\n")) {
				fmt.Println()
			}
		}
		if resp.Status >= 400 {
			return quietError{fmt.Errorf("HTTP %d", resp.Status)}
		}
		return nil
	},
}

// looksLikeQuery: without -X, fields on a v1 path read like query params.
func looksLikeQuery(path string) bool { return strings.HasPrefix(path, "/api/v1/") }

func typedValue(v string) any {
	switch v {
	case "true":
		return true
	case "false":
		return false
	case "null":
		return nil
	}
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(v, 64); err == nil {
		return f
	}
	var j any
	if (strings.HasPrefix(v, "{") || strings.HasPrefix(v, "[") || strings.HasPrefix(v, `"`)) && json.Unmarshal([]byte(v), &j) == nil {
		return j
	}
	return v
}

func setPath(m map[string]any, path []string, v any) {
	for i, p := range path {
		if i == len(path)-1 {
			m[p] = v
			return
		}
		next, ok := m[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[p] = next
		}
		m = next
	}
}

func sortedKeys(m map[string]any) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedHeaderKeys(h map[string][]string) []string {
	var out []string
	for k := range h {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func init() {
	apiCmd.Flags().StringVarP(&apiMethod, "method", "X", "", "HTTP method (default GET, or POST with a body)")
	apiCmd.Flags().StringArrayVarP(&apiFields, "field", "f", nil, "String body field key=value (query param with GET)")
	apiCmd.Flags().StringArrayVarP(&apiRaw, "raw-field", "F", nil, "Typed body field key=value: numbers, booleans, null, JSON")
	apiCmd.Flags().StringArrayVarP(&apiQuery, "query", "q", nil, "Query parameter key=value (repeatable)")
	apiCmd.Flags().StringVar(&apiInput, "input", "", "Request body from a file (- for stdin)")
	apiCmd.Flags().BoolVarP(&apiYes, "yes", "y", false, "Send requests that change data without asking")
	apiCmd.Flags().BoolVarP(&apiInclude, "include", "i", false, "Print the status line and headers")
	apiCmd.Flags().BoolVar(&apiSilent, "silent", false, "Don't print the body")
	apiCmd.Flags().BoolVar(&apiCompactJ, "compact", false, "One-line JSON even in a terminal")
	rootCmd.AddCommand(apiCmd)
}
