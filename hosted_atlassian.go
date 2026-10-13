package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cornedor/laneway/internal/config"
	"github.com/cornedor/laneway/internal/i18n"
	"github.com/cornedor/laneway/internal/jira"
)

// laneway web -hosted -atlassian: people sign in to one Jira Cloud site
// through Atlassian's OAuth 2.0 (3LO), this server being an app in
// Atlassian's developer console. Atlassian's tokens are bearer tokens,
// bound to nothing, so each person's is sealed (config.SealedStore) with
// LANEWAY_WEB_SEAL_KEY, which only the environment holds.

type hostedAtlassian struct {
	site   string // https://x.atlassian.net
	wiki   string // Confluence's site when it is another, else ""
	scopes string
	seal   string
}

// minSealKey is the shortest LANEWAY_WEB_SEAL_KEY taken.
const minSealKey = 32

func newHostedAtlassian(site, wiki, public, data, scopes, seal string, client jira.OAuthClient) (*hosted, error) {
	if !atlassianSite(site) {
		return nil, fmt.Errorf(i18n.T("-atlassian %q is not a site like https://acme.atlassian.net"), site)
	}
	if wiki != "" && !atlassianSite(wiki) {
		return nil, fmt.Errorf(i18n.T("-confluence %q is not a site like https://acme.atlassian.net"), wiki)
	}
	if jira.SiteRoot(wiki) == jira.SiteRoot(site) {
		wiki = ""
	}
	if len(seal) < minSealKey {
		return nil, fmt.Errorf(i18n.T("LANEWAY_WEB_SEAL_KEY must be set, at least %d characters: it encrypts each person's sign-in"), minSealKey)
	}
	if !strings.Contains(" "+scopes+" ", " offline_access ") {
		return nil, errors.New(i18n.T("-scopes needs offline_access: without it a sign-in ends within the hour"))
	}
	h, err := newHosted(jira.SiteRoot(site), public, data, client)
	if err != nil {
		return nil, errors.New(i18n.T("-hosted -atlassian needs -url, -data and -client-id, and LANEWAY_WEB_CLIENT_SECRET in the environment"))
	}
	h.atlassian = &hostedAtlassian{site: jira.SiteRoot(site), wiki: jira.SiteRoot(wiki), scopes: scopes, seal: seal}
	return h, nil
}

// atlassianSite reports whether site is a site's root over https.
func atlassianSite(site string) bool {
	u, err := url.Parse(strings.TrimSpace(site))
	return err == nil && u.Scheme == "https" && u.Host != "" && strings.Trim(u.Path, "/") == "" && u.RawQuery == "" && u.Fragment == "" && u.User == nil
}

// store is where the person's sign-in is kept, sealed for them.
func (h *hosted) atlassianStore(account string) (*config.SealedStore, error) {
	return config.NewSealedStore(filepath.Join(h.accountDir(account), "sign-in.json"), h.atlassian.seal, account)
}

// signIn sets the person's sign-in on cfg: the store, the sites and
// their cloud ids.
func (a *hostedAtlassian) signIn(h *hosted, account string, cfg *jira.Config) error {
	st, err := h.atlassianStore(account)
	if err != nil {
		return err
	}
	raw, err := st.Load()
	if err != nil {
		return err
	}
	var c jira.AtlassianCredentials
	if len(raw) == 0 || json.Unmarshal(raw, &c) != nil || c.CloudID == "" {
		return jira.ErrSignedOut
	}
	cfg.Atlassian, cfg.CloudID = st, c.CloudID
	if a.wiki != "" {
		// Without its grant the wiki is asked for on the Jira site, which
		// refuses: Confluence says so, Jira works.
		cfg.WikiURL, cfg.WikiCloudID = a.wiki, c.WikiCloudID
	}
	return nil
}

