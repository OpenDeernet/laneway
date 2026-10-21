// Laneway desktop: a window around `laneway web`, with native notifications.
// The laneway binary next to this one (or on PATH) serves on loopback; this
// app only shows it. Its own module, so the rest of laneway stays CGO-free.
package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/services/dock"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

const addr = "127.0.0.1:8484"

// pageJS runs in every page: see page.js.
//
//go:embed page.js
var pageJS string

//go:embed splash.html
var splashHTML string

// desktop is what the page's messages act on.
type desktop struct {
	app  *application.App
	win  *application.WebviewWindow
	ns   *notifications.NotificationService
	dock *dock.DockService

	updates  bool         // the updater is set up: see initUpdates
	checking atomic.Bool  // a check runs
	declined atomic.Value // the version Not Now was given for
}

func main() {
	if f, err := appendLog("desktop-app.log"); err == nil {
		log.SetOutput(f) // started from the Finder, stderr goes nowhere
	}
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	exe, _ := os.Executable()
	log.Printf("--- laneway desktop %s, pid %d, %s", version, os.Getpid(), exe)
	// A start that hangs leaves where it hangs in the log.
	watchdog := time.AfterFunc(30*time.Second, func() {
		buf := make([]byte, 1<<20)
		log.Printf("laneway web not shown after 30s:\n%s", buf[:runtime.Stack(buf, true)])
	})

	d := &desktop{ns: notifications.New(), dock: dock.New()}
	origin := "http://" + addr
	mac := runtime.GOOS == "darwin"

	d.app = application.New(application.Options{
		Name:     "Laneway",
		Services: []application.Service{application.NewService(d.ns), application.NewService(d.dock)},
		Mac:      application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: false},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID:               "com.github.cornedor.laneway",
			OnSecondInstanceLaunch: func(application.SecondInstanceData) { d.show() },
		},
		RawMessageHandler: func(_ application.Window, msg string, o *application.OriginInfo) {
			// Only macOS tells frames apart; elsewhere Origin is the window's URL.
			if o == nil || mac && !o.IsMainFrame || !sameOrigin(o.Origin, origin) {
				log.Printf("message dropped: %+v", o)
				return // only the laneway page may post
			}
			d.handle(msg)
		},
	})

	log.Print("app made")

	p := loadPlace()
	opts := application.WebviewWindowOptions{
		Title:  "Laneway",
		Width:  p.W,
		Height: p.H,
		Hidden: true, // until it is where it was
		HTML:   splashHTML,
		JS:     pageJS,
		Mac: application.MacWindow{
			// The page's header is the title bar: the window buttons sit left of the logo.
			TitleBar: application.MacTitleBar{
				AppearsTransparent:   true,
				HideTitle:            true,
				FullSizeContent:      true,
				UseToolbar:           true,
				HideToolbarSeparator: true,
				ToolbarStyle:         application.MacToolbarStyleUnified,
			},
		},
	}
	if p.Max {
		opts.StartState = application.WindowStateMaximised
	}
	d.win = d.app.Window.NewWithOptions(opts)
	remember(d.win, p)
	d.win.OnWindowEvent(events.Common.WindowFullscreen, func(*application.WindowEvent) { d.js("fullscreen(true)") })
	d.win.OnWindowEvent(events.Common.WindowUnFullscreen, func(*application.WindowEvent) { d.js("fullscreen(false)") })

	if mac { // a Mac app stays running with its window closed: the dock brings it back
		d.win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
			d.win.Hide()
			e.Cancel()
		})
		d.app.Event.OnApplicationEvent(events.Mac.ApplicationShouldHandleReopen, func(*application.ApplicationEvent) {
			d.win.Show()
		})
		d.app.Menu.Set(d.menu())
	}

	d.ns.OnNotificationResponse(func(r notifications.NotificationResult) {
		if r.Error != nil {
			return
		}
		d.show()
		if id, ok := r.Response.UserInfo["id"].(string); ok {
			d.js(fmt.Sprintf("click(%q)", id))
		}
	})

	log.Print("window made")
	srv := &server{addr: addr}
	d.app.OnShutdown(srv.stop)
	d.app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		log.Print("started")
		restore(d.app, d.win, p)
		d.win.Show()
		if mac {
			d.offerMove()
		}
		d.initUpdates()
		go func() {
			err := srv.start()
			watchdog.Stop()
			if err != nil {
				log.Print(err)
				d.app.Dialog.Error().SetTitle("Laneway did not start").SetMessage(err.Error()).Show()
				d.app.Quit()
				return
			}
			d.win.SetURL(origin)
		}()
	})

	if err := d.app.Run(); err != nil {
		log.Fatal(err)
	}
}

