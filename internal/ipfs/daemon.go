package ipfs

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Node struct {
	Bin         string
	RepoPath    string
	APIPort     int
	GatewayPort int
	SwarmPort   int

	mu     sync.Mutex
	cmd    *exec.Cmd
	stderr *ring
	done   chan struct{}
	Log    func(string)
}

func NewNode(bin, repoPath string) *Node {
	return &Node{Bin: bin, RepoPath: repoPath, stderr: &ring{n: 50}, Log: func(string) {}}
}

func (n *Node) Keystore() *Keystore { return &Keystore{Dir: filepath.Join(n.RepoPath, "keystore")} }
func (n *Node) GatewayURL() string  { return fmt.Sprintf("http://127.0.0.1:%d", n.GatewayPort) }

func (n *Node) env() []string { return append(os.Environ(), "IPFS_PATH="+n.RepoPath) }

// Start launches the daemon and returns once it prints "Daemon is ready".
func (n *Node) Start(ctx context.Context) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.cmd != nil {
		return nil
	}
	cmd := exec.Command(n.Bin, "daemon")
	cmd.Env = n.env()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	n.cmd = cmd
	n.done = make(chan struct{})
	ready := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			line := sc.Text()
			n.Log("ipfs: " + line)
			if strings.Contains(line, "Daemon is ready") {
				close(ready)
				ready = nil
			}
		}
	}()
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			n.stderr.add(sc.Text())
			n.Log("ipfs! " + sc.Text())
		}
	}()
	go func() {
		cmd.Wait()
		close(n.done)
	}()
	select {
	case <-ready:
		return nil
	case <-n.done:
		return fmt.Errorf("ipfs daemon exited: %s", n.stderr.String())
	case <-time.After(90 * time.Second):
		return errors.New("ipfs daemon did not become ready in 90s")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (n *Node) Running() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.cmd == nil {
		return false
	}
	select {
	case <-n.done:
		return false
	default:
		return true
	}
}

func (n *Node) LastStderr() string { return n.stderr.String() }

func (n *Node) Stop() error {
	n.mu.Lock()
	cmd, done := n.cmd, n.done
	n.cmd = nil
	n.mu.Unlock()
	if cmd == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	n.Run(ctx, "shutdown")
	select {
	case <-done:
		return nil
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		<-done
		return nil
	}
}

// Run executes an ipfs CLI command. Errors carry stderr.
func (n *Node) Run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, n.Bin, args...)
	cmd.Env = n.env()
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ipfs %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return []byte(out.String()), nil
}

func (n *Node) RunJSON(ctx context.Context, v any, args ...string) error {
	out, err := n.Run(ctx, append(args, "--enc=json")...)
	if err != nil {
		return err
	}
	return decodeJSON(out, v)
}

// RunStdin runs a command feeding stdin.
func (n *Node) RunStdin(ctx context.Context, stdin io.Reader, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, n.Bin, args...)
	cmd.Env = n.env()
	cmd.Stdin = stdin
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ipfs %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return []byte(out.String()), nil
}

type ring struct {
	mu    sync.Mutex
	n     int
	lines []string
}

func (r *ring) add(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, s)
	if len(r.lines) > r.n {
		r.lines = r.lines[len(r.lines)-r.n:]
	}
}

func (r *ring) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.lines, "\n")
}
