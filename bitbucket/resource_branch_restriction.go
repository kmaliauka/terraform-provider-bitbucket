package bitbucket

import (
	"context"
	"fmt"
	"log"

	"net/http"
	"net/url"
	"strings"

	"github.com/DrFaust92/bitbucket-go-client"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
)

// BranchRestriction is the data we need to send to create a new branch restriction for the repository
type BranchRestriction struct {
	ID              int     `json:"id,omitempty"`
	Kind            string  `json:"kind,omitempty"`
	BranchMatchkind string  `json:"branch_match_kind,omitempty"`
	BranchType      string  `json:"branch_type,omitempty"`
	Pattern         string  `json:"pattern,omitempty"`
	Value           int     `json:"value,omitempty"`
	Users           []User  `json:"users,omitempty"`
	Groups          []Group `json:"groups,omitempty"`
}

// User is just the user struct we want to use for BranchRestrictions
type User struct {
	Username string `json:"username,omitempty"`
}

// Group is the group we want to add to a branch restriction
type Group struct {
	Slug  string `json:"slug,omitempty"`
	Owner User   `json:"owner,omitempty"`
}

func resourceBranchRestriction() *schema.Resource {
	return &schema.Resource{
		CreateContext: resourceBranchRestrictionsCreate,
		ReadContext:   resourceBranchRestrictionsRead,
		UpdateContext: resourceBranchRestrictionsUpdate,
		DeleteContext: resourceBranchRestrictionsDelete,
		Importer: &schema.ResourceImporter{
			State: func(d *schema.ResourceData, meta interface{}) ([]*schema.ResourceData, error) {
				idParts := strings.Split(d.Id(), "/")
				if len(idParts) != 3 || idParts[0] == "" || idParts[1] == "" || idParts[2] == "" {
					return nil, fmt.Errorf("unexpected format of ID (%q), expected OWNER/REPO/BRANCH-RESTRICTION-ID", d.Id())
				}
				d.SetId(idParts[2])
				d.Set("owner", idParts[0])
				d.Set("repository", idParts[1])
				return []*schema.ResourceData{d}, nil
			},
		},

		Schema: map[string]*schema.Schema{
			"owner": {
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},
			"repository": {
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},
			"kind": {
				Type:     schema.TypeString,
				Required: true,
				ValidateFunc: validation.StringInSlice([]string{
					"allow_auto_merge_when_builds_pass",
					"delete",
					"enforce_merge_checks",
					"force",
					"push",
					"require_all_dependencies_merged",
					"require_approvals_to_merge",
					"require_commits_behind",
					"require_default_reviewer_approvals_to_merge",
					"require_no_changes_requested",
					"require_passing_builds_to_merge",
					"require_tasks_to_be_completed",
					"reset_pullrequest_approvals_on_change",
					"reset_pullrequest_changes_requested_on_change",
					"restrict_merges",
					"smart_reset_pullrequest_approvals",
				}, false),
			},
			"branch_match_kind": {
				Type:         schema.TypeString,
				Optional:     true,
				Default:      "glob",
				ValidateFunc: validation.StringInSlice([]string{"branching_model", "glob"}, false),
			},
			"pattern": {
				Type:     schema.TypeString,
				Optional: true,
			},
			"branch_type": {
				Type:         schema.TypeString,
				Optional:     true,
				ValidateFunc: validation.StringInSlice([]string{"feature", "bugfix", "release", "hotfix", "development", "production"}, false),
			},
			"users": {
				Type:     schema.TypeSet,
				Elem:     &schema.Schema{Type: schema.TypeString},
				Optional: true,
				Set:      schema.HashString,
			},
			"groups": {
				Type: schema.TypeSet,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"owner": {
							Type:     schema.TypeString,
							Required: true,
						},
						"slug": {
							Type:     schema.TypeString,
							Required: true,
						},
					},
				},
				Optional: true,
			},

			"value": {
				Type:     schema.TypeInt,
				Optional: true,
			},
		},
	}
}

func createBranchRestriction(d *schema.ResourceData, users []bitbucket.Account) *bitbucket.Branchrestriction {
	groups := make([]bitbucket.Group, 0, d.Get("groups").(*schema.Set).Len())

	for _, item := range d.Get("groups").(*schema.Set).List() {
		m := item.(map[string]interface{})

		account := &bitbucket.Account{
			Username: m["owner"].(string),
		}

		group := bitbucket.Group{
			Owner: account,
			Slug:  m["slug"].(string),
		}

		groups = append(groups, group)
	}

	restict := &bitbucket.Branchrestriction{
		Kind:   d.Get("kind").(string),
		Value:  int32(d.Get("value").(int)),
		Users:  users,
		Groups: groups,
	}

	if v, ok := d.GetOk("pattern"); ok {
		restict.Pattern = v.(string)
	}

	if v, ok := d.GetOk("branch_type"); ok {
		restict.BranchType = v.(string)
	}

	if v, ok := d.GetOk("branch_match_kind"); ok {
		restict.BranchMatchKind = v.(string)
	}

	return restict
}

