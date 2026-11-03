package collect

import (
	"context"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Shams2a/horus-agent/internal/protocol"
)

// Facts relève la fiche de la machine (§ 5). Collectors et AllowRemoteUpdate
// viennent de la configuration et sont ajoutés par l'appelant.
func (c *Collector) Facts(ctx context.Context) protocol.Facts {
	f := protocol.Facts{
		OS:               c.osInfo(),
		Kernel:           strings.TrimSpace(string(must(c.read("proc/sys/kernel/osrelease")))),
		Arch:             runtime.GOARCH,
		CPU:              protocol.CPUInfo{Model: c.cpuModel(), Cores: runtime.NumCPU()},
		MemoryTotalBytes: c.meminfo()["MemTotal"],
		Virtualization:   c.Virtualization(ctx),
		BootTime:         c.bootTime(),
		Interfaces:       interfaces(),
		Disks:            []protocol.DiskInfo{},
	}
	f.Hostname, _ = os.Hostname()
	for _, m := range c.mounts() {
		if total, _, err := c.Statfs(m.point); err == nil && total > 0 {
			f.Disks = append(f.Disks, protocol.DiskInfo{Mount: m.point, FS: m.fs, TotalBytes: total})
		}
	}
	return f
}

func must(b []byte, _ error) []byte { return b }

func (c *Collector) osInfo() protocol.OSInfo {
	raw, err := c.read("etc/os-release")
	if err != nil {
		raw, _ = c.read("usr/lib/os-release")
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		if unquoted, err := strconv.Unquote(value); err == nil {
			value = unquoted
		} else {
			value = strings.Trim(value, `'"`)
		}
		values[key] = value
	}
	name := values["NAME"]
	if name == "" {
		name = values["PRETTY_NAME"]
	}
	return protocol.OSInfo{ID: values["ID"], Name: name, Version: values["VERSION_ID"]}
}

func (c *Collector) cpuModel() string {
	raw, _ := c.read("proc/cpuinfo")
	for _, key := range []string{"model name", "Model", "Hardware", "cpu model"} {
		for _, line := range strings.Split(string(raw), "\n") {
			k, v, ok := strings.Cut(line, ":")
			if ok && strings.TrimSpace(k) == key {
				return strings.TrimSpace(v)
			}
		}
	}
	return ""
}

func (c *Collector) bootTime() *time.Time {
	raw, err := c.read("proc/stat")
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if v, ok := strings.CutPrefix(line, "btime "); ok {
			if sec, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
				t := time.Unix(sec, 0).UTC()
				return &t
			}
		}
	}
	return nil
}

// Virtualization renvoie physical, vm, lxc, container ou unknown. Le résultat
// est gardé : il ne change pas tant que la machine tourne.
func (c *Collector) Virtualization(ctx context.Context) string {
	if c.virt == "" {
		c.virt = c.detectVirtualization(ctx)
	}
	return c.virt
}

func (c *Collector) detectVirtualization(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// systemd-detect-virt sort en erreur quand il ne détecte rien : on lit la sortie quand même.
	out, _ := c.Exec(ctx, "systemd-detect-virt")
	if kind := classifyVirt(strings.TrimSpace(string(out))); kind != "" {
		return kind
	}
	if env, err := c.read("proc/1/environ"); err == nil && strings.Contains(string(env), "container=lxc") {
		return "lxc"
	}
	if _, err := os.Stat(c.path(".dockerenv")); err == nil {
		return "container"
	}
	if raw, err := c.read("proc/cpuinfo"); err == nil && strings.Contains(string(raw), " hypervisor") {
		return "vm"
	}
	return "unknown"
}

func classifyVirt(v string) string {
	switch v {
	case "":
		return ""
	case "none":
		return "physical"
	case "lxc", "lxc-libvirt":
		return "lxc"
	case "docker", "podman", "rkt", "systemd-nspawn", "openvz", "container-other", "proot", "pouch":
		return "container"
	case "wsl":
		// WSL 2 tourne dans une vraie machine virtuelle Hyper-V.
		return "vm"
	}
	return "vm"
}

func interfaces() []protocol.Interface {
	out := []protocol.Interface{}
	ifaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 || ignoredInterface(iface.Name) {
			continue
		}
		item := protocol.Interface{Name: iface.Name, MAC: iface.HardwareAddr.String(), Addresses: []string{}}
		if addrs, err := iface.Addrs(); err == nil {
			for _, a := range addrs {
				item.Addresses = append(item.Addresses, a.String())
			}
		}
		out = append(out, item)
	}
	return out
}
