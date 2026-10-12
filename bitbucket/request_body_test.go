package bitbucket

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func decodeBody(t *testing.T, raw []byte) map[string]interface{} {
	t.Helper()

	var body map[string]interface{}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("body is not a JSON object: %v", err)
	}

	return body
}

func TestProjectRequestBody(t *testing.T) {
	d := schema.TestResourceDataRaw(t, resourceProject().Schema, map[string]interface{}{
		"owner":       "noogadev",
		"name":        "cargoflow",
		"key":         "CF",
		"description": "CargoFlowAI project",
		"is_private":  true,
		"link": []interface{}{map[string]interface{}{
			"avatar": []interface{}{map[string]interface{}{"href": "https://example.com/avatar.png"}},
		}},
	})

	cases := []struct {
		name      string
		withLinks bool
		want      map[string]interface{}
	}{
		{
			name:      "create sends the avatar",
			withLinks: true,
			want: map[string]interface{}{
				"name":        "cargoflow",
				"key":         "CF",
				"description": "CargoFlowAI project",
				"is_private":  true,
				"links": map[string]interface{}{
					"avatar": map[string]interface{}{"href": "https://example.com/avatar.png"},
				},
			},
		},
		{
			name:      "update omits links",
			withLinks: false,
			want: map[string]interface{}{
				"name":        "cargoflow",
				"key":         "CF",
				"description": "CargoFlowAI project",
				"is_private":  true,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload, err := projectRequestBody(d, tc.withLinks)
			if err != nil {
				t.Fatal(err)
			}

			if got := decodeBody(t, payload.Bytes()); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRepositoryRequestBody(t *testing.T) {
	d := schema.TestResourceDataRaw(t, resourceRepository().Schema, map[string]interface{}{
		"owner":       "noogadev",
		"name":        "cf-app",
		"description": "CargoFlowAI application",
		"is_private":  true,
		"fork_policy": "no_public_forks",
		"scm":         "git",
		"project_key": "CF",
	})

	payload, err := repositoryRequestBody(d)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]interface{}{
		"name":        "cf-app",
		"description": "CargoFlowAI application",
		"is_private":  true,
		"fork_policy": "no_public_forks",
		"has_wiki":    false,
		"has_issues":  false,
		"scm":         "git",
		"project":     map[string]interface{}{"key": "CF"},
	}
	if got := decodeBody(t, payload.Bytes()); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