func resourceBranchRestrictionsCreate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	c := m.(Clients).genClient
	brApi := c.ApiClient.BranchRestrictionsApi

	repo := d.Get("repository").(string)
	workspace := d.Get("owner").(string)

	users, err := m.(Clients).members.resolve(c, workspace, d.Get("users").(*schema.Set))
	if err != nil {
		return diag.FromErr(err)
	}
	branchRestriction := createBranchRestriction(d, users)

	branchRestrictionReq, res, err := brApi.RepositoriesWorkspaceRepoSlugBranchRestrictionsPost(c.AuthContext, *branchRestriction, repo, workspace)
	if err := handleClientError(res, err); err != nil {
		return diag.FromErr(err)
	}

	d.SetId(fmt.Sprintf("%v", branchRestrictionReq.Id))

	return resourceBranchRestrictionsRead(ctx, d, m)
}

func resourceBranchRestrictionsRead(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	c := m.(Clients).genClient
	brApi := c.ApiClient.BranchRestrictionsApi

	brRes, res, err := brApi.RepositoriesWorkspaceRepoSlugBranchRestrictionsIdGet(c.AuthContext, url.PathEscape(d.Id()),
		d.Get("repository").(string), d.Get("owner").(string))

	if res != nil && res.StatusCode == http.StatusNotFound {
		log.Printf("[WARN] Branch Restrictions (%s) not found, removing from state", d.Id())
		d.SetId("")
		return nil
	}

	if err := handleClientError(res, err); err != nil {
		return diag.FromErr(err)
	}

	d.SetId(fmt.Sprintf("%v", brRes.Id))
	d.Set("kind", brRes.Kind)
	d.Set("pattern", brRes.Pattern)
	d.Set("value", brRes.Value)
	if err := d.Set("users", flattenBranchRestrictionUsers(brRes.Users)); err != nil {
		return diag.FromErr(err)
	}
	if err := d.Set("groups", flattenBranchRestrictionGroups(brRes.Groups)); err != nil {
		return diag.FromErr(err)
	}
	d.Set("branch_type", brRes.BranchType)
	d.Set("branch_match_kind", brRes.BranchMatchKind)

	return nil
}

// flattenBranchRestrictionUsers stores the display name Bitbucket returns,
// which is the identifier the users argument accepts. Bitbucket deprecated
// username and no longer returns it, so the UUID is the only fallback left for
// an account with no display name.
func flattenBranchRestrictionUsers(accounts []bitbucket.Account) []string {
	users := make([]string, 0, len(accounts))

	for _, acc := range accounts {
		switch {
		case acc.DisplayName != "":
			users = append(users, acc.DisplayName)
		case acc.Uuid != "":
			users = append(users, acc.Uuid)
		}
	}

	return users
}

// flattenBranchRestrictionGroups resolves the workspace slug that the owner
// argument expects. A group's owner account carries a human readable display
// name rather than a slug, so the workspace object is preferred over it.
func flattenBranchRestrictionGroups(groups []bitbucket.Group) []interface{} {
	out := make([]interface{}, 0, len(groups))

	for _, g := range groups {
		owner := ""

		switch {
		case g.Workspace != nil && g.Workspace.Slug != "":
			owner = g.Workspace.Slug
		case g.FullSlug != "":
			owner, _, _ = strings.Cut(g.FullSlug, ":")
		case g.Owner != nil && g.Owner.Username != "":
			owner = g.Owner.Username
		}

		out = append(out, map[string]interface{}{
			"owner": owner,
			"slug":  g.Slug,
		})
	}

	return out
}

func resourceBranchRestrictionsUpdate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	c := m.(Clients).genClient
	brApi := c.ApiClient.BranchRestrictionsApi

	users, err := m.(Clients).members.resolve(c, d.Get("owner").(string), d.Get("users").(*schema.Set))
	if err != nil {
		return diag.FromErr(err)
	}
	branchRestriction := createBranchRestriction(d, users)

	_, res, err := brApi.RepositoriesWorkspaceRepoSlugBranchRestrictionsIdPut(c.AuthContext,
		*branchRestriction, url.PathEscape(d.Id()),
		d.Get("repository").(string), d.Get("owner").(string))

	if err := handleClientError(res, err); err != nil {
		return diag.FromErr(err)
	}

	return resourceBranchRestrictionsRead(ctx, d, m)
}

func resourceBranchRestrictionsDelete(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	c := m.(Clients).genClient
	brApi := c.ApiClient.BranchRestrictionsApi

	res, err := brApi.RepositoriesWorkspaceRepoSlugBranchRestrictionsIdDelete(c.AuthContext, url.PathEscape(d.Id()),
		d.Get("repository").(string), d.Get("owner").(string))

	if res != nil && res.StatusCode == http.StatusNotFound {
		log.Printf("[WARN] Branch Restrictions (%s) not found, removing from state", d.Id())
		return nil
	}

	if err := handleClientError(res, err); err != nil {
		return diag.FromErr(err)
	}

	return nil
}