// atlassianCallback finishes a sign-in: the code swapped for tokens, the
// site checked, the tokens sealed in the person's directory.
func (h *hosted) atlassianCallback(w http.ResponseWriter, r *http.Request, p *hostedLogin) {
	creds, err := p.atl.Finish(r.Context(), r.URL.Query())
	switch {
	case errors.Is(err, jira.ErrNoSiteAccess):
		http.Error(w, i18n.Tf("your Atlassian sign-in doesn't include %s; sign in again and pick it", h.atlassian.site), http.StatusForbidden)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	case !accountID.MatchString(creds.Account):
		http.Error(w, i18n.T("Atlassian didn't say who signed in"), http.StatusBadGateway)
		return
	}
	if err := os.MkdirAll(h.accountDir(creds.Account), 0o700); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// A sign-in again: the running app stopped first, so it can't keep its
	// tokens over the new ones.
	h.stopApp(creds.Account)
	st, err := h.atlassianStore(creds.Account)
	if err == nil {
		raw, _ := json.Marshal(creds)
		err = st.Save(raw)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.startSession(w, creds.Account)
	if h.atlassian.wiki != "" && creds.WikiCloudID == "" && !p.again {
		// Atlassian's consent takes in one site at a time: once more for
		// Confluence's. Its grant adds to the first.
		h.beginLogin(w, r, p.next, true)
		return
	}
	http.Redirect(w, r, p.next, http.StatusSeeOther)
}

// atlassianSignOut forgets the person's sign-in here. Atlassian revokes
// an app's grant only from the person's account (Connected apps).
func (h *hosted) atlassianSignOut(account string) {
	if st, err := h.atlassianStore(account); err == nil {
		st.Save(nil)
	}
}

// Atlassian's personal data reporting: every cycle (a week unless it says
// otherwise) the accounts kept here are reported; a closed one's
// directory and sessions are erased, an updated one's app stops, so what
// it held of them is fetched again.

const (
	reportFile    = "report.json"
	reportCycle   = 7 * 24 * time.Hour
	reportCheck   = time.Hour
	reportRetryIn = 6 * time.Hour
)

// errNobodyYet is a report with no one to report.
var errNobodyYet = errors.New("no one signed in yet")

type reportState struct {
	Next time.Time `json:"next"`
}

// reportLoop reports when due, checking every reportCheck.
func (h *hosted) reportLoop(ctx context.Context) {
	t := time.NewTicker(reportCheck)
	defer t.Stop()
	for {
		h.reportIfDue(ctx, time.Now())
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (h *hosted) reportIfDue(ctx context.Context, now time.Time) {
	p := filepath.Join(h.data, reportFile)
	var st reportState
	if raw, err := os.ReadFile(p); err == nil {
		json.Unmarshal(raw, &st)
	}
	if now.Before(st.Next) {
		return
	}
	cycle, err := h.report(ctx)
	switch {
	case errors.Is(err, errNobodyYet):
		return // again at the next check
	case err != nil:
		log.Printf("laneway: personal data report: %v", err)
		st.Next = now.Add(reportRetryIn)
	case cycle > 0:
		st.Next = now.Add(cycle)
	default:
		st.Next = now.Add(reportCycle)
	}
	raw, _ := json.Marshal(st)
	if os.WriteFile(p+".tmp", raw, 0o600) == nil {
		os.Rename(p+".tmp", p)
	}
}

// report reports every account kept here, with the sign-in of the first
// that works, and acts on the answer.
func (h *hosted) report(ctx context.Context) (time.Duration, error) {
	entries, err := os.ReadDir(filepath.Join(h.data, "people"))
	if err != nil {
		return 0, err
	}
	var accounts []jira.AccountReport
	kept := map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() || !accountID.MatchString(e.Name()) {
			continue
		}
		at := time.Now()
		if fi, err := os.Stat(filepath.Join(h.accountDir(e.Name()), "sign-in.json")); err == nil {
			at = fi.ModTime()
		}
		accounts = append(accounts, jira.AccountReport{AccountID: e.Name(), UpdatedAt: at.UTC()})
		kept[e.Name()] = true
	}
	if len(accounts) == 0 {
		return 0, errNobodyYet
	}
	var answer jira.ReportAnswer
	err = jira.ErrSignedOut
	for _, a := range accounts {
		var cfg jira.Config
		if h.atlassian.signIn(h, a.AccountID, &cfg) != nil {
			continue
		}
		cfg.OAuthClient = h.client
		answer, err = jira.New(cfg).ReportAccounts(ctx, accounts)
		if err == nil || errors.Is(err, jira.ErrReportLater) {
			break
		}
	}
	if err != nil {
		return 0, err
	}
	// Only what was reported: the answer names nothing else to erase.
	for _, id := range answer.Closed {
		if !kept[id] {
			continue
		}
		h.endAccount(id)
		if err := os.RemoveAll(h.accountDir(id)); err != nil {
			log.Printf("laneway: erasing closed account %s: %v", id, err)
		}
	}
	for _, id := range answer.Updated {
		if kept[id] {
			h.stopApp(id)
		}
	}
	return answer.Cycle, nil
}
