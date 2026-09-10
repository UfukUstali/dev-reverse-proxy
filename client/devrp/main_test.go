package main_test

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestMissingGatewayFailsBeforeChildLaunch(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "devrp")
	build := exec.Command("go", "build", "-race", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	marker := filepath.Join(t.TempDir(), "child")
	cmd := exec.Command(bin, "--", "sh", "-c", "touch \"$1\"", "sh", marker)
	cmd.Env = append(os.Environ(), "SERVER=")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "SERVER") {
		t.Fatalf("expected actionable missing gateway error: %v %s", err, out)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("child started without gateway")
	}
}

func TestLaunchPassesPublicEnvironmentAndCleansOwnedForwarder(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "devrp")
	if out, err := exec.Command("go", "build", "-race", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	// A controllable executable replaces only the external tailscale command.
	fixture := serveFixture
	if err := os.WriteFile(filepath.Join(dir, "tailscale"), []byte(fixture), 0755); err != nil {
		t.Fatal(err)
	}
	var registered, removed atomic.Bool
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/register":
			var req map[string]any
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req["id"] != "myapp" || req["port"] == float64(3035) || len(req["session"].(string)) != 64 {
				t.Errorf("bad registration: %v", req)
			}
			registered.Store(true)
			_, _ = w.Write([]byte(`{"status":"registered","url":"https://myapp.devrp.example.com","domain":"devrp.example.com","public_ip":"203.0.113.10"}`))
		case "/unregister":
			removed.Store(true)
			_, _ = w.Write([]byte(`{"status":"unregistered"}`))
		default:
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		}
	}))
	defer api.Close()
	cmd := exec.Command(bin, "-s", api.URL, "-p", "3035", "--", "sh", "-c", `printf 'CHILD:%s:%s:%s\n' "$PORT" "$DEVRP_ID" "$DEVRP_BASE_URL"`)
	cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "SERVE_STATE="+filepath.Join(dir, "state"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v %s", err, out)
	}
	if !registered.Load() || !removed.Load() || !strings.Contains(string(out), "CHILD:3035:myapp:https://myapp.devrp.example.com") {
		t.Fatalf("lifecycle/env failure: registered=%v removed=%v %s", registered.Load(), removed.Load(), out)
	}
	if _, err := os.Stat(filepath.Join(dir, "state")); !os.IsNotExist(err) {
		t.Fatal("forwarder was left running")
	}
}

const serveFixture = `#!/usr/bin/env python3
import sys,json,os,time,signal,fcntl
p=os.environ['SERVE_STATE']
def read():
 try: return json.load(open(p))
 except FileNotFoundError: return {}
def write(data):
 if data:
  with open(p,'w') as f: json.dump(data,f)
 elif os.path.exists(p): os.unlink(p)
lock=open(p+'.lock','w')
args=sys.argv[1:]
if args==['serve','status','--json']:
 fcntl.flock(lock,fcntl.LOCK_EX)
 print(json.dumps(read()))
 sys.exit(0)
if len(args)!=3 or args[0]!='serve' or not args[1].startswith('--tcp=') or not args[2].startswith('tcp://127.0.0.1:'):
 sys.exit(3)
port=args[1].split('=')[1]
session=str(os.getpid())
fcntl.flock(lock,fcntl.LOCK_EX)
data=read()
if port in data.get('TCP',{}) or any(port in v.get('TCP',{}) for v in data.get('Foreground',{}).values()): sys.exit(4)
data.setdefault('Foreground',{})[session]={'TCP':{port:{'TCPForward':args[2][6:]}}}
write(data)
fcntl.flock(lock,fcntl.LOCK_UN)
def stop(*_):
 fcntl.flock(lock,fcntl.LOCK_EX)
 data=read()
 data.get('Foreground',{}).pop(session,None)
 if not data.get('Foreground'): data.pop('Foreground',None)
 write(data)
 fcntl.flock(lock,fcntl.LOCK_UN)
 sys.exit(0)
signal.signal(signal.SIGTERM,stop)
signal.signal(signal.SIGINT,stop)
while True:
 crash=os.environ.get('SERVE_CRASH','')
 if crash and os.path.exists(crash):
  os.unlink(crash)
  stop()
 time.sleep(.05)
`

