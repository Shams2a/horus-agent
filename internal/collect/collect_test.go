package collect

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Shams2a/horus-agent/internal/protocol"
)

// fakeRoot écrit une arborescence /proc et /etc de test.
func fakeRoot(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const mounts = `/dev/sda1 / ext4 rw,relatime 0 0
proc /proc proc rw 0 0
tmpfs /run tmpfs rw 0 0
/dev/sda1 /var/lib/docker ext4 rw,bind 0 0
/dev/sdc1 /etc/hosts ext4 rw 0 0
/dev/sdb1 /srv/mes\040données xfs rw 0 0
overlay /var/lib/docker/overlay2/x/merged overlay rw 0 0
server:/export /mnt/nfs nfs4 rw 0 0
`

func newTestCollector(t *testing.T, files map[string]string) (*Collector, *time.Time) {
	now := time.Date(2026, 11, 3, 18, 0, 0, 0, time.UTC)
	c := New()
	c.Root = fakeRoot(t, files)
	c.Now = func() time.Time { return now }
	c.Statfs = func(path string) (uint64, uint64, error) {
		return 100 << 30, 25 << 30, nil
	}
	// /etc/hosts joue le fichier monté seul, comme Docker le fait.
	c.IsDir = func(path string) bool { return path != "/etc/hosts" }
	c.Exec = func(_ context.Context, name string, _ ...string) ([]byte, error) {
		switch name {
		case "systemctl":
			return []byte("nginx.service loaded failed failed A high performance web server\nbackup.timer loaded failed failed Backup\n"), nil
		case "systemd-detect-virt":
			return []byte("lxc\n"), nil
		}
		return nil, os.ErrNotExist
	}
	return c, &now
}

func TestMountsKeepRealFilesystemsOnce(t *testing.T) {
	c, _ := newTestCollector(t, map[string]string{"proc/self/mounts": mounts})
	got := c.mounts()
	if len(got) != 2 {
		t.Fatalf("montages = %+v", got)
	}
	if got[0].point != "/" || got[1].point != "/srv/mes données" || got[1].fs != "xfs" {
		t.Errorf("montages = %+v", got)
	}
}

func TestSystemSample(t *testing.T) {
	c, now := newTestCollector(t, map[string]string{
		"proc/stat":        "cpu  100 0 100 700 100 0 0 0 0 0\nbtime 1761631920\n",
		"proc/loadavg":     "0.42 0.35 0.30 1/200 1234\n",
		"proc/meminfo":     "MemTotal:       8000000 kB\nMemAvailable:   6000000 kB\nSwapTotal:      1000000 kB\nSwapFree:        750000 kB\n",
		"proc/net/dev":     "Inter-| Receive\n face |bytes packets errs drop fifo frame compressed multicast|bytes\n    lo: 999 0 0 0 0 0 0 0 999 0 0 0 0 0 0 0\n  eth0: 1000 0 0 0 0 0 0 0 2000 0 0 0 0 0 0 0\nveth12: 5 0 0 0 0 0 0 0 5 0 0 0 0 0 0 0\n",
		"proc/self/mounts": mounts,
	})
	c.Prime()

	// 60 s plus tard : 500 jiffies de plus dont 250 inactifs, 60 000 octets reçus.
	*now = now.Add(60 * time.Second)
	writeFile(t, c.Root, "proc/stat", "cpu  300 0 150 900 150 0 0 0 0 0\n")
	writeFile(t, c.Root, "proc/net/dev", "  eth0: 61000 0 0 0 0 0 0 0 32000 0 0 0 0 0 0 0\n")

	s := c.Sample(context.Background(), []string{protocol.CollectorSystem, protocol.CollectorSystemd})
	m := s.Metrics
	if m.CPU.UsagePct != 50 || m.CPU.Load1 != 0.42 {
		t.Errorf("cpu = %+v", m.CPU)
	}
	if m.Memory.UsedBytes != 2000000*1024 || m.Memory.SwapUsedBytes != 250000*1024 {
		t.Errorf("mémoire = %+v", m.Memory)
	}
	if len(m.Disks) != 2 || m.Disks[0].UsedBytes != 75<<30 {
		t.Errorf("disques = %+v", m.Disks)
	}
	if len(m.Network) != 1 || m.Network[0] != (protocol.NetRate{Iface: "eth0", RxBps: 1000, TxBps: 500}) {
		t.Errorf("réseau = %+v", m.Network)
	}
	if got := m.Systemd.FailedUnits; len(got) != 2 || got[0] != "nginx.service" {
		t.Errorf("systemd = %+v", got)
	}
	if m.Docker != nil {
		t.Error("docker ne doit pas être mesuré s'il n'est pas coché")
	}
	if s.Target.Kind != "self" {
		t.Errorf("cible = %+v", s.Target)
	}
}

func TestSystemdAbsentMeansNotMeasured(t *testing.T) {
	c, _ := newTestCollector(t, nil)
	c.Exec = func(context.Context, string, ...string) ([]byte, error) { return nil, os.ErrNotExist }
	if got := c.Sample(context.Background(), []string{protocol.CollectorSystemd}).Metrics.Systemd; got != nil {
		t.Errorf("systemd = %+v, attendu nil", got)
	}
	raw, _ := json.Marshal(&protocol.Systemd{FailedUnits: parseFailedUnits(nil)})
	if string(raw) != `{"failed_units":[]}` {
		t.Errorf("liste vide sérialisée en %s", raw)
	}
}

func TestFacts(t *testing.T) {
	c, _ := newTestCollector(t, map[string]string{
		"etc/os-release":            "NAME=\"Debian GNU/Linux\"\nID=debian\nVERSION_ID=\"12\"\n",
		"proc/sys/kernel/osrelease": "6.1.0-26-amd64\n",
		"proc/cpuinfo":              "processor\t: 0\nmodel name\t: Intel(R) Xeon(R) E-2236\n",
		"proc/meminfo":              "MemTotal: 1024 kB\n",
		"proc/stat":                 "cpu 1 1 1 1\nbtime 1761631920\n",
		"proc/self/mounts":          mounts,
	})
	f := c.Facts(context.Background())
	if f.OS != (protocol.OSInfo{ID: "debian", Name: "Debian GNU/Linux", Version: "12"}) {
		t.Errorf("os = %+v", f.OS)
	}
	if f.Kernel != "6.1.0-26-amd64" || f.CPU.Model != "Intel(R) Xeon(R) E-2236" || f.MemoryTotalBytes != 1024*1024 {
		t.Errorf("fiche = %+v", f)
	}
	if f.Virtualization != "lxc" {
		t.Errorf("virtualisation = %s", f.Virtualization)
	}
	if f.BootTime == nil || f.BootTime.Unix() != 1761631920 {
		t.Errorf("démarrage = %v", f.BootTime)
	}
	if len(f.Disks) != 2 || f.Disks[1].FS != "xfs" {
		t.Errorf("disques = %+v", f.Disks)
	}
}

func TestClassifyVirt(t *testing.T) {
	cases := map[string]string{"none": "physical", "kvm": "vm", "vmware": "vm", "lxc": "lxc", "docker": "container", "": ""}
	for in, want := range cases {
		if got := classifyVirt(in); got != want {
			t.Errorf("classifyVirt(%q) = %q, attendu %q", in, got, want)
		}
	}
}

func TestDocker(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "docker.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skip("sockets unix indisponibles :", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/containers/json", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("all") != "1" {
			t.Error("all=1 attendu")
		}
		w.Write([]byte(`[{"Id":"4f1c2a9e0b7d1234","Names":["/api"],"Image":"ghcr.io/acme/api:1.2","State":"restarting"}]`))
	})
	mux.HandleFunc("/containers/4f1c2a9e0b7d1234/json", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"RestartCount":3}`))
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	defer srv.Close()

	c, _ := newTestCollector(t, nil)
	c.DockerSocket = sock
	d := c.Sample(context.Background(), []string{protocol.CollectorDocker}).Metrics.Docker
	want := protocol.Container{ID: "4f1c2a9e0b7d", Name: "api", Image: "ghcr.io/acme/api:1.2", State: "restarting", RestartCount: 3}
	if d == nil || len(d.Containers) != 1 || d.Containers[0] != want {
		t.Fatalf("docker = %+v", d)
	}

	c.DockerSocket = filepath.Join(t.TempDir(), "absent.sock")
	if d := c.Sample(context.Background(), []string{protocol.CollectorDocker}).Metrics.Docker; d.Error != "socket_unavailable" {
		t.Errorf("docker = %+v", d)
	}
}

func writeFile(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
