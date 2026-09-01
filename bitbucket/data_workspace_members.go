package bitbucket

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/url"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

// WorkspaceMember is one entry of 2.0/workspaces/{workspace}/members.
//
// The generated swagger client is bypassed here because its Account model drops
// account_id and nickname, and account_id is the only stable key that joins a
// Bitbucket user to the same person in the Atlassian directory.
type WorkspaceMember struct {
	User *WorkspaceMemberUser `json:"user,omitempty"`
}

type WorkspaceMemberUser struct {
	AccountID   string `json:"account_id,omitempty"`
	Uuid        string `json:"uuid,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	Nickname    string `json:"nickname,omitempty"`
}

type paginatedWorkspaceMembers struct {
	Values []WorkspaceMember `json:"values"`
	Next   string            `json:"next,omitempty"`
}

func dataWorkspaceMembers() *schema.Resource {
	return &schema.Resource{
		ReadWithoutTimeout: dataReadWorkspaceMembers,

		Schema: map[string]*schema.Schema{
			"workspace": {
				Type:     schema.TypeString,
				Required: true,
			},
			"members": {
				Type:       schema.TypeSet,
				Elem:       &schema.Schema{Type: schema.TypeString},
				Computed:   true,
				Deprecated: "use workspace_members instead",
			},
			"workspace_members": {
				Type:     schema.TypeSet,
				Computed: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"uuid": {
							Type:     schema.TypeString,
							Computed: true,
						},
						"account_id": {
							Type:     schema.TypeString,
							Computed: true,
						},
						"nickname": {
							Type:     schema.TypeString,
							Computed: true,
						},
						"display_name": {
							Type:     schema.TypeString,
							Computed: true,
						},
						"username": {
							Type:     schema.TypeString,
							Computed: true,
							Deprecated: "Bitbucket no longer returns usernames. This carries the display name " +
								"for backwards compatibility; use display_name instead.",
						},
					},
				},
			},
		},
	}
}

func dataReadWorkspaceMembers(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	client := m.(Clients).httpClient

	workspace := d.Get("workspace").(string)

	var (
		members []string
		users   []WorkspaceMemberUser
	)

	endpoint := "2.0/workspaces/" + url.PathEscape(workspace) + "/members"

	for endpoint != "" {
		res, err := client.Get(endpoint)
		if err != nil {
			return diag.FromErr(err)
		}
		if res == nil || res.Body == nil {
			return diag.Errorf("error reading members of workspace (%s): empty response", workspace)
		}

		body, err := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if err != nil {
			return diag.FromErr(err)
		}

		var page paginatedWorkspaceMembers
		if err := json.Unmarshal(body, &page); err != nil {
			return diag.FromErr(err)
		}

		for _, member := range page.Values {
			if member.User == nil {
				continue
			}

			members = append(members, member.User.Uuid)
			users = append(users, *member.User)
		}

		endpoint = nextPageEndpoint(page.Next)
	}

	log.Printf("[DEBUG] Workspace (%s) has %d members", workspace, len(users))

	d.SetId(workspace)

	if err := d.Set("workspace", workspace); err != nil {
		return diag.FromErr(err)
	}
	if err := d.Set("members", members); err != nil {
		return diag.FromErr(err)
	}
	if err := d.Set("workspace_members", flattenWorkspaceMembers(users)); err != nil {
		return diag.FromErr(err)
	}

	return nil
}

// nextPageEndpoint turns the absolute "next" link the API returns into the
// relative endpoint Client.Do expects, and reports "" when the last page has
// been read.
func nextPageEndpoint(next string) string {
	if next == "" {
		return ""
	}

	parsed, err := url.Parse(next)
	if err != nil {
		log.Printf("[WARN] Could not parse pagination link %q, stopping", next)
		return ""
	}

	endpoint := strings.TrimPrefix(parsed.Path, "/")
	if parsed.RawQuery != "" {
		endpoint += "?" + parsed.RawQuery
	}

	return endpoint
}

func flattenWorkspaceMembers(users []WorkspaceMemberUser) []interface{} {
	if len(users) == 0 {
		return nil
	}

	tfList := make([]interface{}, 0, len(users))

	for _, user := range users {
		tfList = append(tfList, map[string]interface{}{
			"uuid":         user.Uuid,
			"account_id":   user.AccountID,
			"nickname":     user.Nickname,
			"display_name": user.DisplayName,
			// Kept pointing at the display name: configurations written against
			// earlier versions index members by this field.
			"username": user.DisplayName,
		})
	}

	return tfList
}
