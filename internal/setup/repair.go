package setup

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/user"
	"strconv"
	"syscall"

	"github.com/Shams2a/horus-agent/internal/collect"
	"github.com/Shams2a/horus-agent/internal/config"
	"github.com/Shams2a/horus-agent/internal/protocol"
)

// RepairNeeded liste ce que « install.sh --repair » corrigerait (codes
// protocol.Repair*). Lancé par l'agent, sans droits root : il ne fait que
// regarder. Network affiche « À réparer » quand la liste n'est pas vide.
func RepairNeeded(cfg *config.Config) []string {
	out := []string{}
	if info, err := os.Stat(config.BinaryPath); err == nil {
		if st, ok := info.Sys().(*syscall.Stat_t); ok && st.Uid != 0 {
			out = append(out, protocol.RepairBinaryWritable)
		}
	}
	if !HelperInstalled() {
		out = append(out, protocol.RepairHelperMissing)
	}
	if collect.New().Virtualization(context.Background()) == "lxc" && !present(config.LXCDropIn) {
		out = append(out, protocol.RepairLXCUnitMissing)
	}
	if inGroup("docker") {
		out = append(out, protocol.RepairDockerGroup)
	}
	if cfg.Has(protocol.CollectorDocker) && !present(config.DockerSocketUnit) {
		out = append(out, protocol.RepairDockerReaderMissing)
	}
	return out
}

func present(name string) bool {
	_, err := os.Stat(name)
	return err == nil || !errors.Is(err, fs.ErrNotExist)
}

// inGroup : le processus courant a-t-il ce groupe parmi ses groupes ?
func inGroup(name string) bool {
	g, err := user.LookupGroup(name)
	if err != nil {
		return false
	}
	gids, err := os.Getgroups()
	if err != nil {
		return false
	}
	for _, gid := range gids {
		if strconv.Itoa(gid) == g.Gid {
			return true
		}
	}
	return false
}
