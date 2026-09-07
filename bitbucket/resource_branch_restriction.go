package bitbucket

import (
	"context"
	"fmt"
	"log"

	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/DrFaust92/bitbucket-go-client"
	"github.com/antihax/optional"
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

// branchRestrictionKey is what Bitbucket actually treats as the identity of a
// branch restriction. The numeric id is generated, and a restriction deleted and
// recreated by hand comes back under a different one, so matching on the id
// alone loses track of a restriction that is still there.
//
// The API enforces this key itself: creating a duplicate answers
//
//	409 a branch restriction with that kind (delete) and pattern (*) already exists (id=...)
type branchRestrictionKey struct {
	Kind            string
	BranchMatchKind string
	Pattern         string
	BranchType      string
}

func branchRestrictionKeyFromAPI(br bitbucket.Branchrestriction) branchRestrictionKey {
	return newBranchRestrictionKey(br.Kind, br.BranchMatchKind, br.Pattern, br.BranchType)
}

// newBranchRestrictionKey normalises a key so that values read from the API and
// values read from state compare equal.
func newBranchRestrictionKey(kind, matchKind, pattern, branchType string) branchRestrictionKey {
	// The API omits branch_match_kind when it holds the default.
	if matchKind == "" {
		matchKind = "glob"
	}

	key := branchRestrictionKey{Kind: kind, BranchMatchKind: matchKind}

	// Only the field the match kind actually uses takes part in the key:
	// Bitbucket still returns the other one, holding a stale or default value.
	if matchKind == "branching_model" {
		key.BranchType = branchType
	} else {
		key.Pattern = pattern
	}

	return key
}

// branchRestrictionConflictID matches the id Bitbucket names when a restriction
// with the same natural key already exists:
//
//	a branch restriction with that kind (delete) and pattern (*) already exists (id=74671495)
var branchRestrictionConflictID = regexp.MustCompile(`\(id=(\d+)\)`)

// existingBranchRestrictionID reports the id named in a conflict error, or ""
// when the message carries none. The wording is not contractual, so callers
// must cope with an empty result.
func existingBranchRestrictionID(err error) string {
	if err == nil {
		return ""
	}

	if m := branchRestrictionConflictID.FindStringSubmatch(err.Error()); m != nil {
		return m[1]
	}

	return ""
}

// branchRestrictionPriorKey builds the key from the values held before the
// current change. Update must recover by this key, not by the planned one:
// kind, pattern and branch_type are not ForceNew, so a plan may be moving the
// resource onto a key that another restriction already occupies, and adopting
// that one would overwrite a foreign object while orphaning our own.
func branchRestrictionPriorKey(d *schema.ResourceData) branchRestrictionKey {
	prior := func(field string) string {
		old, _ := d.GetChange(field)

		v, _ := old.(string)

		return v
	}

	// kind is Required, so a prior state always carries one. An empty kind here
	// means there is no diff attached at all, and the current values are the
	// prior ones.
	if prior("kind") == "" {
		return branchRestrictionKeyFromState(d)
	}

	return newBranchRestrictionKey(
		prior("kind"),
		prior("branch_match_kind"),
		prior("pattern"),
		prior("branch_type"),
	)
}

func branchRestrictionKeyFromState(d *schema.ResourceData) branchRestrictionKey {
	return newBranchRestrictionKey(
		d.Get("kind").(string),
		d.Get("branch_match_kind").(string),
		d.Get("pattern").(string),
		d.Get("branch_type").(string),
	)
}

// findBranchRestrictionByKey walks the repository's restrictions looking for the
// one occupying the given natural key. It returns (nil, nil) when the
// restriction is genuinely gone.
//
// Kind is filtered server side; the rest is compared locally, because the API
// offers no filter for branch_type.
func findBranchRestrictionByKey(c ProviderConfig, workspace, repo string, key branchRestrictionKey) (*bitbucket.Branchrestriction, error) {
	brApi := c.ApiClient.BranchRestrictionsApi

	options := bitbucket.BranchRestrictionsApiRepositoriesWorkspaceRepoSlugBranchRestrictionsGetOpts{
		Kind: optional.NewString(key.Kind),
	}

	// The page number is tracked here rather than read back from the response,
	// which omits it on some payloads. The cap is a backstop: a server that kept
	// returning a next link would otherwise spin this loop forever, and there is
	// no context to cancel it.
	const maxPages = 100

	for page := int32(1); page <= maxPages; page++ {
		restrictions, res, err := brApi.RepositoriesWorkspaceRepoSlugBranchRestrictionsGet(c.AuthContext, repo, workspace, &options)
		if err := handleClientError(res, err); err != nil {
			return nil, fmt.Errorf("listing branch restrictions of %s/%s: %w", workspace, repo, err)
		}

		// Indexed rather than ranged by value: taking the address of a range
		// variable is safe from Go 1.22 on, but older linters flag it and it
		// copies the struct for nothing.
		for i := range restrictions.Values {
			if branchRestrictionKeyFromAPI(restrictions.Values[i]) == key {
				return &restrictions.Values[i], nil
			}
		}

		if restrictions.Next == "" {
			return nil, nil
		}

		options.Page = optional.NewInt32(page + 1)
	}

	return nil, fmt.Errorf("listing branch restrictions of %s/%s: gave up after %d pages", workspace, repo, maxPages)
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
		// The restriction already exists but Terraform has never tracked it.
		// Adopting it silently would immediately overwrite whatever it holds,
		// so name it and leave the decision to the operator.
		if res != nil && res.StatusCode == http.StatusConflict {
			// The natural key is authoritative; the id quoted in the message is
			// only a hint, and nothing guarantees it refers to the restriction
			// this resource describes. Look the key up first and fall back to
			// the message only when the lookup cannot answer.
			existing := ""

			found, findErr := findBranchRestrictionByKey(c, workspace, repo, branchRestrictionKeyFromState(d))
			switch {
			case findErr != nil:
				// Report why the lookup failed instead of discarding it: a 429
				// or a scope problem here is the actual root cause, not the 409.
				return diag.Errorf(
					"creating the branch restriction conflicted with an existing one (%s), "+
						"and looking the existing one up failed: %s",
					err, findErr)
			case found != nil:
				existing = fmt.Sprintf("%v", found.Id)
			default:
				existing = existingBranchRestrictionID(err)
			}

			if existing != "" {
				return diag.Errorf(
					"a branch restriction of kind %q already exists on %s/%s with id %s; "+
						"import it instead of creating it: terraform import <resource address> %s/%s/%s",
					d.Get("kind").(string), workspace, repo, existing, workspace, repo, existing)
			}
		}

		return diag.FromErr(err)
	}

	d.SetId(fmt.Sprintf("%v", branchRestrictionReq.Id))

	return resourceBranchRestrictionsRead(ctx, d, m)
}

