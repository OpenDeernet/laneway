package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// offerMove asks to move the app to Applications when macOS runs it from a
// temporary copy (opened from Downloads, say): that copy's path changes each
// launch, and starting at login points at the laneway inside it.
func (d *desktop) offerMove() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	bundle := filepath.Dir(filepath.Dir(filepath.Dir(exe))) // Laneway.app/Contents/MacOS/laneway-desktop
	orig := translocatedFrom(bundle)
	if orig == "" {
		return
	}
	log.Printf("running from a temporary copy of %s", orig)
	dlg := d.app.Dialog.Question().SetTitle("Move Laneway to Applications?").
		SetMessage("Until it is there, macOS runs a temporary copy of it, and starting at login would not find it.")
	move := dlg.AddButton("Move to Applications")
	move.OnClick(func() { go d.moveToApplications(orig) })
	later := dlg.AddButton("Not Now")
	dlg.SetDefaultButton(move).SetCancelButton(later).Show()
}

// moveToApplications moves the app at orig to /Applications and starts it
// from there.
func (d *desktop) moveToApplications(orig string) {
	dst := filepath.Join("/Applications", filepath.Base(orig))
	if err := moveApp(orig, dst); err != nil {
		log.Print("move: ", err)
		d.app.Dialog.Error().SetTitle("Laneway was not moved").
			SetMessage(err.Error() + "\n\nDrag Laneway to Applications in the Finder instead.").Show()
		return
	}
	log.Printf("moved to %s", dst)
	// Quit first: started while this one runs, it would only bring this one forward.
	if err := exec.Command("/bin/sh", "-c", `sleep 2; open "$0"`, dst).Start(); err != nil {
		log.Print("move: ", err)
	}
	d.app.Quit()
}

// moveApp puts the bundle at orig at dst, an older one there in the Trash,
// and clears the quarantine that makes macOS run a temporary copy.
func moveApp(orig, dst string) error {
	// orig is dst when it was moved there without the Finder: only the quarantine to clear.
	if orig != dst {
		if _, err := os.Stat(dst); err == nil {
			if err := trash(dst); err != nil {
				return fmt.Errorf("the Laneway in Applications did not go to the Trash: %w", err)
			}
		}
		if err := os.Rename(orig, dst); err != nil {
			// Another volume (a disk image, say): copy it, the original stays.
			if out, err := exec.Command("ditto", orig, dst).CombinedOutput(); err != nil {
				return fmt.Errorf("copying to Applications: %s", strings.TrimSpace(string(out)))
			}
		}
	}
	if out, err := exec.Command("xattr", "-dr", "com.apple.quarantine", dst).CombinedOutput(); err != nil {
		return fmt.Errorf("clearing quarantine: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// trash moves path to the user's Trash, under a name no other item there has.
func trash(path string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	ext := filepath.Ext(path)
	name := strings.TrimSuffix(filepath.Base(path), ext) + " " + time.Now().Format("15.04.05") + ext
	return os.Rename(path, filepath.Join(home, ".Trash", name))
}
