package bitbucket

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/DrFaust92/bitbucket-go-client"
	"github.com/antihax/optional"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func resourceDeploymentVariable() *schema.Resource {
	return &schema.Resource{
		CreateWithoutTimeout: resourceDeploymentVariableCreate,
		UpdateWithoutTimeout: resourceDeploymentVariableUpdate,
		ReadWithoutTimeout:   resourceDeploymentVariableRead,
		DeleteWithoutTimeout: resourceDeploymentVariableDelete,
		Importer: &schema.ResourceImporter{
			State: func(d *schema.ResourceData, meta interface{}) ([]*schema.ResourceData, error) {
				idParts := strings.Split(d.Id(), "/")
				if len(idParts) != 3 || idParts[0] == "" || idParts[1] == "" || idParts[2] == "" {
					return nil, fmt.Errorf("unexpected format of ID (%q), expected DEPLOYMENT-ID/DEPLOYMENT-VARIABLE-ID", d.Id())
				}
				d.SetId(idParts[2])
				d.Set("deployment", strings.Join([]string{idParts[0], idParts[1]}, "/"))
				return []*schema.ResourceData{d}, nil
			},
		},

		Schema: map[string]*schema.Schema{
			"uuid": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"key": {
				Type:     schema.TypeString,
				Required: true,
			},
			"value": {
				Type:      schema.TypeString,
				Required:  true,
				Sensitive: true,
			},
			"secured": {
				Type:     schema.TypeBool,
				Optional: true,
				Default:  false,
			},
			"deployment": {
				Type:     schema.TypeString,
				Required: true,
			},
		},
	}
}

func newDeploymentVariableFromResource(d *schema.ResourceData) *bitbucket.DeploymentVariable {
	dk := &bitbucket.DeploymentVariable{
		Key:     d.Get("key").(string),
		Value:   d.Get("value").(string),
		Secured: d.Get("secured").(bool),
	}
	return dk
}

func parseDeploymentId(str string) (repository string, deployment string) {
	parts := strings.SplitN(str, ":", 2)
	return parts[0], parts[1]
}

func resourceDeploymentVariableCreate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	c := m.(Clients).genClient
	pipeApi := c.ApiClient.PipelinesApi
	rvcr := newDeploymentVariableFromResource(d)

	repository, deployment := parseDeploymentId(d.Get("deployment").(string))
	workspace, repoSlug, err := deployVarId(repository)
	if err != nil {
		return diag.FromErr(err)
	}

	rvRes, res, err := pipeApi.CreateDeploymentVariable(c.AuthContext, *rvcr, workspace, repoSlug, deployment)
	if err := handleClientError(res, err); err != nil {
		return diag.FromErr(err)
	}

	d.Set("uuid", rvRes.Uuid)
	d.SetId(rvRes.Uuid)

	time.Sleep(5000 * time.Millisecond) // sleep for a while, to allow BitBucket cache to catch up
	return resourceDeploymentVariableRead(ctx, d, m)
}

func resourceDeploymentVariableRead(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	c := m.(Clients).genClient

	repository, deployment := parseDeploymentId(d.Get("deployment").(string))
	workspace, repoSlug, err := deployVarId(repository)
	if err != nil {
		return diag.FromErr(err)
	}

	deployVar, err := findDeploymentVariable(c, workspace, repoSlug, deployment, d.Id())
	if err != nil {
		return diag.FromErr(err)
	}

	if deployVar == nil {
		log.Printf("[WARN] Deployment Variable (%s) not found, removing from state", d.Id())
		d.SetId("")
		return nil
	}

	if err := d.Set("key", deployVar.Key); err != nil {
		return diag.FromErr(err)
	}
	if err := d.Set("uuid", deployVar.Uuid); err != nil {
		return diag.FromErr(err)
	}
	if err := d.Set("secured", deployVar.Secured); err != nil {
		return diag.FromErr(err)
	}

	// A secured value is write-only: the API never returns it, so the value
	// already in state is the only copy there is.
	value := deployVar.Value
	if deployVar.Secured {
		value = d.Get("value").(string)
	}
	if err := d.Set("value", value); err != nil {
		return diag.FromErr(err)
	}

	return nil
}

// findDeploymentVariable walks every page of a deployment's variables.
// Bitbucket returns ten per page, so a single unpaginated call silently
// reports anything past the first page as deleted (upstream issue #254).
func findDeploymentVariable(c ProviderConfig, workspace, repoSlug, deployment, uuid string) (*bitbucket.DeploymentVariable, error) {
	options := bitbucket.PipelinesApiGetDeploymentVariablesOpts{}

	// The page number is tracked here rather than read back from the response,
	// which omits it on some payloads and would otherwise loop forever.
	for page := int32(1); ; page++ {
		vars, res, err := c.ApiClient.PipelinesApi.GetDeploymentVariables(c.AuthContext, workspace, repoSlug, deployment, &options)

		if res != nil && res.StatusCode == http.StatusNotFound {
			return nil, nil
		}

		if err := handleClientError(res, err); err != nil {
			return nil, err
		}

		for _, v := range vars.Values {
			if v.Uuid == uuid {
				return &v, nil
			}
		}

		if vars.Next == "" {
			return nil, nil
		}

		options.Page = optional.NewInt32(page + 1)
	}
}

func resourceDeploymentVariableUpdate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	c := m.(Clients).genClient
	pipeApi := c.ApiClient.PipelinesApi
	rvcr := newDeploymentVariableFromResource(d)

	repository, deployment := parseDeploymentId(d.Get("deployment").(string))
	workspace, repoSlug, err := deployVarId(repository)
	if err != nil {
		return diag.FromErr(err)
	}

	_, res, err := pipeApi.UpdateDeploymentVariable(c.AuthContext, *rvcr, workspace, repoSlug, deployment, d.Get("uuid").(string))
	if err := handleClientError(res, err); err != nil {
		return diag.FromErr(err)
	}

	return resourceDeploymentVariableRead(ctx, d, m)
}

func resourceDeploymentVariableDelete(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	c := m.(Clients).genClient
	pipeApi := c.ApiClient.PipelinesApi

	repository, deployment := parseDeploymentId(d.Get("deployment").(string))
	workspace, repoSlug, err := deployVarId(repository)
	if err != nil {
		return diag.FromErr(err)
	}

	res, err := pipeApi.DeleteDeploymentVariable(c.AuthContext, workspace, repoSlug, deployment, d.Get("uuid").(string))
	if err := handleClientError(res, err); err != nil {
		return diag.FromErr(err)
	}

	return nil
}

func deployVarId(repo string) (string, string, error) {
	idparts := strings.Split(repo, "/")
	if len(idparts) == 2 {
		return idparts[0], idparts[1], nil
	} else {
		return "", "", fmt.Errorf("incorrect ID format, should match `owner/key`")
	}
}
