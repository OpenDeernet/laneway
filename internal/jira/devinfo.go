package jira

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Development info: the pull requests and branches source control links to
// an issue, from the dev-status API Jira's own development panel reads. It
// is not in the public docs, so everything here degrades to "none".

// DevItem is a pull request or branch linked to an issue.
type DevItem struct {
	Kind   string // "pr", "branch", "commit", "build" or "deploy"
	Name   string // PR title, branch name, build name and number, deployment
	Status string // OPEN, MERGED, DECLINED; a build's or deployment's state; "" for a branch
	Repo   string
	Branch string // a PR's source → destination; a deployment's environment
	URL    string
	Tool   string // the instance type: GitHub, GitLab, …

	// The rest is detail the web's development panel shows; zero when the
	// tool does not send it.
	Author, AuthorAvatar string        // a PR's or commit's author; a branch's last committer
	Reviewers            []DevReviewer // a PR's
	Updated              time.Time     // last update; a commit's author time
	Source, Target       string        // a PR's branches
	Hash, ShortHash      string        // a commit's, or a branch's last commit's
	Message              string        // that commit's first line
	RepoURL              string
	Comments             int       // a PR's comment count
	Tests                *DevTests // a build's test summary
	Duration             int       // a deployment's, in seconds
	EnvType              string    // a deployment's environment type: production, staging, …
	CreatePR             string    // a branch's "create pull request" link
}

// DevReviewer is a pull request's reviewer.
type DevReviewer struct {
	Name, Avatar string
	Approved     bool
}

// DevTests is a build's test summary.
type DevTests struct{ Total, Passed, Failed, Skipped int }

// devTime is a dev-status time: an ISO string (with or without the colon
// in the zone) or epoch milliseconds.
type devTime struct{ time.Time }

func (t *devTime) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		return nil
	}
	if ms, err := strconv.ParseInt(s, 10, 64); err == nil {
		t.Time = time.UnixMilli(ms).UTC()
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.000-0700", "2006-01-02T15:04:05-0700"} {
		if v, err := time.Parse(layout, s); err == nil {
			t.Time = v
			return nil
		}
	}
	return nil // unknown: no time rather than no dev info
}

type devPerson struct {
	Name   string `json:"name"`
	Avatar string `json:"avatar"`
}

type devCommit struct {
	ID              string    `json:"id"`
	DisplayID       string    `json:"displayId"`
	Message         string    `json:"message"`
	URL             string    `json:"url"`
	AuthorTimestamp devTime   `json:"authorTimestamp"`
	Author          devPerson `json:"author"`
}

