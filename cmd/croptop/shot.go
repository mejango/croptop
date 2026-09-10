package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// shot captures part of the screen and hands it to the running console,
// which opens a small form to turn it into a post.
func (a *app) shot(install bool, key string) error {
	if install {
		return installShotShortcut(key)
	}
	tmp := filepath.Join(os.TempDir(), "croptop-shot.png")
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("screencapture", "-i", "-x", tmp) // interactive selection, no sound
	case "linux":
		if _, err := exec.LookPath("grim"); err == nil {
			cmd = exec.Command("sh", "-c", `grim -g "$(slurp)" "$0"`, tmp)
		} else {
			cmd = exec.Command("gnome-screenshot", "-a", "-f", tmp)
		}
	default:
		return fmt.Errorf("croptop shot works on macOS and Linux; on Windows paste a screenshot into a new post")
	}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("screenshot: %w", err)
	}
	f, err := os.Open(tmp)
	if err != nil {
		return fmt.Errorf("no screenshot was taken")
	}
	defer f.Close()
	defer os.Remove(tmp)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, _ := mw.CreateFormFile("image", "shot.png")
	io.Copy(part, f)
	mw.Close()
	base := "http://" + strings.Replace(a.cfg.Listen, "0.0.0.0", "127.0.0.1", 1)
	resp, err := http.Post(base+"/v0/croptop/quick", mw.FormDataContentType(), &body)
	if err != nil {
		return fmt.Errorf("the console is not running at %s; start croptop first", base)
	}
	defer resp.Body.Close()
	var out struct{ Open string }
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.Open == "" {
		return fmt.Errorf("console refused the screenshot: %s", resp.Status)
	}
	openBrowser(base + out.Open)
	return nil
}