// menu is the macOS menu bar: the standard one, with Settings… (⌘,) in the app menu.
func (d *desktop) menu() *application.Menu {
	m := application.NewMenu()
	app := m.AddSubmenu("Laneway")
	app.AddRole(application.About)
	app.Add("Check for Updates…").OnClick(func(*application.Context) { go d.checkUpdate(true) })
	app.AddSeparator()
	app.Add("Settings…").SetAccelerator("CmdOrCtrl+,").OnClick(func(*application.Context) {
		d.show()
		d.win.ExecJS("window.laneway?.go('/settings')")
	})
	app.AddSeparator()
	app.AddRole(application.ServicesMenu)
	app.AddSeparator()
	app.AddRole(application.Hide)
	app.AddRole(application.HideOthers)
	app.AddRole(application.UnHide)
	app.AddSeparator()
	app.AddRole(application.Quit)
	m.AddRole(application.FileMenu) // Close Window, ⌘W
	m.AddRole(application.EditMenu)
	m.AddRole(application.ViewMenu)
	m.AddRole(application.WindowMenu)
	return m
}

func (d *desktop) show() {
	d.win.Show()
	d.win.Focus()
}

// js calls window.__lanewayDesktop.call in the page.
func (d *desktop) js(call string) {
	d.win.ExecJS("window.__lanewayDesktop?." + call)
}

// sameOrigin is whether page (a URL; macOS passes the page's whole URL) is
// on origin.
func sameOrigin(page, origin string) bool {
	u, err := url.Parse(page)
	return err == nil && u.Scheme+"://"+u.Host == origin
}

// handle is a message from page.js.
func (d *desktop) handle(msg string) {
	var m struct {
		Type, ID, Title, Body, Tag, URL string
		N                               int
	}
	if json.Unmarshal([]byte(msg), &m) != nil {
		return
	}
	answer := func(p string) { d.js(fmt.Sprintf("permission(%q)", p)) }
	switch m.Type {
	case "check":
		if ok, err := d.ns.CheckNotificationAuthorization(); err == nil && ok {
			answer("granted")
		}
	case "permission":
		go func() { // waits on the user
			ok, err := d.ns.RequestNotificationAuthorization()
			if err != nil {
				log.Print("notifications: ", err)
			}
			answer(map[bool]string{true: "granted", false: "denied"}[ok && err == nil])
		}()
	case "notify":
		id := m.Tag
		if id == "" {
			id = m.ID
		}
		err := d.ns.SendNotification(notifications.NotificationOptions{ID: id, Title: m.Title, Body: m.Body, Data: map[string]any{"id": m.ID}})
		if err != nil {
			log.Print("notifications: ", err)
		}
	case "badge":
		var err error
		if label := strconv.Itoa(m.N); m.N > 99 {
			err = d.dock.SetBadge("99+")
		} else if m.N > 0 {
			err = d.dock.SetBadge(label)
		} else {
			err = d.dock.RemoveBadge()
		}
		if err != nil {
			log.Print("badge: ", err)
		}
	case "update":
		go d.checkUpdate(true)
	case "open":
		if u, err := url.Parse(m.URL); err == nil && (u.Scheme == "http" || u.Scheme == "https" || u.Scheme == "mailto") {
			if err := d.app.Browser.OpenURL(u.String()); err != nil {
				log.Print("open: ", err)
			}
		}
	}
}
