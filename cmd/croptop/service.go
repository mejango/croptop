package main

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/mejango/croptop/internal/config"
)

const serviceLabel = "top.crop.croptop"

// service keeps the console (and with it the IPFS node) running from login on,
// through launchd on macOS and a systemd user unit on Linux.
func (a *app) service(sub string) error {
	base := "http://" + strings.Replace(a.cfg.Listen, "0.0.0.0", "127.0.0.1", 1)
	switch sub {
	case "install":
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		if exe, err = filepath.EvalSymlinks(exe); err != nil {
			return err
		}
		args := []string{exe, "serve", "--no-open"}
		if def, _ := config.DefaultDir(); a.dataDir != def {
			args = append(args, "--data", a.dataDir)
		}
		// Already installed with this binary and answering: nothing to do (the app calls this on every launch).
		if same, _ := serviceMatches(args); same && up(base) {
			fmt.Printf("croptop already runs as a service at %s\n", base)
			return nil
		}
		// A console started by hand holds the port and the datastore: ask it to leave first.
		if up(base) {
			fmt.Println("asking the running console to quit so the service can take over")
			http.Post(base+"/v0/croptop/quit", "application/json", nil)
			for i := 0; i < 30 && up(base); i++ {
				time.Sleep(500 * time.Millisecond)
			}
		}
		switch runtime.GOOS {
		case "darwin":
			if err := installLaunchd(args, a.dataDir); err != nil {
				return err
			}
		case "linux":
			if err := installSystemd(args); err != nil {
				return err
			}
		default:
			return fmt.Errorf("croptop service is for macOS and Linux; on Windows add croptop to Startup apps")
		}
		for i := 0; i < 60 && !up(base); i++ {
			time.Sleep(500 * time.Millisecond)
		}
		if !up(base) {
			return fmt.Errorf("the service was installed but the console is not answering at %s yet; check %s", base, filepath.Join(a.dataDir, "service.log"))
		}
		fmt.Printf("croptop runs at login now and is up at %s\n", base)
		return nil
	case "uninstall":
		switch runtime.GOOS {
		case "darwin":
			plist := launchdPlist()
			exec.Command("launchctl", "bootout", "gui/"+uid()+"/"+serviceLabel).Run()
			os.Remove(plist)
		case "linux":
			exec.Command("systemctl", "--user", "disable", "--now", "croptop").Run()
			os.Remove(systemdUnit())
		}
		fmt.Println("croptop no longer starts at login")
		return nil
	case "status":
		installed := false
		switch runtime.GOOS {
		case "darwin":
			_, err := os.Stat(launchdPlist())
			installed = err == nil
		case "linux":
			_, err := os.Stat(systemdUnit())
			installed = err == nil
		}
		fmt.Printf("service installed: %v\nconsole up at %s: %v\n", installed, base, up(base))
		return nil
	}
	return fmt.Errorf("usage: croptop service install|uninstall|status")
}

func up(base string) bool {
	c := &http.Client{Timeout: 2 * time.Second}
	r, err := c.Get(base + "/v0/ping")
	if err != nil {
		return false
	}
	r.Body.Close()
	return r.StatusCode == 200
}

func uid() string { return fmt.Sprint(os.Getuid()) }

func launchdPlist() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", serviceLabel+".plist")
}

func installLaunchd(args []string, dataDir string) error {
	plist := launchdPlist()
	if err := os.MkdirAll(filepath.Dir(plist), 0o755); err != nil {
		return err
	}
	var sb strings.Builder
	for _, a := range args {
		sb.WriteString("    <string>" + a + "</string>\n")
	}
	log := filepath.Join(dataDir, "service.log")
	body := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>` + serviceLabel + `</string>
  <key>ProgramArguments</key><array>
` + sb.String() + `  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ProcessType</key><string>Background</string>
  <key>StandardOutPath</key><string>` + log + `</string>
  <key>StandardErrorPath</key><string>` + log + `</string>
</dict></plist>
`
	if err := os.WriteFile(plist, []byte(body), 0o644); err != nil {
		return err
	}
	exec.Command("launchctl", "bootout", "gui/"+uid()+"/"+serviceLabel).Run() // replace an older one quietly
	if out, err := exec.Command("launchctl", "bootstrap", "gui/"+uid(), plist).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl bootstrap: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func systemdUnit() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "systemd", "user", "croptop.service")
}

func installSystemd(args []string) error {
	unit := systemdUnit()
	if err := os.MkdirAll(filepath.Dir(unit), 0o755); err != nil {
		return err
	}
	body := "[Unit]\nDescription=Croptop console and IPFS node\n\n[Service]\nExecStart=" + strings.Join(args, " ") + "\nRestart=always\nRestartSec=3\n\n[Install]\nWantedBy=default.target\n"
	if err := os.WriteFile(unit, []byte(body), 0o644); err != nil {
		return err
	}
	if out, err := exec.Command("sh", "-c", "systemctl --user daemon-reload && systemctl --user enable --now croptop").CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// serviceMatches reports whether the installed service definition already names these arguments.
func serviceMatches(args []string) (bool, error) {
	var file string
	switch runtime.GOOS {
	case "darwin":
		file = launchdPlist()
	case "linux":
		file = systemdUnit()
	default:
		return false, nil
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return false, err
	}
	for _, a := range args {
		if !strings.Contains(string(b), a) {
			return false, nil
		}
	}
	return true, nil
}
