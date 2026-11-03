package collect

import (
	"sort"
	"strings"

	"github.com/Shams2a/horus-agent/internal/protocol"
)

// Pseudo-systèmes de fichiers et systèmes réseau exclus des disques (§ 5).
// Les systèmes réseau sont exclus car statfs peut s'y bloquer.
var ignoredFS = map[string]bool{
	"autofs": true, "binfmt_misc": true, "bpf": true, "cgroup": true, "cgroup2": true,
	"configfs": true, "debugfs": true, "devpts": true, "devtmpfs": true, "efivarfs": true,
	"fusectl": true, "fuse.lxcfs": true, "hugetlbfs": true, "mqueue": true, "nsfs": true,
	"overlay": true, "proc": true, "pstore": true, "ramfs": true, "rpc_pipefs": true,
	"securityfs": true, "squashfs": true, "sysfs": true, "tmpfs": true, "tracefs": true,
	"nfs": true, "nfs4": true, "cifs": true, "smb3": true, "fuse.sshfs": true,
	"9p": true, "iso9660": true, "selinuxfs": true,
}

// ignoredInterface : boucle locale et interfaces virtuelles de conteneurs.
func ignoredInterface(name string) bool {
	if name == "lo" || name == "docker0" {
		return true
	}
	for _, prefix := range []string{"veth", "br-", "virbr", "cni", "flannel", "cali", "kube-"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

type mount struct {
	device, point, fs string
}

// parseMounts lit /proc/self/mounts : une entrée par périphérique réel, dans
// l'ordre. Les montages liés d'un même périphérique sont ignorés.
func parseMounts(raw []byte) []mount {
	var out []mount
	seen := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || ignoredFS[f[2]] || seen[f[0]] {
			continue
		}
		seen[f[0]] = true
		out = append(out, mount{device: f[0], point: unescapeMount(f[1]), fs: f[2]})
	}
	return out
}

// unescapeMount décode les caractères encodés en octal par le noyau (\040 pour une espace).
func unescapeMount(s string) string {
	return strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`).Replace(s)
}

func sortRates(rates []protocol.NetRate) {
	sort.Slice(rates, func(i, j int) bool { return rates[i].Iface < rates[j].Iface })
}
