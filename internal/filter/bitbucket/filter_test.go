package bitbucket

import (
	"encoding/json"
	"testing"
)

func TestPayloadFilter(t *testing.T) {
	filter := NewPayloadFilter()

	input := `
{
  "eventKey": "pr:opened",
  "actor": {
    "name": "actor-name",
    "displayName": "Actor Name",
    "id": 123,
    "slug": "actor-slug",
    "emailAddress": "actor@example.com",
    "links": { "self": [] }
  },
  "pullRequest": {
    "id": 101,
    "title": "PR Title",
    "description": "PR Description",
    "links": { "self": [] },
    "reviewers": [ { "user": { "name": "r1" } } ],
    "participants": [],
    "author": {
      "user": {
        "name": "author-name",
        "displayName": "Author Name",
        "id": 456,
        "slug": "author-slug",
        "emailAddress": "author@example.com",
        "links": { "self": [] }
      }
    },
    "toRef": {
      "id": "refs/heads/master",
      "displayId": "master",
      "latestCommit": "abcdef",
      "repository": {
        "slug": "repo-slug",
        "name": "repo-name",
        "project": {
           "key": "PROJ",
           "name": "Project Name"
        },
        "links": { "self": [] }
      }
    }
  }
}
`

	output := filter.Filter([]byte(input))

	var result map[string]interface{}
	json.Unmarshal(output, &result)

	// Check top-level pruning
	if _, ok := result["actor"]; ok {
		t.Error("expected actor to be pruned")
	}

	pr, _ := result["pullRequest"].(map[string]interface{})

	// Check nested pruning
	if _, ok := pr["links"]; ok {
		t.Error("expected pr.links to be pruned")
	}
	if _, ok := pr["reviewers"]; ok {
		t.Error("expected pr.reviewers to be pruned")
	}

	// Check Author simplification
	author, _ := pr["author"].(map[string]interface{})
	user, _ := author["user"].(map[string]interface{})
	if _, ok := user["emailAddress"]; ok {
		t.Error("expected author email to be pruned")
	}
	if _, ok := user["id"]; ok {
		t.Error("expected author id to be pruned")
	}
	if _, ok := user["displayName"]; !ok {
		t.Error("expected author displayName to be kept")
	}

	// Check Repository simplification
	toRef, _ := pr["toRef"].(map[string]interface{})
	repo, _ := toRef["repository"].(map[string]interface{})
	if _, ok := repo["name"]; ok {
		t.Error("expected repo name to be pruned (we keep slug)")
	}
	if _, ok := repo["links"]; ok {
		t.Error("expected repo links to be pruned")
	}
	project, _ := repo["project"].(map[string]interface{})
	if _, ok := project["name"]; ok {
		t.Error("expected project name to be pruned")
	}
	if _, ok := project["key"]; !ok {
		t.Error("expected project key to be kept")
	}
}

func TestResponseFilter_Comments(t *testing.T) {
	filter := NewResponseFilter()
	input := `
{
  "values": [
    {
      "id": 1,
      "version": 1,
      "text": "comment text",
      "author": {
        "name": "author",
        "displayName": "Author",
        "emailAddress": "a@b.com",
        "links": {}
      },
      "createdDate": 1234567890,
      "updatedDate": 1234567890,
      "content": {
        "raw": "comment content",
        "markup": "markdown",
        "html": "<p>html</p>"
      },
      "links": {}
    }
  ]
}
`
	// output := filter.Filter("bitbucket_get_pull_request_comments", []byte(input)) // input needs to be unmarshaled first for real usage usage logic test below covers it correctly

	var inputMap map[string]interface{}
	json.Unmarshal([]byte(input), &inputMap)

	resultAny := filter.Filter("bitbucket_get_pull_request_comments", inputMap)
	result := resultAny.(map[string]interface{})
	values := result["values"].([]interface{})
	comment := values[0].(map[string]interface{})

	if _, ok := comment["createdDate"]; ok {
		t.Error("expected createdDate to be pruned")
	}
	if _, ok := comment["links"]; ok {
		t.Error("expected links to be pruned")
	}

	author := comment["author"].(map[string]interface{})
	if _, ok := author["emailAddress"]; ok {
		t.Error("expected author email to be pruned")
	}

	content := comment["content"].(map[string]interface{})
	if _, ok := content["html"]; ok {
		t.Error("expected content html to be pruned")
	}
	if _, ok := content["raw"]; !ok {
		t.Error("expected content raw to be kept")
	}
}
