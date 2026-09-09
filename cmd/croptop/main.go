// croptop publishes Croptop sites to IPFS from Linux, macOS, and Windows.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/mejango/croptop/internal/config"
	"github.com/mejango/croptop/internal/follow"
	"github.com/mejango/croptop/internal/host"
	"github.com/mejango/croptop/internal/ipfs"
	"github.com/mejango/croptop/internal/publish"
	"github.com/mejango/croptop/internal/render"
	"github.com/mejango/croptop/internal/server"
	"github.com/mejango/croptop/internal/store"
	"github.com/mejango/croptop/internal/tpl"
	"github.com/mejango/croptop/templates"
	"github.com/mejango/croptop/web"
)

var version = "dev"

const usage = `croptop — publish Croptop sites to IPFS

  croptop serve            run the console at http://127.0.0.1:8086 (default)
  croptop import-planet    copy sites and keys from the Croptop Mac app
  croptop adopt <name> --key site.pem
                           take over a published site on this machine
  croptop sync <site>      merge what another machine published, then publish
  croptop publish <site>   render, add to IPFS, update the IPNS name
  croptop key export <site>   print the site's private key (PEM)
  croptop key import <site> <file.pem>
  croptop passcode set     required before listening on a non-loopback address
  croptop follow <name>    keep, serve, and re-provide someone else's site
  croptop unfollow <name>
  croptop following        list followed sites
  croptop status           sites, sequences, followed sites (via the running console)
  croptop template list | install <cid or name> | publish <dir>
  croptop engine           print the active ipfs engine (kubo or embedded)
  croptop version

Flags for serve: --listen <addr>, --role node (headless: no browser, log only)
  croptop host --domain crop.top --listen 127.0.0.1:8090 [--root croptop.eth] [--announce /dns4/…/tcp/…]
                           run a gateway and pin host for a domain (see docs/host.md)

Common flags: --data <dir> (default: ` + "%s" + `), --templates <dir>
`

