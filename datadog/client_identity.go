package datadog

import "encoding/json"

// User is the person an application key acts as.
type User struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Handle string `json:"handle"`
	Email  string `json:"email"`
}

// CurrentUser returns the owner of the application key
// (GET /api/v2/current_user) — the cheapest call that proves both keys work.
func (c *Client) CurrentUser() (*User, error) {
	data, err := c.do("GET", "/api/v2/current_user", nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data struct {
			ID         string `json:"id"`
			Attributes struct {
				Name   string `json:"name"`
				Handle string `json:"handle"`
				Email  string `json:"email"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	a := resp.Data.Attributes
	return &User{ID: resp.Data.ID, Name: a.Name, Handle: a.Handle, Email: a.Email}, nil
}

// OrgName returns the organization's name (GET /api/v1/org).
func (c *Client) OrgName() (string, error) {
	data, err := c.do("GET", "/api/v1/org", nil)
	if err != nil {
		return "", err
	}
	var resp struct {
		Orgs []struct {
			Name string `json:"name"`
		} `json:"orgs"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", err
	}
	if len(resp.Orgs) == 0 {
		return "", nil
	}
	return resp.Orgs[0].Name, nil
}

// Probe GETs a path and reports only whether it worked — for checking what
// the keys can read.
func (c *Client) Probe(path string) error {
	_, err := c.do("GET", path, nil)
	return err
}
