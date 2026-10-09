package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The account menu's server links: none on Jira; on laneway-server the
// sign-in page, and the admin only for whoever administers something.
func TestAccount(t *testing.T) {
	for _, tc := range []struct {
		name           string
		laneway, admin bool
	}{{"jira", false, false}, {"member", true, false}, {"admin", true, true}} {
		t.Run(tc.name, func(t *testing.T) {
			site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/rest/api/3/serverInfo":
					if tc.laneway {
						fmt.Fprint(w, `{"lanewayServerVersion":"1"}`)
					} else {
						fmt.Fprint(w, `{"deploymentType":"Cloud"}`)
					}
				case "/rest/api/3/mypermissions":
					fmt.Fprintf(w, `{"permissions":{"ADMINISTER":{"havePermission":false},"ADMINISTER_PROJECTS":{"havePermission":%t}}}`, tc.admin)
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(site.Close)
			ts := tokensServer(t, site.URL)
			var got struct {
				Laneway               bool
				SignInPage, AdminPage string
			}
			if code := issueCall(t, "GET", ts.URL+"/api/account", nil, &got); code != 200 || got.Laneway != tc.laneway {
				t.Fatalf("%d %+v", code, got)
			}
			if want := map[bool]string{true: site.URL + "/admin#passkeys"}[tc.laneway]; got.SignInPage != want {
				t.Errorf("sign-in page %q, want %q", got.SignInPage, want)
			}
			if want := map[bool]string{true: site.URL + "/admin"}[tc.admin]; got.AdminPage != want {
				t.Errorf("admin page %q, want %q", got.AdminPage, want)
			}
		})
	}
}
