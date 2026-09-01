// Command exe is a personal VM cloud for your Mac: a daemon that runs
// Virtualization.framework VMs, a vibecoding agent backed by Ollama, and a
// Cloudflare-Tunnel-published reverse proxy — plus the CLI to drive it all.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	"exe/internal/agent"
	"exe/internal/config"
	"exe/internal/hostinfo"
	"exe/internal/keys"
	"exe/internal/peer"
	"exe/internal/proxy"
	"exe/internal/server"
	"exe/internal/vmm"
)

const usage = `exe — a personal VM cloud on this machine

Usage:
  exe serve                                run the daemon (API + HTTP proxy)
  exe init                                 write a starter config (~/.exe/config.json)
  exe ls                                   list VMs
  exe create <name> [-cpus N] [-mem MB] [-disk GB]
  exe start <name>
  exe stop <name>
  exe rm <name>
  exe ip <name>
  exe ssh <name> [command...]
  exe code <name> [-m model] <prompt...>   vibecode inside the VM (Ollama agent)
  exe expose <name> -port N [-sub name]    publish https://<sub>.<domain> -> VM port
  exe unexpose <host>                      remove a proxy route
  exe routes                               show proxy routes

The daemon also speaks SSH on :2222 (config ssh_listen):
  ssh -p 2222 exe@<mac>     lobby: ls / new / rm / code / expose ... (--json for scripts)
  ssh -p 2222 <vm>@<mac>    full SSH into the VM (scp, sftp, -L/-R; auto-starts it)
`

