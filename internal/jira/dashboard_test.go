package jira

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDashboards(t *testing.T) {
	lanewaySite := false
	var searched []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/rest/api/3/serverInfo":
			if lanewaySite {
				fmt.Fprint(w, `{"lanewayServerVersion":"0.1"}`)
				return
			}
			fmt.Fprint(w, `{"deploymentType":"Cloud"}`)
		case r.URL.Path == "/rest/api/3/dashboard/search":
			if r.URL.Query().Get("startAt") == "0" {
				fmt.Fprint(w, `{"isLast":false,"values":[{"id":"1","name":"Alpha","owner":{"displayName":"Ada"}}]}`)
				return
			}
			fmt.Fprint(w, `{"isLast":true,"values":[{"id":"2","name":"Beta","isFavourite":true,"view":"https://x/jira/dashboards/2"}]}`)
		case r.URL.Path == "/rest/api/3/dashboard/2/gadget":
			fmt.Fprint(w, `{"gadgets":[{"id":12,"moduleKey":"com.atlassian.jira.gadgets:pie-chart-gadget","position":{"row":0,"column":1}},
				{"id":11,"uri":"rest/gadgets/1.0/g/com.atlassian.jira.gadgets:filter-results-gadget/gadgets/filter-results-gadget.xml","title":"F","position":{"row":1,"column":0}},
				{"id":10,"moduleKey":"com.atlassian.jira.gadgets:assigned-to-me-gadget","position":{"row":0,"column":0}}]}`)
		case r.URL.Path == "/rest/api/3/dashboard/2/items/11/properties/config":
			fmt.Fprint(w, `{"key":"config","value":{"filterId":"filter-10042"}}`)
		case r.URL.Path == "/rest/laneway/1/dashboard/2/gadget/12/data":
			fmt.Fprint(w, `{"total":3,"buckets":[]}`)
		case strings.HasPrefix(r.URL.Path, "/rest/api/3/search"):
			var b struct{ JQL string }
			json.NewDecoder(r.Body).Decode(&b)
			searched = append(searched, b.JQL+r.URL.Query().Get("jql"))
			fmt.Fprint(w, `{"issues":[{"key":"ABC-1","fields":{"summary":"One","status":{"name":"To Do"}}}],"total":1,"isLast":true}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	c := New(Config{BaseURL: srv.URL, Email: "a@b.c", APIToken: "t"})
	ds, err := c.Dashboards(ctx)
	if err != nil || len(ds) != 2 || ds[0].Name != "Beta" || !ds[0].Favourite || ds[1].Owner != "Ada" {
		t.Fatalf("dashboards %+v %v", ds, err)
	}
	gs, err := c.Gadgets(ctx, "2")
	if err != nil || len(gs) != 3 || gs[0].ID != 10 || gs[1].ID != 11 || gs[2].ID != 12 {
		t.Fatalf("gadgets %+v %v (want by column, then row)", gs, err)
	}
	if gs[1].Kind() != "filter-results-gadget" || gs[2].Kind() != "pie-chart-gadget" {
		t.Errorf("kinds %q %q", gs[1].Kind(), gs[2].Kind())
	}
	// Jira: a filter gadget by its config, the pie chart elsewhere.
	raw, err := c.GadgetData(ctx, "2", gs[1])
	if err != nil || !strings.Contains(string(raw), `"key":"ABC-1"`) {
		t.Fatalf("filter gadget %s %v", raw, err)
	}
	if _, err := c.GadgetData(ctx, "2", gs[2]); !errors.Is(err, ErrGadgetElsewhere) {
		t.Errorf("pie on Jira: %v", err)
	}
	if len(searched) == 0 || !strings.Contains(searched[0], "filter = 10042") {
		t.Errorf("searched %q", searched)
	}
	// laneway-server: its answer as it is.
	lanewaySite = true
	c = New(Config{BaseURL: srv.URL, Email: "a@b.c", APIToken: "t"})
	if raw, err := c.GadgetData(ctx, "2", gs[2]); err != nil || string(raw) != `{"total":3,"buckets":[]}` {
		t.Errorf("laneway pie %s %v", raw, err)
	}
}

func TestGadgetJQL(t *testing.T) {
	for cfg, want := range map[string]string{
		`{"jql":"project = X"}`:     "project = X",
		`{"filterId":"10001"}`:      "filter = 10001",
		`{"filterId":"filter-7"}`:   "filter = 7",
		`{"filterId":12}`:           "filter = 12",
		`{"filterId":"project-10"}`: "",
		`{}`:                        "",
	} {
		var m map[string]any
		json.Unmarshal([]byte(cfg), &m)
		if got := gadgetJQL(m); got != want {
			t.Errorf("%s: %q, want %q", cfg, got, want)
		}
	}
}
