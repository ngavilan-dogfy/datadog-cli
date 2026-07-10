package datadog

import (
	"encoding/json"
	"fmt"
)

// --- GCP Integration (v2) ---

type GCPAccountsResponse struct {
	Data []GCPAccount `json:"data"`
}

type GCPAccount struct {
	ID         string              `json:"id"`
	Type       string              `json:"type"`
	Attributes GCPAccountAttributes `json:"attributes"`
	Meta       *GCPAccountMeta     `json:"meta,omitempty"`
}

type GCPAccountAttributes struct {
	AccountTags                    []string                  `json:"account_tags"`
	Automute                       bool                      `json:"automute"`
	ClientEmail                    string                    `json:"client_email"`
	CloudRunRevisionFilters        []string                  `json:"cloud_run_revision_filters"`
	HostFilters                    []string                  `json:"host_filters"`
	IsCspmEnabled                  bool                      `json:"is_cspm_enabled"`
	IsResourceChangeCollEnabled    bool                      `json:"is_resource_change_collection_enabled"`
	IsSecurityCommandCenterEnabled bool                      `json:"is_security_command_center_enabled"`
	MonitoredResourceConfigs       []MonitoredResourceConfig `json:"monitored_resource_configs"`
	ResourceCollectionEnabled      bool                      `json:"resource_collection_enabled"`
}

type MonitoredResourceConfig struct {
	Type    string   `json:"type"`
	Filters []string `json:"filters"`
}

type GCPAccountMeta struct {
	AccessibleProjects []string `json:"accessible_projects"`
}

// --- GCP API methods ---

func (c *Client) ListGCPAccounts() ([]GCPAccount, error) {
	data, err := c.do("GET", "/api/v2/integration/gcp/accounts", nil)
	if err != nil {
		return nil, err
	}

	var result GCPAccountsResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

type GCPAccountPatchRequest struct {
	Data GCPAccountPatchData `json:"data"`
}

type GCPAccountPatchData struct {
	ID         string                      `json:"id"`
	Type       string                      `json:"type"`
	Attributes GCPAccountPatchAttributes   `json:"attributes"`
}

type GCPAccountPatchAttributes struct {
	HostFilters              *[]string                  `json:"host_filters,omitempty"`
	MonitoredResourceConfigs *[]MonitoredResourceConfig `json:"monitored_resource_configs,omitempty"`
	Automute                 *bool                      `json:"automute,omitempty"`
	CloudRunRevisionFilters  *[]string                  `json:"cloud_run_revision_filters,omitempty"`
}

func (c *Client) UpdateGCPAccount(id string, attrs GCPAccountPatchAttributes) (*GCPAccount, error) {
	req := GCPAccountPatchRequest{
		Data: GCPAccountPatchData{
			ID:         id,
			Type:       "gcp_service_account",
			Attributes: attrs,
		},
	}

	data, err := c.do("PATCH", fmt.Sprintf("/api/v2/integration/gcp/accounts/%s", id), req)
	if err != nil {
		return nil, err
	}

	var result struct {
		Data GCPAccount `json:"data"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return &result.Data, nil
}