func main() {
	log.SetFlags(log.Ltime)
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "serve":
		err = cmdServe()
	case "init":
		err = cmdInit()
	case "ls":
		err = cmdLs()
	case "create":
		err = cmdCreate(args)
	case "start":
		err = cmdSimpleVM(args, "start")
	case "stop":
		err = cmdSimpleVM(args, "stop")
	case "rm":
		err = cmdRm(args)
	case "ip":
		err = cmdIP(args)
	case "ssh":
		err = cmdSSH(args)
	case "code":
		err = cmdCode(args)
	case "expose":
		err = cmdExpose(args)
	case "unexpose":
		err = cmdUnexpose(args)
	case "routes":
		err = cmdRoutes()
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Printf("unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// ---- daemon ----------------------------------------------------------------

func cmdServe() error {
	// Tee the daemon log into a ring buffer so the web UI can stream it
	// (GET /v1/logs); the terminal still gets everything on stderr.
	logs := server.NewLogBuffer(1000)
	log.SetOutput(io.MultiWriter(os.Stderr, logs))
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	// File-back the ring so the log survives restarts. After config.Load so
	// the state dir exists; failure just means a memory-only log.
	if err := logs.Persist(filepath.Join(config.Dir(), "daemon.log")); err != nil {
		log.Printf("daemon.log: %v (log will not survive restarts)", err)
	}
	stateDir := config.Dir()
	go hostinfo.Model() // warm the machine-model cache for the About window
	privKey, pubKey, err := keys.Ensure(stateDir)
	if err != nil {
		return err
	}
	mgr, err := vmm.New(vmm.Options{
		StateDir:       stateDir,
		ImageURL:       cfg.ImageURL,
		SSHUser:        cfg.SSHUser,
		AuthorizedKey:  pubKey,
		PrivateKeyPath: privKey,
		Firecracker: vmm.FirecrackerOptions{
			Binary:            cfg.Firecracker.Binary,
			KernelURL:         cfg.Firecracker.KernelURL,
			NetworkHelper:     cfg.Firecracker.NetworkHelper,
			NetworkCIDR:       cfg.Firecracker.NetworkCIDR,
			OutboundInterface: cfg.Firecracker.OutboundInterface,
		},
		QEMU: vmm.QEMUOptions{
			Binary:      cfg.QEMU.Binary,
			FirmwareDir: cfg.QEMU.FirmwareDir,
			NetworkCIDR: cfg.QEMU.NetworkCIDR,
		},
	})
	if err != nil {
		return err
	}
	px, err := proxy.New(filepath.Join(stateDir, "routes.json"))
	if err != nil {
		return err
	}
	// On backends whose guest network lives inside this process (Windows),
	// the reverse proxy must dial VM backends through the manager.
	if gd, ok := mgr.(vmm.GuestDialer); ok {
		px.SetDial(gd.DialGuest)
	}
	srv := server.New(cfg, mgr, px, privKey, stateDir)
	srv.Logs = logs
	gate, err := server.NewSSHGate(srv)
	if err != nil {
		return err
	}

	// Node-to-node sync: identity + engine. Failure only disables sync, the
	// daemon still serves everything else.
	var eng *peer.Engine
	if ident, perr := peer.LoadIdentity(stateDir); perr != nil {
		log.Printf("peer identity: %v — node sync disabled", perr)
	} else if eng, perr = peer.NewEngine(peer.EngineConfig{
		StateDir:     stateDir,
		DataDir:      filepath.Join(stateDir, "appdata"),
		WorkspaceDir: filepath.Join(stateDir, "workspace"),
		Self:         ident,
		PortFn:       func() string { return portOf(srv.Config().Listen) },
		// a peer-applied remote change has no local writer, so an empty client tag
		OnApply: func(app, rel string, deleted bool) { srv.BroadcastAppData(app, rel, deleted, "") },
		OnConflict: func(app, rel string) {
			where := app + "/" + rel
			if app == peer.WorkspaceNS {
				where = "Workspace/" + rel
			}
			srv.PostNews("sync", "Sync conflict",
				where+" was edited on two nodes at once — this node kept the winning copy and saved the other under sync-conflicts.")
		},
		Logf: log.Printf,
	}); perr != nil {
		log.Printf("peer sync: %v — node sync disabled", perr)
		eng = nil
	} else {
		srv.Peers = eng
		eng.Start()
		log.Printf("node sync: this node is %s (%s)", ident.ID, ident.Name)
	}

	if ie, ok := mgr.(vmm.ImageEnsurer); ok {
		go func() {
			if _, err := ie.EnsureImage(context.Background()); err != nil {
				log.Printf("base image prefetch failed: %v", err)
			}
		}()
	}

	// Chat sessions from before summaries (or the wider title cap)
	// existed get theirs on boot.
	go srv.BackfillChatMeta()

	// The hub agent answers replies on the exe-hub as an identity of its
	// own; it idles until hub.url / hub.agent.* are configured.
	go srv.RunHubAgent(context.Background())

	apiHandler := srv.Handler()
	proxyHandler := px.Handler()
	errc := make(chan error, 4)
	serveHTTP := func(h http.Handler, lns ...net.Listener) *http.Server {
		hs := &http.Server{Handler: h}
		for _, ln := range lns {
			go func() {
				if err := hs.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
					errc <- err
				}
			}()
		}
		return hs
	}
	// bindAPI binds addr plus, when addr names a specific non-loopback IP
	// (e.g. a Tailscale address), a 127.0.0.1 companion on the same port —
	// the CLI, local agents and skill.md consumers on this machine keep
	// working no matter where the API is published.
	bindAPI := func(addr string, wait time.Duration) ([]net.Listener, error) {
		ln, err := listenRetry(addr, wait)
		if err != nil {
			return nil, err
		}
		lns := []net.Listener{ln}
		if lo := loopbackAddr(addr); lo != "" {
			if ln2, lerr := listenRetry(lo, wait); lerr != nil {
				log.Printf("api: %s not bound (%v) — API is only on %s", lo, lerr, addr)
			} else {
				lns = append(lns, ln2)
			}
		}
		return lns, nil
	}
	// Retry binds briefly: after POST /v1/daemon/restart the parent process
	// is still releasing these ports while we come up.
	apiLns, err := bindAPI(cfg.Listen, 15*time.Second)
	if err != nil {
		return err
	}
	proxyLn, err := listenRetry(cfg.ProxyListen, 15*time.Second)
	if err != nil {
		return err
	}
	var srvMu sync.Mutex // guards apiSrv/proxySrv/sshLn/cur* across rebinds and shutdown
	curListen, curProxy := cfg.Listen, cfg.ProxyListen
	apiSrv := serveHTTP(apiHandler, apiLns...)
	proxySrv := serveHTTP(proxyHandler, proxyLn)

	// SSH gate (lobby + direct VM ssh); its sessions live independently of
	// the listener, so a rebind only swaps where new connections land.
	serveSSH := func(ln net.Listener) {
		go func() {
			if err := gate.Serve(ln); err != nil {
				errc <- err
			}
		}()
	}
	var sshLn net.Listener
	curSSH := cfg.SSHListen
	if config.SSHEnabled(curSSH) {
		if sshLn, err = listenRetry(curSSH, 15*time.Second); err != nil {
			return err
		}
		serveSSH(sshLn)
	}
	rebindSSH := func(next string) {
		if next == curSSH {
			return
		}
		if sshLn != nil {
			sshLn.Close()
			sshLn = nil
		}
		if config.SSHEnabled(next) {
			ln, lerr := listenRetry(next, 10*time.Second)
			if lerr != nil {
				log.Printf("rebind ssh to %s failed: %v — ssh gate is down until the address is fixed", next, lerr)
			} else {
				sshLn = ln
				serveSSH(ln)
				log.Printf("ssh gate rebound to %s", next)
			}
		} else {
			log.Printf("ssh gate disabled")
		}
		curSSH = next
	}

	// Re-bind a listener in place when listen/proxy_listen change via
	// PUT /v1/config — VMs live in this process and are untouched.
	bindPlain := func(addr string, wait time.Duration) ([]net.Listener, error) {
		ln, err := listenRetry(addr, wait)
		if err != nil {
			return nil, err
		}
		return []net.Listener{ln}, nil
	}
	rebindOne := func(name string, hs **http.Server, h http.Handler, cur *string, next string, bind func(string, time.Duration) ([]net.Listener, error)) {
		if next == *cur {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		(*hs).Shutdown(ctx)
		cancel()
		lns, err := bind(next, 10*time.Second)
		if err != nil {
			log.Printf("rebind %s to %s failed: %v — keeping %s", name, next, err, *cur)
			lns, err = bind(*cur, 10*time.Second)
			if err != nil {
				errc <- fmt.Errorf("rebind %s: lost both %s and %s: %v", name, next, *cur, err)
				return
			}
		} else {
			*cur = next
			log.Printf("%s rebound to %s", name, *cur)
		}
		*hs = serveHTTP(h, lns...)
	}
	srv.OnRebind = func(newListen, newProxy, newSSH string) {
		go func() {
			time.Sleep(300 * time.Millisecond) // let the PUT response drain first
			srvMu.Lock()
			defer srvMu.Unlock()
			rebindOne("api", &apiSrv, apiHandler, &curListen, newListen, bindAPI)
			rebindOne("proxy", &proxySrv, proxyHandler, &curProxy, newProxy, bindPlain)
			rebindSSH(newSSH)
		}()
	}
	log.Printf("exe daemon: API http://%s, proxy %s, state %s", displayAddr(cfg.Listen), cfg.ProxyListen, stateDir)
	if len(apiLns) > 1 {
		log.Printf("api: also on http://%s", apiLns[1].Addr())
	}
	if ip := server.TailscaleIP(); ip != "" {
		log.Printf("tailscale: %s", ip)
	}
	if cfg.Ollama.BaseURL != "" {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			v, err := agent.Version(ctx, agent.Config{BaseURL: cfg.Ollama.BaseURL, APIKey: cfg.Ollama.APIKey})
			if err == nil {
				log.Printf("ollama %s at %s", v, cfg.Ollama.BaseURL)
			}
		}()
	}
	if config.SSHEnabled(curSSH) {
		log.Printf("ssh gate on %s: `ssh -p %s exe@<this host>` is the lobby, `ssh -p %s <vm>@<this host>` is the VM", curSSH, portOf(curSSH), portOf(curSSH))
	}
	log.Printf("note: VMs run inside this process and power off when it exits")

	// After an in-place restart (POST /v1/daemon/restart) the previous
	// process hands us the VMs that were running so we bring them back.
	if names := os.Getenv("EXE_AUTOSTART"); names != "" {
		os.Unsetenv("EXE_AUTOSTART")
		go func() {
			for _, name := range strings.Split(names, ",") {
				if name == "" {
					continue
				}
				if _, err := mgr.Start(context.Background(), name); err != nil {
					log.Printf("autostart %s: %v", name, err)
				} else {
					log.Printf("autostart %s: running", name)
				}
			}
		}()
	}

	shutdown := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.DrainChatRuns(ctx, "stopped: the daemon is shutting down") // detached replies persist and end cleanly
		if eng != nil {
			eng.Stop()
		}
		srvMu.Lock()
		apiSrv.Shutdown(ctx)
		proxySrv.Shutdown(ctx)
		if sshLn != nil {
			sshLn.Close()
		}
		srvMu.Unlock()
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	// Swallow SIGHUP instead of inheriting whatever came before: a daemon
	// once launched under nohup carries SIG_IGN here, re-execs itself on
	// in-app restart, and passes the ignore down forever — including into
	// every Terminal-window shell, whose jobs then shrug off the hangup
	// when their window closes (exec resets a *caught* handler to the
	// default, but keeps an ignored one). Catching it keeps the daemon
	// itself immune to terminal hangups and starts children clean.
	signal.Notify(make(chan os.Signal, 1), syscall.SIGHUP)
	wait := func() error {
		select {
		case err := <-errc:
			return err
		case <-sig:
			log.Printf("shutting down")
			shutdown()
			return nil
		}
	}
	return serveWait(srv, wait, shutdown)
}

func portOf(addr string) string {
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[i+1:]
	}
	return addr
}