// DevInfo lists key's pull requests (open first), builds, deployments,
// branches and commits.
func (c *Client) DevInfo(ctx context.Context, key string) ([]DevItem, error) {
	if !c.Enabled() {
		return nil, errNotConfigured
	}
	if c.atl != nil {
		return nil, nil // dev-status is private: Atlassian keeps apps out
	}
	var iss struct {
		ID string `json:"id"`
	}
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/issue/"+url.PathEscape(key)+"?fields=summary", key, nil, &iss); err != nil {
		return nil, err
	}
	var sum struct {
		Summary map[string]struct {
			ByInstanceType map[string]struct {
				Count int `json:"count"`
			} `json:"byInstanceType"`
		} `json:"summary"`
	}
	if err := c.do(ctx, http.MethodGet, "/rest/dev-status/latest/issue/summary?issueId="+url.QueryEscape(iss.ID), key, nil, &sum); err != nil {
		return nil, err
	}
	var out []DevItem
	var firstErr error
	// A detail answer carries more than it was asked for (branch brings its pull
	// requests, pullrequest its branches), so the same item comes back from two
	// calls. Keep one, the copy from the call for its own kind.
	seen := map[string]int{} // kind + url (or repo + name) -> index in out
	ownKind := map[string]string{"pullrequest": "pr", "branch": "branch", "repository": "commit", "build": "build", "deployment-environment": "deploy"}
	own := map[int]bool{}
	for _, dataType := range []string{"pullrequest", "branch", "repository", "build", "deployment-environment"} {
		tools := sum.Summary[dataType].ByInstanceType
		names := make([]string, 0, len(tools))
		for t, v := range tools {
			if v.Count > 0 {
				names = append(names, t)
			}
		}
		sort.Strings(names)
		for _, tool := range names {
			items, err := c.devDetail(ctx, iss.ID, tool, dataType)
			if err != nil { // one tool failing leaves the others
				firstErr = cmp.Or(firstErr, err)
				continue
			}
			for _, it := range items {
				k := it.Kind + " " + cmp.Or(it.URL, it.Repo+" "+it.Name+" "+it.Hash)
				mine := ownKind[dataType] == it.Kind
				if i, ok := seen[k]; ok {
					if mine && !own[i] {
						out[i], own[i] = it, true
					}
					continue
				}
				seen[k], own[len(out)] = len(out), mine
				out = append(out, it)
			}
		}
	}
	if len(out) == 0 && firstErr != nil {
		return nil, firstErr
	}
	slices.SortStableFunc(out, func(a, b DevItem) int {
		rank := func(d DevItem) int {
			switch {
			case d.Kind == "pr" && d.Status == "OPEN":
				return 0
			case d.Kind == "pr":
				return 1
			case d.Kind == "build":
				return 2
			case d.Kind == "deploy":
				return 3
			case d.Kind == "branch":
				return 4
			}
			return 5
		}
		return rank(a) - rank(b)
	})
	return out, nil
}

