package setup

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"

	"github.com/Shams2a/horus-agent/internal/config"
)

//go:embed horus-agent.service
var unitFile []byte

func requireRoot() error {
	if os.Geteuid() != 0 {
		return errors.New("cette commande doit être lancée en root (sudo)")
	}
	return nil
}

func hasSystemd() bool {
	_, err := os.Stat("/run/systemd/system")
	return err == nil
}

func run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s : %v (%s)", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// agentAccount renvoie l'uid et le gid du compte horus-agent, en le créant au besoin.
func agentAccount() (int, int, error) {
	u, err := user.Lookup(config.UserName)
	if err != nil {
		shell := "/usr/sbin/nologin"
		if _, statErr := os.Stat(shell); statErr != nil {
			shell = "/sbin/nologin"
		}
		if err := run("useradd", "--system", "--user-group", "--no-create-home",
			"--home-dir", config.StateDir, "--shell", shell, config.UserName); err != nil {
			return 0, 0, err
		}
		if u, err = user.Lookup(config.UserName); err != nil {
			return 0, 0, err
		}
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	return uid, gid, nil
}

// setDockerAccess ajoute ou retire le compte de l'agent du groupe docker.
func setDockerAccess(enabled bool) error {
	if _, err := user.LookupGroup("docker"); err != nil {
		if enabled {
			return errors.New("groupe docker introuvable : Docker est-il installé ?")
		}
		return nil
	}
	if enabled {
		return run("usermod", "-aG", "docker", config.UserName)
	}
	u, err := user.Lookup(config.UserName)
	if err != nil {
		return nil
	}
	groups, _ := u.GroupIds()
	docker, _ := user.LookupGroup("docker")
	for _, g := range groups {
		if g == docker.Gid {
			return run("gpasswd", "-d", config.UserName, "docker")
		}
	}
	return nil
}

func installService() error {
	if err := os.WriteFile(config.UnitPath, unitFile, 0o644); err != nil {
		return err
	}
	if err := run("systemctl", "daemon-reload"); err != nil {
		return err
	}
	return run("systemctl", "enable", "--now", config.ServiceName)
}

func stopService() {
	if hasSystemd() {
		_ = run("systemctl", "stop", config.ServiceName)
	}
}

func restartService() error {
	if !hasSystemd() {
		return nil
	}
	return run("systemctl", "restart", config.ServiceName)
}

func serviceState() string {
	out, _ := exec.Command("systemctl", "is-active", config.ServiceName).Output()
	if s := strings.TrimSpace(string(out)); s != "" {
		return s
	}
	return "inconnu"
}