// loopbackAddr returns "127.0.0.1:<port>" when addr names a specific
// non-loopback host (a Tailscale or LAN IP), so the API stays reachable from
// this machine; "" when addr already covers loopback (wildcard or 127.x).
func loopbackAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || host == "" || host == "localhost" {
		return ""
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsUnspecified()) {
		return ""
	}
	return net.JoinHostPort("127.0.0.1", port)
}

func listenRetry(addr string, wait time.Duration) (net.Listener, error) {
	deadline := time.Now().Add(wait)
	for {
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			return ln, nil
		}
		if !strings.Contains(err.Error(), "address already in use") || time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func cmdInit() error {
	p, err := config.WriteTemplate()
	if err != nil {
		return err
	}
	fmt.Printf(`wrote %s

Fill in:
  ollama.api_key           https://ollama.com/settings/keys (or export OLLAMA_API_KEY)
  cloudflare.api_token     token with Zone:DNS:Edit + Account:Cloudflare Tunnel:Edit
  cloudflare.account_id    dash.cloudflare.com -> right sidebar
  cloudflare.zone_id       zone overview page
  cloudflare.tunnel_id     Zero Trust -> Networks -> Tunnels (must be remotely managed)
  cloudflare.domain        e.g. apps.example.com or example.com
  advertise_host           this Mac's LAN or Tailscale IP, reachable from the tunnel host
                           (pre-filled with the Tailscale IP when detected)

Then: exe serve
`, p)
	return nil
}

// ---- API client helpers ------------------------------------------------------

func displayAddr(l string) string {
	if strings.HasPrefix(l, ":") {
		return "127.0.0.1" + l
	}
	return l
}

func api(cfg *config.Config, method, path string, body any, timeout time.Duration) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, "http://"+displayAddr(cfg.Listen)+path, rd)
	if err != nil {
		return nil, err
	}
	if cfg.APIToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIToken)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		if strings.Contains(err.Error(), "connection refused") {
			return nil, fmt.Errorf("cannot reach the exe daemon at %s — is `exe serve` running?", displayAddr(cfg.Listen))
		}
		return nil, err
	}
	return resp, nil
}

