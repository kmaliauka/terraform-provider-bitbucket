package bitbucket

import (
	"testing"
)

func TestNextPageEndpoint(t *testing.T) {
	cases := []struct {
		name string
		next string
		want string
	}{
		{
			name: "last page",
			next: "",
			want: "",
		},
		{
			name: "absolute link is turned into a relative endpoint",
			next: "https://api.bitbucket.org/2.0/workspaces/noogadev/members?page=2",
			want: "2.0/workspaces/noogadev/members?page=2",
		},
		{
			name: "link without a query",
			next: "https://api.bitbucket.org/2.0/workspaces/noogadev/members",
			want: "2.0/workspaces/noogadev/members",
		},
		{
			name: "unparseable link stops pagination rather than looping",
			next: "://not a url",
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := nextPageEndpoint(tc.next); got != tc.want {
				t.Fatalf("nextPageEndpoint(%q) = %q, want %q", tc.next, got, tc.want)
			}
		})
	}
}

func TestFlattenWorkspaceMembers(t *testing.T) {
	// Bitbucket really does return a display name and a nickname that differ in
	// spelling, which is why account_id is the only key worth joining on.
	users := []WorkspaceMemberUser{
		{
			AccountID:   "557058:1f2da371-cc1f-49d6-b18a-db22fc4e9617",
			Uuid:        "{856dc97c-a640-4f09-be2a-f34e39330978}",
			DisplayName: "Aliaksandr Liovachkin",
			Nickname:    "Aliaksandr Levochkin",
		},
	}

	result := flattenWorkspaceMembers(users)
	if len(result) != 1 {
		t.Fatalf("expected 1 member, got %d", len(result))
	}

	got := result[0].(map[string]interface{})
	want := map[string]interface{}{
		"uuid":         "{856dc97c-a640-4f09-be2a-f34e39330978}",
		"account_id":   "557058:1f2da371-cc1f-49d6-b18a-db22fc4e9617",
		"display_name": "Aliaksandr Liovachkin",
		"nickname":     "Aliaksandr Levochkin",
		// Kept as the display name so existing configurations keep resolving.
		"username": "Aliaksandr Liovachkin",
	}

	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
}

func TestFlattenWorkspaceMembersEmpty(t *testing.T) {
	if got := flattenWorkspaceMembers(nil); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}