func prepareCLI(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "devrp")
	if out, err := exec.Command("go", "build", "-race", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(dir, "tailscale"), []byte(serveFixture), 0755); err != nil {
		t.Fatal(err)
	}
	return dir, bin
}
func waitFor(t *testing.T, condition func() bool, description string) {
	t.Helper()
	deadline := time.Now().Add(12 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("timed out: " + description)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
func TestOutageRecoveryAndConflictKeepChildAlive(t *testing.T) {
	dir, bin := prepareCLI(t)
	var mode atomic.Int32
	var registrations atomic.Int32
	var heartbeats atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/heartbeat":
			heartbeats.Add(1)
			if mode.Load() == 1 {
				w.WriteHeader(503)
				fmt.Fprint(w, `{"message":"offline"}`)
				return
			}
			if mode.Load() >= 2 {
				w.WriteHeader(404)
				fmt.Fprint(w, `{"message":"restart"}`)
				return
			}
		case "/register":
			if mode.Load() == 1 {
				w.WriteHeader(503)
				fmt.Fprint(w, `{"message":"offline"}`)
				return
			}
			if mode.Load() == 3 {
				w.WriteHeader(409)
				fmt.Fprint(w, `{"message":"name taken"}`)
				return
			}
			registrations.Add(1)
			fmt.Fprint(w, `{"status":"registered","url":"https://myapp.devrp.example.com","domain":"devrp.example.com","public_ip":"203.0.113.10"}`)
			return
		}
		fmt.Fprint(w, `{"status":"ok"}`)
	}))
	defer api.Close()
	logPath := filepath.Join(dir, "output")
	log, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	childPath := filepath.Join(dir, "child-pid")
	cmd := exec.Command(bin, "-s", api.URL, "--", "sh", "-c", `echo $$ > "$1"; trap 'exit 0' TERM; while :; do sleep 1; done`, "sh", childPath)
	cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "SERVE_STATE="+filepath.Join(dir, "state"))
	cmd.Stdout = log
	cmd.Stderr = log
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer func() {
		_ = cmd.Process.Signal(os.Interrupt)
		select {
		case <-done:
		case <-time.After(7 * time.Second):
			_ = cmd.Process.Kill()
		}
	}()
	waitFor(t, func() bool { _, err := os.Stat(childPath); return err == nil }, "child launch")
	data, _ := os.ReadFile(childPath)
	var childPID int
	_, _ = fmt.Sscanf(string(data), "%d", &childPID)
	defer syscall.Kill(-childPID, syscall.SIGKILL)
	mode.Store(1)
	waitFor(t, func() bool { return heartbeats.Load() >= 1 }, "heartbeat outage")
	if err := syscall.Kill(childPID, 0); err != nil {
		t.Fatal("outage stopped application")
	}
	mode.Store(2)
	waitFor(t, func() bool { return registrations.Load() >= 2 }, "gateway restart recovery")
	mode.Store(3)
	waitFor(t, func() bool {
		data, _ := os.ReadFile(logPath)
		return strings.Contains(string(data), "Application remains running")
	}, "reconnect conflict")
	if err := syscall.Kill(childPID, 0); err != nil {
		t.Fatal("conflict stopped application")
	}
	if _, err := os.Stat(filepath.Join(dir, "state")); !os.IsNotExist(err) {
		t.Fatal("conflict left forwarder active")
	}
	_ = cmd.Process.Signal(os.Interrupt)
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("conflict should produce failure exit status")
		}
		done <- err
	case <-time.After(8 * time.Second):
		t.Fatal("Ctrl-C did not finish")
	}
	if err := syscall.Kill(childPID, 0); err == nil {
		t.Fatal("Ctrl-C left child alive")
	}
}

func TestExistingPortSurvivesCtrlC(t *testing.T) {
	dir, bin := prepareCLI(t)
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "alive") }))
	defer app.Close()
	var assigned, removed atomic.Bool
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/register" {
			assigned.Store(true)
			fmt.Fprint(w, `{"status":"registered","url":"https://myapp.devrp.example.com","domain":"devrp.example.com","public_ip":"203.0.113.10"}`)
			return
		}
		if r.URL.Path == "/unregister" {
			removed.Store(true)
		}
		fmt.Fprint(w, `{"status":"ok"}`)
	}))
	defer api.Close()
	cmd := exec.Command(bin, "-s", api.URL, "-p", fmt.Sprint(app.Listener.Addr().(*net.TCPAddr).Port))
	cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "SERVE_STATE="+filepath.Join(dir, "state"))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	waitFor(t, assigned.Load, "existing port assigned")
	_ = cmd.Process.Signal(os.Interrupt)
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if !removed.Load() {
		t.Fatal("Ctrl-C did not remove publication")
	}
	resp, err := http.Get(app.URL)
	if err != nil {
		t.Fatal("Ctrl-C stopped independent application", err)
	}
	resp.Body.Close()
	if _, err := os.Stat(filepath.Join(dir, "state")); !os.IsNotExist(err) {
		t.Fatal("Ctrl-C left Serve running")
	}
}