func resourceBranchRestrictionsRead(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	c := m.(Clients).genClient
	brApi := c.ApiClient.BranchRestrictionsApi

	// Collected rather than returned immediately: the read continues after an
	// adoption, and returning early would leave state unpopulated.
	var adopted diag.Diagnostics

	brRes, res, err := brApi.RepositoriesWorkspaceRepoSlugBranchRestrictionsIdGet(c.AuthContext, url.PathEscape(d.Id()),
		d.Get("repository").(string), d.Get("owner").(string))

	if res != nil && res.StatusCode == http.StatusNotFound {
		// The stored id is gone, but the restriction may simply have been
		// recreated by hand under a new one: Bitbucket keys a restriction by
		// kind and pattern, not by id.
		recovered, findErr := findBranchRestrictionByKey(c, d.Get("owner").(string), d.Get("repository").(string), branchRestrictionKeyFromState(d))
		if findErr != nil {
			return diag.FromErr(findErr)
		}

		if recovered == nil {
			log.Printf("[WARN] Branch Restrictions (%s) not found, removing from state", d.Id())
			d.SetId("")

			return nil
		}

		// Surface this in the plan rather than only in the log. A branch
		// restriction is a security control: that it was destroyed and
		// recreated outside Terraform is worth an operator's attention even
		// when the replacement happens to match the configuration.
		adopted = append(adopted, diag.Diagnostic{
			Severity: diag.Warning,
			Summary:  "Branch restriction was recreated outside Terraform",
			Detail: fmt.Sprintf(
				"Branch restriction %s on %s/%s no longer exists, but a restriction with the same kind and branch pattern or type is present under id %v. "+
					"Terraform adopted the new id. This usually means someone deleted and recreated the restriction in the Bitbucket UI.",
				d.Id(), d.Get("owner").(string), d.Get("repository").(string), recovered.Id),
		})

		log.Printf("[INFO] Branch Restriction (%s) was recreated outside Terraform, adopting its new id %v", d.Id(), recovered.Id)
		brRes = *recovered
	} else if err := handleClientError(res, err); err != nil {
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

	return adopted
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

	workspace := d.Get("owner").(string)
	repo := d.Get("repository").(string)

	_, res, err := brApi.RepositoriesWorkspaceRepoSlugBranchRestrictionsIdPut(c.AuthContext,
		*branchRestriction, url.PathEscape(d.Id()), repo, workspace)

	if res != nil && res.StatusCode == http.StatusNotFound {
		// Read normally refreshes just before this, but with -refresh=false, or
		// when the restriction is recreated between plan and apply, the id here
		// can still be stale. Recover the same way Read and Delete do.
		priorKey := branchRestrictionPriorKey(d)

		recovered, findErr := findBranchRestrictionByKey(c, workspace, repo, priorKey)
		if findErr != nil {
			return diag.FromErr(findErr)
		}

		if recovered == nil {
			return diag.Errorf(
				"branch restriction %s no longer exists on %s/%s and no restriction matches its kind and branch pattern; "+
					"remove it from state or re-run a plan to recreate it",
				d.Id(), workspace, repo)
		}

		log.Printf("[INFO] Branch Restriction (%s) was recreated outside Terraform, updating its new id %v instead", d.Id(), recovered.Id)
		d.SetId(fmt.Sprintf("%v", recovered.Id))

		_, res, err = brApi.RepositoriesWorkspaceRepoSlugBranchRestrictionsIdPut(c.AuthContext,
			*branchRestriction, url.PathEscape(d.Id()), repo, workspace)
	}

	if err := handleClientError(res, err); err != nil {
		return diag.FromErr(err)
	}

	return resourceBranchRestrictionsRead(ctx, d, m)
}

func resourceBranchRestrictionsDelete(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	c := m.(Clients).genClient
	brApi := c.ApiClient.BranchRestrictionsApi

	workspace := d.Get("owner").(string)
	repo := d.Get("repository").(string)

	res, err := brApi.RepositoriesWorkspaceRepoSlugBranchRestrictionsIdDelete(c.AuthContext, url.PathEscape(d.Id()),
		repo, workspace)

	if res != nil && res.StatusCode == http.StatusNotFound {
		// The stored id is gone, but the restriction may have been recreated by
		// hand under a new one. Reporting success here would drop the resource
		// from state and leave that restriction in place, unmanaged.
		recovered, findErr := findBranchRestrictionByKey(c, workspace, repo, branchRestrictionKeyFromState(d))
		if findErr != nil {
			return diag.FromErr(findErr)
		}

		if recovered == nil {
			log.Printf("[WARN] Branch Restrictions (%s) not found, nothing to delete", d.Id())
			return nil
		}

		log.Printf("[INFO] Branch Restriction (%s) was recreated outside Terraform, deleting its new id %v instead", d.Id(), recovered.Id)

		res, err = brApi.RepositoriesWorkspaceRepoSlugBranchRestrictionsIdDelete(c.AuthContext,
			url.PathEscape(fmt.Sprintf("%v", recovered.Id)), repo, workspace)
	}

	if err := handleClientError(res, err); err != nil {
		return diag.FromErr(err)
	}

	return nil
}