func decodeInto(resp *http.Response, out any) error {
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 400 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			return fmt.Errorf("%s", e.Error)
		}
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, raw)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// splitName pulls the first non-flag argument out so flags may appear
// before or after the VM name.
func splitName(args []string) (string, []string, error) {
	for i, a := range args {
		if !strings.HasPrefix(a, "-") {
			rest := make([]string, 0, len(args)-1)
			rest = append(rest, args[:i]...)
			rest = append(rest, args[i+1:]...)
			return a, rest, nil
		}
	}
	return "", nil, fmt.Errorf("vm name required")
}

// ---- vm commands -------------------------------------------------------------

func printVMs(vms []*vmm.Info) {
	tw := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSTATE\tCPUS\tMEM\tDISK\tIP")
	for _, v := range vms {
		ip := v.IP
		if ip == "" {
			ip = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%dMB\t%dGB\t%s\n", v.Name, v.State, v.CPUs, v.MemoryMB, v.DiskGB, ip)
	}
	tw.Flush()
}

func cmdLs() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	resp, err := api(cfg, "GET", "/v1/vms", nil, 30*time.Second)
	if err != nil {
		return err
	}
	var vms []*vmm.Info
	if err := decodeInto(resp, &vms); err != nil {
		return err
	}
	printVMs(vms)
	return nil
}