// devDetail reads one tool's items of one data type.
func (c *Client) devDetail(ctx context.Context, issueID, tool, dataType string) ([]DevItem, error) {
	var resp struct {
		Detail []struct {
			PullRequests []struct {
				Name   string `json:"name"`
				URL    string `json:"url"`
				Status string `json:"status"`
				Source struct {
					Branch string `json:"branch"`
				} `json:"source"`
				Destination struct {
					Branch string `json:"branch"`
				} `json:"destination"`
				RepositoryName string    `json:"repositoryName"`
				RepositoryURL  string    `json:"repositoryUrl"`
				Author         devPerson `json:"author"`
				Reviewers      []struct {
					devPerson
					Approved bool `json:"approved"`
				} `json:"reviewers"`
				CommentCount int     `json:"commentCount"`
				LastUpdate   devTime `json:"lastUpdate"`
			} `json:"pullRequests"`
			Branches []struct {
				Name       string `json:"name"`
				URL        string `json:"url"`
				CreatePR   string `json:"createPullRequestUrl"`
				Repository struct {
					Name string `json:"name"`
					URL  string `json:"url"`
				} `json:"repository"`
				LastCommit *devCommit `json:"lastCommit"`
			} `json:"branches"`
			Repositories []struct {
				Name    string      `json:"name"`
				URL     string      `json:"url"`
				Commits []devCommit `json:"commits"`
			} `json:"repositories"`
			Builds []struct {
				Name        string  `json:"name"`
				DisplayName string  `json:"displayName"`
				BuildNumber any     `json:"buildNumber"`
				State       string  `json:"state"`
				URL         string  `json:"url"`
				LastUpdated devTime `json:"lastUpdated"`
				TestSummary *struct {
					Total   int `json:"totalNumber"`
					Passed  int `json:"numberPassed"`
					Failed  int `json:"numberFailed"`
					Skipped int `json:"numberSkipped"`
				} `json:"testSummary"`
				References []struct {
					Ref struct {
						Name string `json:"name"`
					} `json:"ref"`
				} `json:"references"`
			} `json:"builds"`
			Deployments []struct {
				DisplayName string  `json:"displayName"`
				State       string  `json:"state"`
				URL         string  `json:"url"`
				LastUpdated devTime `json:"lastUpdated"`
				Duration    int     `json:"duration"`
				Environment struct {
					DisplayName string `json:"displayName"`
					Type        string `json:"type"`
				} `json:"environment"`
				Pipeline struct {
					DisplayName string `json:"displayName"`
				} `json:"pipeline"`
			} `json:"deployments"`
		} `json:"detail"`
	}
	q := url.Values{"issueId": {issueID}, "applicationType": {tool}, "dataType": {dataType}}
	if err := c.do(ctx, http.MethodGet, "/rest/dev-status/latest/issue/detail?"+q.Encode(), "development info", nil, &resp); err != nil {
		return nil, err
	}
	var out []DevItem
	for _, d := range resp.Detail {
		for _, p := range d.PullRequests {
			it := DevItem{Kind: "pr", Name: p.Name, Status: p.Status, Repo: p.RepositoryName, RepoURL: p.RepositoryURL,
				Branch: p.Source.Branch + " → " + p.Destination.Branch, Source: p.Source.Branch, Target: p.Destination.Branch,
				URL: p.URL, Tool: tool, Author: p.Author.Name, AuthorAvatar: p.Author.Avatar, Comments: p.CommentCount, Updated: p.LastUpdate.Time}
			for _, r := range p.Reviewers {
				it.Reviewers = append(it.Reviewers, DevReviewer{Name: r.Name, Avatar: r.Avatar, Approved: r.Approved})
			}
			out = append(out, it)
		}
		for _, b := range d.Branches {
			it := DevItem{Kind: "branch", Name: b.Name, Repo: b.Repository.Name, RepoURL: b.Repository.URL, URL: b.URL, Tool: tool, CreatePR: b.CreatePR}
			if lc := b.LastCommit; lc != nil {
				it.Hash, it.ShortHash, it.Message = cmp.Or(lc.ID, lc.DisplayID), lc.DisplayID, firstLine(lc.Message)
				it.Author, it.AuthorAvatar, it.Updated = lc.Author.Name, lc.Author.Avatar, lc.AuthorTimestamp.Time
			}
			out = append(out, it)
		}
		for _, r := range d.Repositories {
			for _, cm := range r.Commits {
				msg := firstLine(cm.Message)
				out = append(out, DevItem{Kind: "commit", Name: cm.DisplayID + " " + msg, Status: cm.Author.Name, Repo: r.Name, RepoURL: r.URL, URL: cm.URL, Tool: tool,
					Hash: cmp.Or(cm.ID, cm.DisplayID), ShortHash: cm.DisplayID, Message: msg, Author: cm.Author.Name, AuthorAvatar: cm.Author.Avatar, Updated: cm.AuthorTimestamp.Time})
			}
		}
		for _, b := range d.Builds {
			name := cmp.Or(b.DisplayName, b.Name)
			if b.BuildNumber != nil {
				name += fmt.Sprintf(" #%v", b.BuildNumber)
			}
			var ref string
			if len(b.References) > 0 {
				ref = b.References[0].Ref.Name
			}
			it := DevItem{Kind: "build", Name: name, Status: strings.ToUpper(b.State), Branch: ref, URL: b.URL, Tool: tool, Updated: b.LastUpdated.Time}
			if ts := b.TestSummary; ts != nil {
				it.Tests = &DevTests{Total: ts.Total, Passed: ts.Passed, Failed: ts.Failed, Skipped: ts.Skipped}
			}
			out = append(out, it)
		}
		for _, dp := range d.Deployments {
			out = append(out, DevItem{Kind: "deploy", Name: cmp.Or(dp.Pipeline.DisplayName, dp.DisplayName), Status: strings.ToUpper(dp.State),
				Branch: cmp.Or(dp.Environment.DisplayName, dp.Environment.Type), URL: dp.URL, Tool: tool,
				Updated: dp.LastUpdated.Time, Duration: dp.Duration, EnvType: dp.Environment.Type})
		}
	}
	return out, nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return strings.TrimSpace(line)
}