type app struct {
	pubWait      bool // command-line publish waits for the push and warm-up
	dataDir      string
	templatesDir string
	cfg          *config.Config
	store        *store.Store
	engine       ipfs.Engine
	kubo         *ipfs.Node // set when engine is kubo
	follow       *follow.Store
	tpl          *tpl.Resolver
	pub          *publish.Publisher
	tmpl         fs.FS
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	defaultData, _ := config.DefaultDir()
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprintf(os.Stderr, usage, defaultData); fs.PrintDefaults() }
	a := &app{}
	fs.StringVar(&a.dataDir, "data", defaultData, "data directory")
	fs.StringVar(&a.templatesDir, "templates", "", "use a template directory instead of the embedded one")
	listen := fs.String("listen", "", "address to listen on (default from config, 127.0.0.1:8086)")
	noOpen := fs.Bool("no-open", false, "do not open the browser")
	force := fs.Bool("force", false, "override safety checks")
	keyFile := fs.String("key", "", "PEM key file (adopt)")
	container := fs.String("container", "", "Planet container path (import-planet)")
	engineFlag := fs.String("engine", "", "ipfs engine: kubo (downloaded sidecar) or embedded (built in); remembered in config")
	role := fs.String("role", "console", "console (opens the browser) or node (headless)")
	domain := fs.String("domain", "crop.top", "domain this host serves (host)")
	root := fs.String("root", "", "site the bare domain serves: an ENS name, IPNS name, or CID (host)")
	announce := fs.String("announce", os.Getenv("CROPTOP_ANNOUNCE"), "public multiaddrs to advertise, comma separated, for a node behind a proxy (host)")
	trust := fs.String("trust", os.Getenv("CROPTOP_TRUST"), "domains whose forwarded pushes are accepted, comma separated (host)")
	if err := fs.Parse(flagsFirst(args)); err != nil {
		return nil
	}
	rest := fs.Args()

	switch cmd {
	case "version":
		fmt.Println("croptop", version, "kubo", ipfs.KuboVersion)
		return nil
	case "help", "-h", "--help":
		fs.Usage()
		return nil
	}
	if err := a.open(*engineFlag); err != nil {
		return err
	}

	switch cmd {
	case "engine":
		fmt.Println(a.cfg.EngineName())
		return nil
	case "template":
		if len(rest) > 0 && rest[0] == "list" {
			list, err := a.tpl.Installed()
			if err != nil {
				return err
			}
			for _, t := range list {
				id := t.CID
				if t.Default {
					id = "(built in)"
				}
				fmt.Printf("%-12s %-8s build %-5d %s  used by %d site(s)\n", t.Name, t.Version, t.Build, id, len(t.Sites))
			}
			return nil
		}
	case "serve":
		return a.serve(*listen, *noOpen || *role == "node")
	case "host":
		return a.host(*domain, *listen, *root, *announce, *trust)
	case "status":
		if *listen != "" {
			a.cfg.Listen = *listen
		}
		return a.status()
	case "import-planet":
		c := *container
		if c == "" {
			c = publish.DefaultPlanetContainer()
		}
		if c == "" {
			return fmt.Errorf("--container is required on %s", runtime.GOOS)
		}
		ids, err := publish.ImportPlanet(a.store, a.keystore(), c, *force, println)
		if err != nil {
			return err
		}
		fmt.Printf("imported %d sites into %s\n", len(ids), a.dataDir)
		return nil
	case "passcode":
		if len(rest) < 1 || rest[0] != "set" {
			return fmt.Errorf("usage: croptop passcode set")
		}
		fmt.Print("New passcode: ")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		line = strings.TrimSpace(line)
		if len(line) < 4 {
			return fmt.Errorf("passcode must be at least 4 characters")
		}
		a.cfg.SetPasscode(line)
		if err := a.cfg.Save(a.dataDir); err != nil {
			return err
		}
		fmt.Println("Passcode set. Sign in as user \"Croptop\". Start with --listen 0.0.0.0:8086 to reach it from other devices.")
		return nil
	case "key":
		if len(rest) < 2 {
			return fmt.Errorf("usage: croptop key export <site> | key import <site> <file.pem>")
		}
		site, err := a.findSite(rest[1])
		if err != nil {
			return err
		}
		switch rest[0] {
		case "export":
			pem, err := a.keystore().ExportPEM(site.ID)
			if err != nil {
				return fmt.Errorf("no key for %s on this machine", site.Name)
			}
			os.Stdout.Write(pem)
			return nil
		case "import":
			if len(rest) < 3 {
				return fmt.Errorf("usage: croptop key import <site> <file.pem>")
			}
			b, err := os.ReadFile(rest[2])
			if err != nil {
				return err
			}
			ks := a.keystore()
			if err := ks.ImportPEM(site.ID, b); err != nil {
				return err
			}
			if name, _ := ks.Name(site.ID); name != site.IPNS {
				ks.Delete(site.ID)
				return fmt.Errorf("that key belongs to %s, not %s", name, site.IPNS)
			}
			fmt.Println("key imported for", site.Name)
			return nil
		}
		return fmt.Errorf("unknown key command %q", rest[0])
	}

	// the rest need kubo running
	if err := a.startNode(context.Background()); err != nil {
		return err
	}
	defer a.engine.Stop()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	switch cmd {
	case "publish":
		a.pubWait = true
		if len(rest) < 1 {
			return fmt.Errorf("usage: croptop publish <site name or id>")
		}
		site, err := a.findSite(rest[0])
		if err != nil {
			return err
		}
		res, err := a.pub.Publish(ctx, site.ID, *force)
		if err != nil {
			return err
		}
		fmt.Printf("published %s\n  cid      %s\n  sequence %d\n  url      %s\n", site.Name, res.CID, res.Sequence, render.SiteURL(site))
		return nil
	case "sync":
		if len(rest) < 1 {
			return fmt.Errorf("usage: croptop sync <site name or id>")
		}
		site, err := a.findSite(rest[0])
		if err != nil {
			return err
		}
		res, err := a.pub.Sync(ctx, site.ID)
		if err != nil {
			return err
		}
		fmt.Printf("synced %s: %d new, %d updated; published at sequence %d\n", site.Name, res.Added, res.Updated, res.Result.Sequence)
		return nil
	case "adopt":
		if len(rest) < 1 || *keyFile == "" {
			return fmt.Errorf("usage: croptop adopt <ipns-name-or-ens> --key site.pem")
		}
		pem, err := os.ReadFile(*keyFile)
		if err != nil {
			return err
		}
		id, err := a.pub.Adopt(ctx, rest[0], pem)
		if err != nil {
			return err
		}
		if err := a.pub.Render.Render(ctx, id); err != nil {
			return err
		}
		site, _ := a.store.Site(id)
		fmt.Printf("adopted %s (%s) at sequence %d\n", site.Name, id, site.IPNSSequence)
		return nil
	case "follow":
		if len(rest) < 1 {
			return fmt.Errorf("usage: croptop follow <ipns name or ENS name>")
		}
		e, err := a.follow.Follow(ctx, rest[0])
		if err != nil {
			return err
		}
		fmt.Printf("following %s (%s) at %s\n", e.Title, e.IPNS, e.CID)
		return nil
	case "unfollow":
		if len(rest) < 1 {
			return fmt.Errorf("usage: croptop unfollow <name or ipns>")
		}
		list, _ := a.follow.List()
		for _, e := range list {
			if e.IPNS == rest[0] || strings.EqualFold(e.Name, rest[0]) || strings.EqualFold(e.Title, rest[0]) {
				return a.follow.Unfollow(e.IPNS)
			}
		}
		return fmt.Errorf("not following %q", rest[0])
	case "following":
		list, err := a.follow.List()
		if err != nil {
			return err
		}
		for _, e := range list {
			fmt.Printf("%s\t%s\t%s\tchanged %s\n", e.Title, e.IPNS, e.CID, e.Changed.Time().Format(time.RFC3339))
		}
		return nil
	case "template":
		if len(rest) < 1 {
			return fmt.Errorf("usage: croptop template list | install <cid or name> | publish <dir>")
		}
		switch rest[0] {
		case "install":
			if len(rest) < 2 {
				return fmt.Errorf("usage: croptop template install <cid or name>")
			}
			info, err := a.tpl.Install(ctx, rest[1])
			if err != nil {
				return err
			}
			fmt.Printf("installed %s %s as %s\n", info.Name, info.Version, info.CID)
			return nil
		case "publish":
			if len(rest) < 2 {
				return fmt.Errorf("usage: croptop template publish <dir>")
			}
			cid, err := a.tpl.Publish(ctx, rest[1])
			if err != nil {
				return err
			}
			fmt.Printf("published template %s\n  cid %s\n  install elsewhere with: croptop template install %s\n", rest[1], cid, cid)
			return nil
		}
		return fmt.Errorf("unknown template command %q", rest[0])
	case "ipfs-smoke":
		info, err := a.engine.Info(ctx)
		fmt.Printf("%s: %+v %v\n", a.cfg.EngineName(), info, err)
		if len(rest) > 0 {
			rec, err := a.engine.NetworkRecord(ctx, rest[0])
			fmt.Printf("record %+v err=%v\n", rec, err)
		}
		return nil
	}
	return fmt.Errorf("unknown command %q (try croptop help)", cmd)
}