func cmdCreate(args []string) error {
	name, rest, err := splitName(args)
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("create", flag.ExitOnError)
	cpus := fs.Int("cpus", 0, "vCPUs")
	mem := fs.Int("mem", 0, "memory MB")
	disk := fs.Int("disk", 0, "disk GB")
	fs.Parse(rest)
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	fmt.Printf("creating %s (first run may download the base image; watch `exe serve` logs)...\n", name)
	resp, err := api(cfg, "POST", "/v1/vms",
		vmm.Spec{Name: name, CPUs: *cpus, MemoryMB: *mem, DiskGB: *disk}, 45*time.Minute)
	if err != nil {
		return err
	}
	var info vmm.Info
	if err := decodeInto(resp, &info); err != nil {
		return err
	}
	printVMs([]*vmm.Info{&info})
	fmt.Printf("\nnext:\n  exe ssh %s\n  exe code %s \"build me ...\"\n  exe expose %s -port 8000\n", name, name, name)
	return nil
}

func cmdSimpleVM(args []string, action string) error {
	name, _, err := splitName(args)
	if err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	resp, err := api(cfg, "POST", "/v1/vms/"+name+"/"+action, map[string]string{}, 10*time.Minute)
	if err != nil {
		return err
	}
	if action == "start" {
		var info vmm.Info
		if err := decodeInto(resp, &info); err != nil {
			return err
		}
		printVMs([]*vmm.Info{&info})
		return nil
	}
	if err := decodeInto(resp, nil); err != nil {
		return err
	}
	fmt.Printf("%s: %s ok\n", name, action)
	return nil
}

func cmdRm(args []string) error {
	name, _, err := splitName(args)
	if err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	resp, err := api(cfg, "DELETE", "/v1/vms/"+name, nil, 2*time.Minute)
	if err != nil {
		return err
	}
	if err := decodeInto(resp, nil); err != nil {
		return err
	}
	fmt.Printf("%s: deleted\n", name)
	return nil
}