// installShotShortcut registers a macOS Quick Action that runs `croptop shot`
// and binds it to a key. The default, Command Control Shift C, is one no
// common app claims: an app's own menu shortcuts always win over a Service's
// (Command Shift C is "Show Colors" in the terminal, TextEdit, Mail...).
func installShotShortcut(key string) error {
	if key == "" {
		key = "cmd+ctrl+shift+c"
	}
	keyEq, human, err := parseShortcut(key)
	if err != nil {
		return err
	}
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("the shortcut installer is for macOS; on Linux bind `croptop shot` to a key in your desktop's keyboard settings")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, "Library", "Services", "Croptop Shot.workflow", "Contents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	info := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>NSServices</key><array><dict>
    <key>NSMenuItem</key><dict><key>default</key><string>Croptop Shot</string></dict>
    <key>NSMessage</key><string>runWorkflowAsService</string>
    <key>NSRequiredContext</key><dict><key>NSApplicationIdentifier</key><string>*</string></dict>
    <key>NSKeyEquivalent</key><dict><key>default</key><string>%s</string></dict>
  </dict></array>
</dict></plist>
`
	info = fmt.Sprintf(info, keyEq[len(keyEq)-1:])
	wflow := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>AMApplicationBuild</key><string>528</string>
  <key>AMApplicationVersion</key><string>2.10</string>
  <key>AMDocumentVersion</key><string>2</string>
  <key>actions</key><array><dict>
    <key>action</key><dict>
      <key>AMAccepts</key><dict><key>Container</key><string>List</string><key>Optional</key><true/><key>Types</key><array><string>com.apple.cocoa.string</string></array></dict>
      <key>AMActionVersion</key><string>2.0.3</string>
      <key>AMApplication</key><array><string>Automator</string></array>
      <key>AMParameterProperties</key><dict><key>COMMAND_STRING</key><dict/><key>CheckedForUserDefaultShell</key><dict/><key>inputMethod</key><dict/><key>shell</key><dict/><key>source</key><dict/></dict>
      <key>AMProvides</key><dict><key>Container</key><string>List</string><key>Types</key><array><string>com.apple.cocoa.string</string></array></dict>
      <key>ActionBundlePath</key><string>/System/Library/Automator/Run Shell Script.action</string>
      <key>ActionName</key><string>Run Shell Script</string>
      <key>ActionParameters</key><dict>
        <key>COMMAND_STRING</key><string>%s shot &gt;/dev/null 2&gt;&amp;1 &amp;</string>
        <key>CheckedForUserDefaultShell</key><true/>
        <key>inputMethod</key><integer>1</integer>
        <key>shell</key><string>/bin/sh</string>
        <key>source</key><string></string>
      </dict>
      <key>BundleIdentifier</key><string>com.apple.RunShellScript</string>
      <key>CFBundleVersion</key><string>2.0.3</string>
      <key>CanShowSelectedItemsWhenRun</key><false/>
      <key>CanShowWhenRun</key><true/>
      <key>Category</key><array><string>AMCategoryUtilities</string></array>
      <key>Class Name</key><string>RunShellScriptAction</string>
      <key>InputUUID</key><string>7A1B0F2E-0000-4000-8000-000000000001</string>
      <key>Keywords</key><array><string>Shell</string></array>
      <key>OutputUUID</key><string>7A1B0F2E-0000-4000-8000-000000000002</string>
      <key>UUID</key><string>7A1B0F2E-0000-4000-8000-000000000003</string>
      <key>UnlocalizedApplications</key><array><string>Automator</string></array>
      <key>arguments</key><dict><key>0</key><dict><key>default value</key><integer>0</integer><key>name</key><string>inputMethod</string><key>required</key><string>0</string><key>type</key><string>0</string><key>uuid</key><string>0</string></dict></dict>
      <key>isViewVisible</key><true/>
      <key>location</key><string>309.000000:253.000000</string>
      <key>nibPath</key><string>/System/Library/Automator/Run Shell Script.action/Contents/Resources/Base.lproj/main.nib</string>
    </dict>
    <key>isViewVisible</key><true/>
  </dict></array>
  <key>connectors</key><dict/>
  <key>workflowMetaData</key><dict>
    <key>serviceInputTypeIdentifier</key><string>com.apple.Automator.nothing</string>
    <key>serviceOutputTypeIdentifier</key><string>com.apple.Automator.nothing</string>
    <key>serviceProcessesInput</key><integer>0</integer>
    <key>workflowTypeIdentifier</key><string>com.apple.Automator.servicesMenu</string>
  </dict>
</dict></plist>
`, exe)
	if err := os.WriteFile(filepath.Join(dir, "Info.plist"), []byte(info), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "document.wflow"), []byte(wflow), 0o644); err != nil {
		return err
	}
	// register the key the way System Settings stores it
	// The key must be passed with its own double quotes: defaults parses the value as old-style plist text.
	if out, err := exec.Command("defaults", "write", "pbs", "NSServicesStatus", "-dict-add", `"(null) - Croptop Shot - runWorkflowAsService"`,
		`{ "key_equivalent" = "`+keyEq+`"; "enabled_services_menu" = 1; "presentation_modes" = { ContextMenu = 0; ServicesMenu = 1; }; }`).CombinedOutput(); err != nil {
		return fmt.Errorf("registering the key: %s", strings.TrimSpace(string(out)))
	}
	exec.Command("/System/Library/CoreServices/pbs", "-update").Run()
	fmt.Printf("installed the Croptop Shot quick action. Press %s anywhere to grab part of the screen and post it.\n", human)
	fmt.Println("If the key does nothing, open System Settings, Keyboard, Keyboard Shortcuts, Services, General, and tick Croptop Shot.")
	return nil
}

// parseShortcut turns "cmd+ctrl+shift+c" into the key_equivalent string
// System Settings stores ("@^$C") and a readable name.
func parseShortcut(spec string) (string, string, error) {
	mods := map[string][2]string{"cmd": {"@", "Command"}, "command": {"@", "Command"}, "ctrl": {"^", "Control"}, "control": {"^", "Control"}, "shift": {"$", "Shift"}, "opt": {"~", "Option"}, "option": {"~", "Option"}, "alt": {"~", "Option"}}
	order := "@^~$"
	parts := strings.Split(strings.ToLower(spec), "+")
	have := map[string]bool{}
	var names []string
	letter := ""
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if m, ok := mods[p]; ok {
			if !have[m[0]] {
				have[m[0]] = true
				names = append(names, m[1])
			}
			continue
		}
		if len(p) != 1 || letter != "" {
			return "", "", fmt.Errorf("shortcut must be modifiers plus one key, like cmd+shift+c")
		}
		letter = strings.ToUpper(p)
	}
	if letter == "" || !have["@"] && !have["^"] {
		return "", "", fmt.Errorf("shortcut needs cmd or ctrl plus one key, like cmd+ctrl+shift+c")
	}
	eq := ""
	for _, c := range order {
		if have[string(c)] {
			eq += string(c)
		}
	}
	return eq + letter, strings.Join(append(names, letter), " "), nil
}
