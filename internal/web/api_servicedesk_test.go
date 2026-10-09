package web

import (
	"testing"
	"time"

	"github.com/cornedor/laneway/internal/demo"
	"github.com/cornedor/laneway/internal/jira"
)

// The Request block's and the queues' routes: empty on Jira without a
// service desk (the plain demo); on a desk the request, its queues, and
// comments that are internal notes unless for the customer.
func TestServiceDeskRoutes(t *testing.T) {
	type reqOut struct {
		Desk    bool
		Request *jira.Request
	}
	for _, desk := range []bool{false, true} {
		d := demo.New(time.Now())
		if desk {
			d.Laneway()
		}
		base, stop, err := d.Start()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(stop)
		ts := issueServer(t, base)
		iu := ts.URL + "/api/issues/DEMO-1"
		var r reqOut
		if code := issueCall(t, "GET", iu+"/request", nil, &r); code != 200 || r.Desk != desk || (r.Request != nil) != desk {
			t.Fatalf("desk %v: request %d %+v", desk, code, r)
		}
		var qs []jira.Queue
		if code := issueCall(t, "GET", ts.URL+"/api/servicedesk/queues", nil, &qs); code != 200 || qs == nil || (len(qs) > 0) != desk {
			t.Fatalf("desk %v: queues %d %+v", desk, code, qs)
		}
		if !desk {
			if len(d.Unhandled) > 0 {
				t.Errorf("unhandled: %v", d.Unhandled)
			}
			continue
		}
		if r.Request.Type == "" || len(r.Request.SLAs) != 2 {
			t.Errorf("request %+v", r.Request)
		}
		for _, vis := range []jira.Visibility{{}, {Customer: true}} {
			if code := issueCall(t, "POST", iu+"/comments", map[string]any{"Markdown": "on it", "Visibility": vis}, nil); code != 200 {
				t.Fatalf("comment %+v: %d", vis, code)
			}
			var iss jira.Issue
			issueCall(t, "GET", iu+"?fresh=1", nil, &iss)
			last := iss.Comments[len(iss.Comments)-1]
			if last.Body != "on it" || last.Visibility.Internal == vis.Customer {
				t.Errorf("for %+v: posted %+v", vis, last)
			}
		}
	}
}
