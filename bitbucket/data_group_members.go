package bitbucket

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"

	"github.com/DrFaust92/bitbucket-go-client"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func dataGroupMembers() *schema.Resource {
	return &schema.Resource{
		ReadWithoutTimeout: dataReadGroupMembers,

		Schema: map[string]*schema.Schema{
			"workspace": {
				Type:     schema.TypeString,
				Required: true,
			},
			"slug": {
				Type:     schema.TypeString,
				Required: true,
			},
			"members": {
				Type:       schema.TypeSet,
				Elem:       &schema.Schema{Type: schema.TypeString},
				Computed:   true,
				Deprecated: "use group_members instead",
			},
			"group_members": {
				Type:     schema.TypeSet,
				Computed: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"uuid": {
							Type:     schema.TypeString,
							Computed: true,
						},
						"username": {
							Type:     schema.TypeString,
							Computed: true,
						},
						"display_name": {
							Type:     schema.TypeString,
							Computed: true,
						},
					},
				},
			},
		},
	}
}

func dataReadGroupMembers(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	client := m.(Clients).httpClient

	workspace := d.Get("workspace").(string)
	slug := d.Get("slug").(string)

	groupsReq, err := client.Get(fmt.Sprintf("1.0/groups/%s/%s/members", workspace, slug))
	if err != nil {
		return diag.FromErr(err)
	}

	if groupsReq == nil || groupsReq.Body == nil {
		return diag.Errorf("error reading Group (%s): empty response", d.Id())
	}

	var members []bitbucket.Account

	body, readerr := io.ReadAll(groupsReq.Body)
	if readerr != nil {
		return diag.FromErr(readerr)
	}

	log.Printf("[DEBUG] Group Membership Response JSON: %v", string(body))

	decodeerr := json.Unmarshal(body, &members)
	if decodeerr != nil {
		return diag.FromErr(decodeerr)
	}

	log.Printf("[DEBUG] Group Membership Response Decoded: %#v", members)

	var mems []string
	for _, mbr := range members {
		mems = append(mems, mbr.Uuid)
	}

	d.SetId(fmt.Sprintf("%s/%s", workspace, slug))
	d.Set("workspace", workspace)
	d.Set("slug", slug)
	d.Set("members", mems)
	d.Set("group_members", flattenAccounts(members))

	return nil
}

// flattenAccounts shapes the members of a workspace group.
//
// It keeps the historical field mapping of this data source: the group API is
// the deprecated 1.0 endpoint, whose accounts carry a different set of fields
// from 2.0 workspace members, so the two are flattened separately.
func flattenAccounts(accounts []bitbucket.Account) []interface{} {
	if len(accounts) == 0 {
		return nil
	}

	tfList := make([]interface{}, 0, len(accounts))

	for _, account := range accounts {
		tfList = append(tfList, map[string]interface{}{
			"uuid":         account.Uuid,
			"username":     account.DisplayName,
			"display_name": account.Username,
		})
	}

	return tfList
}