func getVM(cfg *config.Config, name string) (*vmm.Info, error) {
	resp, err := api(cfg, "GET", "/v1/vms/"+name, nil, 30*time.Second)
	if err != nil {
		return nil, err
	}
	var info vmm.Info
	if err := decodeInto(resp, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

func cmdIP(args []string) error {
	name, _, err := splitName(args)
	if err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	info, err := getVM(cfg, name)
	if err != nil {
		return err
	}
	if info.IP == "" {
		return fmt.Errorf("vm %s has no IP (state=%s)", name, info.State)
	}
	fmt.Println(info.IP)
	return nil
}

func cmdSSH(args []string) error {
	name, rest, err := splitName(args)
	if err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	info, err := getVM(cfg, name)
	if err != nil {
		return err
	}
	keyPath := filepath.Join(config.Dir(), "ssh", "id_ed25519")
	sshArgs := []string{
		"-i", keyPath,
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=" + os.DevNull, // NUL on Windows
		"-o", "LogLevel=ERROR",
	}
	if runtime.GOOS == "windows" {
		// VM IPs live inside the daemon process on Windows, so go through
		// the SSH gate, which auto-starts the VM and bridges to its sshd.
		if !config.SSHEnabled(cfg.SSHListen) {
			return fmt.Errorf("VM IPs are only reachable inside the daemon on Windows; enable ssh_listen to use `exe ssh`")
		}
		sshArgs = append(sshArgs, "-p", portOf(cfg.SSHListen), name+"@127.0.0.1")
	} else {
		if info.IP == "" {
			return fmt.Errorf("vm %s has no IP (state=%s); `exe start %s` first", name, info.State, name)
		}
		sshArgs = append(sshArgs, cfg.SSHUser+"@"+info.IP)
	}
	sshArgs = append(sshArgs, rest...)
	c := exec.Command("ssh", sshArgs...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := c.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		return err
	}
	return nil
}

func cmdCode(args []string) error {
	name, rest, err := splitName(args)
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("code", flag.ExitOnError)
	model := fs.String("m", "", "override model (e.g. glm-5.2, qwen3-coder)")
	fs.Parse(rest)
	prompt := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if prompt == "" {
		b, _ := io.ReadAll(os.Stdin)
		prompt = strings.TrimSpace(string(b))
	}
	if prompt == "" {
		return fmt.Errorf("prompt required: exe code %s \"build me ...\"", name)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	resp, err := api(cfg, "POST", "/v1/vms/"+name+"/agent",
		map[string]string{"prompt": prompt, "model": *model}, 0)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return decodeInto(resp, nil)
	}
	_, err = io.Copy(os.Stdout, resp.Body)
	return err
}

func cmdExpose(args []string) error {
	name, rest, err := splitName(args)
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("expose", flag.ExitOnError)
	port := fs.Int("port", 0, "VM port to publish (required)")
	sub := fs.String("sub", "", "subdomain (default: vm name)")
	fs.Parse(rest)
	if *port == 0 {
		return fmt.Errorf("-port is required")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	resp, err := api(cfg, "POST", "/v1/vms/"+name+"/expose",
		map[string]any{"subdomain": *sub, "port": *port}, 2*time.Minute)
	if err != nil {
		return err
	}
	var res map[string]any
	if err := decodeInto(resp, &res); err != nil {
		return err
	}
	fmt.Printf("route: %s -> %s\n", res["host"], res["backend"])
	if v, ok := res["dns"]; ok {
		fmt.Println("dns:", v)
	}
	if v, ok := res["ingress"]; ok {
		fmt.Println("tunnel ingress ->", v)
	}
	if ws, ok := res["warnings"].([]any); ok {
		for _, wmsg := range ws {
			fmt.Println("warning:", wmsg)
		}
	}
	fmt.Println("url:", res["url"])
	return nil
}

func cmdUnexpose(args []string) error {
	host, _, err := splitName(args)
	if err != nil {
		return fmt.Errorf("host required, e.g. exe unexpose app.example.com")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	resp, err := api(cfg, "DELETE", "/v1/routes/"+host, nil, 30*time.Second)
	if err != nil {
		return err
	}
	if err := decodeInto(resp, nil); err != nil {
		return err
	}
	fmt.Printf("%s: route removed\n", host)
	return nil
}

func cmdRoutes() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	resp, err := api(cfg, "GET", "/v1/routes", nil, 30*time.Second)
	if err != nil {
		return err
	}
	var routes map[string]string
	if err := decodeInto(resp, &routes); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "HOST\tBACKEND")
	for h, b := range routes {
		fmt.Fprintf(tw, "%s\t%s\n", h, b)
	}
	tw.Flush()
	return nil
}
