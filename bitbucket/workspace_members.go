package bitbucket

import (
	"fmt"
	"sync"

	"github.com/DrFaust92/bitbucket-go-client"
	"github.com/antihax/optional"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

// workspaceMemberCache memoizes a workspace's display name to UUID index.
//
// Resources address users by display name, the API needs a UUID, and the only
// way to map one to the other is to page through every workspace member. A
// plan touching many branch restrictions would otherwise repeat that walk once
// per resource, which is exactly the traffic that triggers rate limiting.
type workspaceMemberCache struct {
	mu      sync.Mutex
	indexes map[string]map[string]string
}

func newWorkspaceMemberCache() *workspaceMemberCache {
	return &workspaceMemberCache{indexes: map[string]map[string]string{}}
}

// uuidsByDisplayName returns the workspace's index, fetching it at most once.
// The lock is held across the fetch so concurrent resources wait for a single
// walk instead of each starting their own.
func (c *workspaceMemberCache) uuidsByDisplayName(pc ProviderConfig, workspace string) (map[string]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if index, ok := c.indexes[workspace]; ok {
		return index, nil
	}

	index := map[string]string{}
	options := bitbucket.WorkspacesApiWorkspacesWorkspaceMembersGetOpts{}

	// The page number is tracked here rather than read back from the response,
	// which omits it on some payloads and would otherwise loop forever.
	for page := int32(1); ; page++ {
		members, res, err := pc.ApiClient.WorkspacesApi.WorkspacesWorkspaceMembersGet(pc.AuthContext, workspace, &options)
		if err := handleClientError(res, err); err != nil {
			return nil, fmt.Errorf("listing members of workspace %q: %w", workspace, err)
		}

		for _, member := range members.Values {
			if member.User == nil || member.User.DisplayName == "" {
				continue
			}

			name := member.User.DisplayName
			if existing, ok := index[name]; ok && existing != member.User.Uuid {
				return nil, fmt.Errorf(
					"workspace %q has more than one member named %q (%s and %s); use the account UUID instead",
					workspace, name, existing, member.User.Uuid)
			}

			index[name] = member.User.Uuid
		}

		if members.Next == "" {
			break
		}

		options.Page = optional.NewInt32(page + 1)
	}

	c.indexes[workspace] = index

	return index, nil
}

// resolve maps configured user identifiers onto accounts the API accepts. A
// UUID is passed through untouched, so no lookup is needed for configurations
// that already use them.
func (c *workspaceMemberCache) resolve(pc ProviderConfig, workspace string, names *schema.Set) ([]bitbucket.Account, error) {
	if names == nil || names.Len() == 0 {
		return nil, nil
	}

	var index map[string]string

	accounts := make([]bitbucket.Account, 0, names.Len())

	for _, item := range names.List() {
		name := item.(string)

		if isAccountUUID(name) {
			accounts = append(accounts, bitbucket.Account{Uuid: name})
			continue
		}

		if index == nil {
			var err error
			if index, err = c.uuidsByDisplayName(pc, workspace); err != nil {
				return nil, err
			}
		}

		uuid, ok := index[name]
		if !ok {
			return nil, fmt.Errorf("no member of workspace %q has the display name %q", workspace, name)
		}

		accounts = append(accounts, bitbucket.Account{Uuid: uuid})
	}

	return accounts, nil
}

// isAccountUUID reports whether a configured value is already a Bitbucket
// account UUID, which the API always writes in braces.
func isAccountUUID(s string) bool {
	return len(s) > 2 && s[0] == '{' && s[len(s)-1] == '}'
}