func TestMultiplePublicationsPreserveOtherServeConfiguration(t *testing.T) {
	dir, bin := prepareCLI(t)
	state := filepath.Join(dir, "state")
	unrelated := `{"TCP":{"24000":{"TCPForward":"127.0.0.1:9000"}}}`
	if err := os.WriteFile(state, []byte(unrelated), 0600); err != nil {
		t.Fatal(err)
	}
	var registered atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/register" {
			var req map[string]any
			json.NewDecoder(r.Body).Decode(&req)
			registered.Add(1)
			fmt.Fprintf(w, `{"status":"registered","url":"https://%s.devrp.example.com","domain":"devrp.example.com","public_ip":"203.0.113.10"}`, req["id"])
			return
		}
		fmt.Fprint(w, `{"status":"ok"}`)
	}))
	defer api.Close()
	commands := []*exec.Cmd{}
	for _, name := range []string{"one", "two"} {
		cmd := exec.Command(bin, "-s", api.URL, "-i", name, "-p", "3035")
		cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "SERVE_STATE="+state, "SERVE_CRASH="+filepath.Join(dir, "crash"))
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		commands = append(commands, cmd)
		defer cmd.Process.Kill()
	}
	waitFor(t, func() bool { return registered.Load() == 2 }, "both publications")
	if err := os.WriteFile(filepath.Join(dir, "crash"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return registered.Load() >= 3 }, "Serve subprocess recovery")
	for i, cmd := range commands {
		_ = cmd.Process.Signal(os.Interrupt)
		if err := cmd.Wait(); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(state)
		if err != nil {
			t.Fatal("removed unrelated Serve config", err)
		}
		var config struct {
			TCP        map[string]any
			Foreground map[string]any
		}
		if err = json.Unmarshal(data, &config); err != nil {
			t.Fatal(err)
		}
		if config.TCP["24000"] == nil || len(config.Foreground) != 1-i {
			t.Fatalf("cleanup affected another session: %s", data)
		}
	}
}

func TestGatewayRejectionsFailBeforeChildLaunch(t *testing.T) {
	dir, bin := prepareCLI(t)
	for _, status := range []int{400, 403, 409, 422, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/unregister" {
					fmt.Fprint(w, `{"status":"ok"}`)
					return
				}
				w.WriteHeader(status)
				fmt.Fprint(w, `{"message":"registration rejected"}`)
			}))
			defer api.Close()
			marker := filepath.Join(t.TempDir(), "child")
			cmd := exec.Command(bin, "-s", api.URL, "--", "sh", "-c", `touch "$1"`, "sh", marker)
			cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "SERVE_STATE="+filepath.Join(dir, "state"))
			out, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(out), "registration rejected") {
				t.Fatalf("%v %s", err, out)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("rejected publication launched child")
			}
			if _, err := os.Stat(filepath.Join(dir, "state")); !os.IsNotExist(err) {
				t.Fatal("rejected publication left Serve running")
			}
		})
	}
}

func TestServeRecoveryRetriesFailedRemovalBeforeReplacingSession(t *testing.T) {
	dir, bin := prepareCLI(t)
	var offline atomic.Bool
	var registrations, failedRemovals atomic.Int32
	var mu sync.Mutex
	activeSession := ""
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/unregister" {
			if offline.Load() {
				failedRemovals.Add(1)
				w.WriteHeader(503)
				fmt.Fprint(w, `{"message":"temporary outage"}`)
				return
			}
			activeSession = ""
			fmt.Fprint(w, `{"status":"ok"}`)
			return
		}
		if r.URL.Path == "/register" {
			var req map[string]any
			json.NewDecoder(r.Body).Decode(&req)
			if activeSession != "" && activeSession != req["session"] {
				w.WriteHeader(409)
				fmt.Fprint(w, `{"message":"name in use"}`)
				return
			}
			activeSession = req["session"].(string)
			registrations.Add(1)
			fmt.Fprint(w, `{"status":"registered","url":"https://myapp.devrp.example.com","domain":"devrp.example.com","public_ip":"203.0.113.10"}`)
			return
		}
		fmt.Fprint(w, `{"status":"ok"}`)
	}))
	defer api.Close()
	cmd := exec.Command(bin, "-s", api.URL, "-p", "3035")
	cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "SERVE_STATE="+filepath.Join(dir, "state"), "SERVE_CRASH="+filepath.Join(dir, "crash"))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	waitFor(t, func() bool { return registrations.Load() == 1 }, "initial publication")
	offline.Store(true)
	os.WriteFile(filepath.Join(dir, "crash"), nil, 0600)
	waitFor(t, func() bool { return failedRemovals.Load() >= 2 }, "removal retry")
	offline.Store(false)
	waitFor(t, func() bool { return registrations.Load() == 2 }, "forwarder replacement without self-conflict")
	_ = cmd.Process.Signal(os.Interrupt)
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
}
