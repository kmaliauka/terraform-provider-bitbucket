package bitbucket

import (
	"bytes"
	"encoding/json"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

// Request bodies carry writable fields only: the API rejects read-only keys such as
// created_on or type with "extra keys not allowed".

type requestLink struct {
	Href string `json:"href"`
}

type requestLinks struct {
	Avatar *requestLink `json:"avatar,omitempty"`
}

type projectRequest struct {
	Name        string        `json:"name"`
	Key         string        `json:"key"`
	Description string        `json:"description,omitempty"`
	IsPrivate   bool          `json:"is_private"`
	Links       *requestLinks `json:"links,omitempty"`
}

type projectKeyRequest struct {
	Key string `json:"key"`
}

type repositoryRequest struct {
	Name        string             `json:"name"`
	Description string             `json:"description,omitempty"`
	IsPrivate   bool               `json:"is_private"`
	ForkPolicy  string             `json:"fork_policy,omitempty"`
	HasWiki     bool               `json:"has_wiki"`
	HasIssues   bool               `json:"has_issues"`
	Scm         string             `json:"scm,omitempty"`
	Language    string             `json:"language,omitempty"`
	Links       *requestLinks      `json:"links,omitempty"`
	Project     *projectKeyRequest `json:"project,omitempty"`
}

func requestLinksFromResource(d *schema.ResourceData) *requestLinks {
	links, ok := d.Get("link").([]interface{})
	if !ok || len(links) == 0 || links[0] == nil {
		return nil
	}

	avatar, ok := links[0].(map[string]interface{})["avatar"].([]interface{})
	if !ok || len(avatar) == 0 || avatar[0] == nil {
		return nil
	}

	href, _ := avatar[0].(map[string]interface{})["href"].(string)
	if href == "" {
		return nil
	}

	return &requestLinks{Avatar: &requestLink{Href: href}}
}

// projectRequestBody sends links only when withLinks is set, because the API accepts an avatar on create only.
func projectRequestBody(d *schema.ResourceData, withLinks bool) (*bytes.Buffer, error) {
	body := projectRequest{
		Name:        d.Get("name").(string),
		Key:         d.Get("key").(string),
		Description: d.Get("description").(string),
		IsPrivate:   d.Get("is_private").(bool),
	}

	if withLinks {
		body.Links = requestLinksFromResource(d)
	}

	return jsonBuffer(body)
}

func repositoryRequestBody(d *schema.ResourceData) (*bytes.Buffer, error) {
	body := repositoryRequest{
		Name:        d.Get("name").(string),
		Description: d.Get("description").(string),
		IsPrivate:   d.Get("is_private").(bool),
		ForkPolicy:  d.Get("fork_policy").(string),
		HasWiki:     d.Get("has_wiki").(bool),
		HasIssues:   d.Get("has_issues").(bool),
		Scm:         d.Get("scm").(string),
		Language:    d.Get("language").(string),
		Links:       requestLinksFromResource(d),
	}

	if key := d.Get("project_key").(string); key != "" {
		body.Project = &projectKeyRequest{Key: key}
	}

	return jsonBuffer(body)
}

func jsonBuffer(body interface{}) (*bytes.Buffer, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	return bytes.NewBuffer(payload), nil
}