// flagsFirst moves --flags ahead of positional arguments so that
// `croptop key export MySite --data .data` works; Go's flag package stops
// at the first positional otherwise.
func flagsFirst(args []string) []string {
	boolFlags := map[string]bool{"no-open": true, "force": true, "h": true, "help": true}
	var flags, rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			rest = append(rest, a)
			continue
		}
		name := strings.TrimLeft(a, "-")
		flags = append(flags, a)
		if !strings.Contains(name, "=") && !boolFlags[name] && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return append(flags, rest...)
}

func (a *app) open(engine string) error {
	var err error
	if a.cfg, err = config.Load(a.dataDir); err != nil {
		return err
	}
	if engine != "" && engine != a.cfg.EngineName() {
		if engine != "kubo" && engine != "embedded" {
			return fmt.Errorf("--engine must be kubo or embedded")
		}
		a.cfg.Engine = engine
		if err := a.cfg.Save(a.dataDir); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(a.dataDir, 0o755); err != nil {
		return err
	}
	a.store = &store.Store{Root: a.dataDir}
	a.tmpl = templates.FS
	if a.templatesDir != "" {
		a.tmpl = os.DirFS(a.templatesDir)
		if _, err := fs.Stat(a.tmpl, "template.json"); err != nil {
			return fmt.Errorf("--templates %s: no template.json", a.templatesDir)
		}
	}
	logf := func(line string) {
		if os.Getenv("CROPTOP_DEBUG") != "" {
			fmt.Fprintln(os.Stderr, line)
		}
	}
	if a.cfg.EngineName() == "embedded" {
		e := ipfs.NewEmbedded(a.dataDir)
		e.Log = logf
		a.engine = e
	} else {
		bin := a.cfg.KuboBin
		if bin == "" {
			bin = filepath.Join(a.dataDir, "kubo", "ipfs")
			if runtime.GOOS == "windows" {
				bin += ".exe"
			}
		}
		a.kubo = ipfs.NewNode(bin, filepath.Join(a.dataDir, "ipfs"))
		a.kubo.Log = logf
		a.engine = a.kubo
	}
	ffmpeg, _ := exec.LookPath("ffmpeg")
	a.tpl = &tpl.Resolver{DataDir: a.dataDir, Store: a.store, Default: a.tmpl, Engine: a.engine, Log: println}
	r := &render.Renderer{Store: a.store, Templates: a.tmpl, TemplateFor: a.tpl.TemplateFor, CIDs: a.engine, FFmpeg: ffmpeg, Log: println}
	a.pub = &publish.Publisher{Store: a.store, Node: a.engine, Render: r, Log: println, Wait: a.pubWait}
	a.follow = &follow.Store{Root: a.dataDir, Engine: a.engine, Log: println}
	return nil
}

func (a *app) keystore() *ipfs.Keystore { return a.engine.Keystore() }

func (a *app) startNode(ctx context.Context) error {
	if a.kubo != nil {
		if a.cfg.KuboBin == "" {
			bin, err := ipfs.EnsureKubo(filepath.Join(a.dataDir, "kubo"), println)
			if err != nil {
				return fmt.Errorf("get kubo: %w", err)
			}
			a.kubo.Bin = bin
		}
		if err := a.kubo.Init(ctx); err != nil {
			return fmt.Errorf("ipfs init: %w", err)
		}
		println("starting ipfs (kubo)")
	} else {
		println("starting ipfs (embedded)")
	}
	if err := a.engine.Start(ctx); err != nil {
		return fmt.Errorf("ipfs: %w", err)
	}
	if n := a.engine.ConnectLocalNodes(ctx); n > 0 {
		println(fmt.Sprintf("peered with %d local ipfs node(s)", n))
	}
	return nil
}

func (a *app) findSite(nameOrID string) (*store.Site, error) {
	if s, err := a.store.Site(strings.ToUpper(nameOrID)); err == nil {
		return s, nil
	}
	sites, err := a.store.Sites()
	if err != nil {
		return nil, err
	}
	for _, s := range sites {
		if strings.EqualFold(s.Name, nameOrID) || (s.Domain != nil && strings.EqualFold(*s.Domain, nameOrID)) || s.IPNS == nameOrID {
			return s, nil
		}
	}
	var prefix []*store.Site
	for _, s := range sites {
		if strings.HasPrefix(strings.ToLower(s.Name), strings.ToLower(nameOrID)) {
			prefix = append(prefix, s)
		}
	}
	if len(prefix) == 1 {
		return prefix[0], nil
	}
	return nil, fmt.Errorf("no site named %q; sites: %s", nameOrID, siteNames(sites))
}

func siteNames(sites []*store.Site) string {
	var out []string
	for _, s := range sites {
		out = append(out, s.Name)
	}
	return strings.Join(out, ", ")
}

func (a *app) serve(listen string, noOpen bool) error {
	if listen != "" {
		a.cfg.Listen = listen
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := a.startNode(ctx); err != nil {
		return err
	}
	defer a.engine.Stop()
	go a.pub.RunKeepalive(ctx, 10*time.Minute)
	go a.follow.Run(ctx, 6*time.Hour)
	go func() { // DHT provider records expire after 48h
		t := time.NewTicker(12 * time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				a.pub.ProvideAll(ctx)
				a.follow.ProvideAll(ctx)
			}
		}
	}()
	ui, _ := fs.Sub(web.FS, ".")
	srv := &server.Server{
		Store: a.store, Pub: a.pub, Follow: a.follow, Tpl: a.tpl, Node: a.engine, Cfg: a.cfg, UI: ui, Templates: a.tmpl,
		Version: version, DataDir: a.dataDir, Log: println,
	}
	url := "http://" + strings.Replace(a.cfg.Listen, "0.0.0.0", "127.0.0.1", 1)
	println("console at " + url)
	if !noOpen {
		go func() { time.Sleep(300 * time.Millisecond); openBrowser(url) }()
	}
	go func() {
		<-ctx.Done()
		println("stopping")
	}()
	return srv.ListenAndServe(ctx, a.cfg.Listen)
}

// host runs the crop.top role: gateway, pin host, and name registry for a domain.
func (a *app) host(domain, listen, root, announce, trust string) error {
	if listen == "" {
		listen = "127.0.0.1:8090"
	}
	if err := a.open("embedded"); err != nil {
		return err
	}
	e, ok := a.engine.(*ipfs.Embedded)
	if !ok {
		return fmt.Errorf("host needs the embedded engine")
	}
	if announce != "" {
		e.Announce = strings.Split(announce, ",")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := a.startNode(ctx); err != nil {
		return err
	}
	defer a.engine.Stop()
	h := &host.Host{Domain: strings.ToLower(domain), DataDir: a.dataDir, Engine: e, Log: println, Root: root}
	if trust != "" {
		h.Trust = strings.Split(trust, ",")
	}
	if err := h.Start(); err != nil {
		return err
	}
	go h.Run(ctx)
	srv := &http.Server{Addr: listen, Handler: h, ReadHeaderTimeout: 30 * time.Second}
	go func() {
		<-ctx.Done()
		println("stopping")
		sctx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		srv.Shutdown(sctx)
	}()
	println(fmt.Sprintf("hosting %s at http://%s", h.Domain, listen))
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// status reads the running console's API and prints a summary.
func (a *app) status() error {
	base := "http://" + strings.Replace(a.cfg.Listen, "0.0.0.0", "127.0.0.1", 1)
	get := func(path string, v any) error {
		resp, err := http.Get(base + path)
		if err != nil {
			return fmt.Errorf("console not reachable at %s (is `croptop serve` running?): %w", base, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode == 401 {
			return fmt.Errorf("the console has a passcode; set PN_API_PASSCODE or check from the browser")
		}
		return json.NewDecoder(resp.Body).Decode(v)
	}
	var st struct {
		Version string `json:"version"`
		IPFS    struct {
			Running bool   `json:"running"`
			Peers   int    `json:"peers"`
			PeerID  string `json:"peerID"`
			Version string `json:"version"`
		} `json:"ipfs"`
	}
	if err := get("/v0/croptop/status", &st); err != nil {
		return err
	}
	fmt.Printf("croptop %s, ipfs %s, %d peers, id %s\n", st.Version, st.IPFS.Version, st.IPFS.Peers, st.IPFS.PeerID)
	var sites []map[string]any
	if err := get("/v0/planets/my", &sites); err != nil {
		return err
	}
	fmt.Println("sites:")
	for _, s := range sites {
		seq, _ := s["ipnsSequence"].(float64)
		state := "live"
		if s["publishedElsewhere"] == true {
			state = "published elsewhere"
		}
		if s["lastPublishedCID"] == nil {
			state = "never published"
		}
		fmt.Printf("  %-24s seq %-4.0f %s\n", s["name"], seq, state)
	}
	var following []map[string]any
	if err := get("/v0/croptop/following", &following); err != nil {
		return err
	}
	if len(following) > 0 {
		fmt.Println("following:")
		for _, f := range following {
			fmt.Printf("  %-24s %s\n", f["title"], f["cid"])
		}
	}
	return nil
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	cmd.Start()
}

func println(s string) { fmt.Println(s) }
