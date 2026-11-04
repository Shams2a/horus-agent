// Package collect relève la fiche de la machine et ses mesures, sous Linux
// (docs/protocole.md § 5 et § 6). Tout se lit dans /proc, /etc et /sys, sans
// droits root.
package collect

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Shams2a/horus-agent/internal/protocol"
)

// Collector garde l'état nécessaire aux moyennes (processeur, débits).
// Root, Statfs, IsDir, Exec et Now sont remplaçables en test.
type Collector struct {
	Root         string
	DockerSocket string
	// DockerReader : socket du service root de lecture Docker (0.3.0).
	DockerReader string
	Statfs       func(path string) (total, free, avail uint64, err error)
	// IsDir écarte les fichiers montés un par un (/etc/resolv.conf dans un
	// conteneur Docker), qui ne sont pas des disques.
	IsDir func(path string) bool
	Exec  func(ctx context.Context, name string, args ...string) ([]byte, error)
	Now   func() time.Time

	prevCPU   *cpuTimes
	prevNet   map[string]netCounters
	prevNetAt time.Time
	virt      string
}

func New() *Collector {
	return &Collector{
		Root:         "/",
		DockerSocket: "/var/run/docker.sock",
		DockerReader: "/run/horus-agent/docker.sock",
		Statfs:       statfs,
		IsDir: func(path string) bool {
			info, err := os.Stat(path)
			return err == nil && info.IsDir()
		},
		Exec: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, args...).Output()
		},
		Now: time.Now,
	}
}

func (c *Collector) path(p string) string {
	return filepath.Join(c.Root, p)
}

func (c *Collector) read(p string) ([]byte, error) {
	return os.ReadFile(c.path(p))
}

// statfs : taille, espace libre, et espace libre pour un compte autre que
// root (sans la réserve d'ext4), comme df.
func statfs(path string) (uint64, uint64, uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, 0, err
	}
	bsize := uint64(st.Bsize)
	return st.Blocks * bsize, st.Bfree * bsize, st.Bavail * bsize, nil
}

// Prime fait une première lecture des compteurs, pour que la première mesure
// ait déjà un intervalle de référence.
func (c *Collector) Prime() {
	if t, err := c.readCPU(); err == nil {
		c.prevCPU = &t
	}
	if n, err := c.readNet(); err == nil {
		c.prevNet, c.prevNetAt = n, c.Now()
	}
}

// Sample relève les mesures des collecteurs actifs.
func (c *Collector) Sample(ctx context.Context, collectors []string) protocol.Sample {
	s := protocol.Sample{Target: protocol.TargetSelf, TS: c.Now().UTC().Truncate(time.Second)}
	for _, name := range collectors {
		switch name {
		case protocol.CollectorSystem:
			s.Metrics.CPU = c.cpu()
			s.Metrics.Memory = c.memory()
			s.Metrics.Disks = c.diskUsage()
			s.Metrics.Network = c.network()
		case protocol.CollectorSystemd:
			s.Metrics.Systemd = c.systemd(ctx)
		case protocol.CollectorDocker:
			s.Metrics.Docker = c.docker(ctx)
		}
	}
	return s
}
