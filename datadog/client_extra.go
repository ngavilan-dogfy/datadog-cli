package datadog

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// --- Synthetics ---

func (c *Client) ListSynthetics() ([]SyntheticsTest, error) {
	data, err := c.do("GET", "/api/v1/synthetics/tests", nil)
	if err != nil {
		return nil, err
	}

	var result SyntheticsListResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return result.Tests, nil
}

func (c *Client) GetSynthetic(publicID string) (*SyntheticsTest, error) {
	data, err := c.do("GET", "/api/v1/synthetics/tests/"+publicID, nil)
	if err != nil {
		return nil, err
	}

	var test SyntheticsTest
	return &test, json.Unmarshal(data, &test)
}

func (c *Client) TriggerSynthetics(publicIDs []string) (*SyntheticsTriggerResponse, error) {
	tests := make([]SyntheticsTriggerTest, len(publicIDs))
	for i, id := range publicIDs {
		tests[i] = SyntheticsTriggerTest{PublicID: id}
	}

	req := SyntheticsTriggerRequest{Tests: tests}
	data, err := c.do("POST", "/api/v1/synthetics/tests/trigger", req)
	if err != nil {
		return nil, err
	}

	var result SyntheticsTriggerResponse
	return &result, json.Unmarshal(data, &result)
}

// --- Host Tags ---

func (c *Client) GetHostTags(hostname string) ([]string, error) {
	data, err := c.do("GET", "/api/v1/tags/hosts/"+hostname, nil)
	if err != nil {
		return nil, err
	}

	var result HostTagsResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return result.Tags, nil
}

func (c *Client) AddHostTags(hostname string, tags []string) error {
	body := map[string]interface{}{
		"tags": tags,
	}
	_, err := c.do("POST", "/api/v1/tags/hosts/"+hostname, body)
	return err
}

func (c *Client) UpdateHostTags(hostname string, tags []string) error {
	body := map[string]interface{}{
		"tags": tags,
	}
	_, err := c.do("PUT", "/api/v1/tags/hosts/"+hostname, body)
	return err
}

func (c *Client) RemoveHostTags(hostname string) error {
	_, err := c.do("DELETE", "/api/v1/tags/hosts/"+hostname, nil)
	return err
}

// --- Service Catalog ---

func (c *Client) ListServices() ([]ServiceCatalogEntry, error) {
	data, err := c.do("GET", "/api/v2/services/definitions?schema-version=v2.1", nil)
	if err != nil {
		return nil, err
	}

	var result ServiceCatalogResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

func (c *Client) GetService(serviceName string) (*ServiceCatalogEntry, error) {
	data, err := c.do("GET", "/api/v2/services/definitions/"+serviceName+"?schema-version=v2.1", nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		Data ServiceCatalogEntry `json:"data"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return &result.Data, nil
}

// --- Notebooks ---

func (c *Client) ListNotebooks(query string) ([]NotebookEntry, error) {
	params := url.Values{}
	if query != "" {
		params.Set("query", query)
	}
	params.Set("count", "100")

	data, err := c.do("GET", "/api/v1/notebooks?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}

	var result NotebooksResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// --- Security Signals ---

func (c *Client) SearchSecuritySignals(query string, from, to string, limit int) (*SecuritySignalsResponse, error) {
	if limit == 0 {
		limit = 25
	}

	req := SecuritySignalsSearchRequest{
		Filter: SecuritySignalsFilter{
			Query: query,
			From:  from,
			To:    to,
		},
		Sort: "-timestamp",
		Page: LogsPage{Limit: limit},
	}

	data, err := c.do("POST", "/api/v2/security_monitoring/signals/search", req)
	if err != nil {
		return nil, err
	}

	var result SecuritySignalsResponse
	return &result, json.Unmarshal(data, &result)
}

// --- Usage ---

func (c *Client) GetUsageSummary(startDate, endDate string) (*UsageSummaryResponse, error) {
	params := url.Values{}
	params.Set("start_month", startDate)
	if endDate != "" {
		params.Set("end_month", endDate)
	}

	data, err := c.do("GET", "/api/v1/usage/summary?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}

	var result UsageSummaryResponse
	return &result, json.Unmarshal(data, &result)
}

// --- CI Pipelines ---

func (c *Client) SearchPipelines(query string, from, to string, limit int) ([]CIPipelineEvent, error) {
	if limit == 0 {
		limit = 25
	}
	if query == "" {
		// Empty query makes the API compose an invalid "... AND ()" filter.
		query = "*"
	}

	body := map[string]interface{}{
		"filter": map[string]interface{}{
			"query": query,
			"from":  from,
			"to":    to,
		},
		"page": map[string]interface{}{
			"limit": limit,
		},
		"sort": "-timestamp",
	}

	data, err := c.do("POST", "/api/v2/ci/pipelines/events/search", body)
	if err != nil {
		// CI Visibility might not be enabled
		if strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "404") ||
			strings.Contains(err.Error(), "No valid indexes") {
			return nil, fmt.Errorf("CI Visibility not enabled or insufficient permissions")
		}
		return nil, err
	}

	var result CIPipelinesResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}
