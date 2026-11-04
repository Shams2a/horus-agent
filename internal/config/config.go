// Package config lit et écrit la configuration de l'agent (docs/protocole.md § 8).
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

// Emplacements sur la machine.
const (
	ConfigPath  = "/etc/horus-agent/config.json"
	KeyPath     = "/var/lib/horus-agent/agent.key"
	StateDir    = "/var/lib/horus-agent"
	InstallDir  = "/opt/horus-agent"
	BinaryPath  = "/opt/horus-agent/bin/horus-agent"
	UnitPath    = "/etc/systemd/system/horus-agent.service"
	ServiceName = "horus-agent"
	UserName    = "horus-agent"
	MachineID   = "/etc/machine-id"

	// Lien pour les commandes d'admin (sudo horus-agent …).
	AdminLink = "/usr/local/bin/horus-agent"

	// Demandes de l'agent au service d'aide root (§ 7 et § 7 bis). Le
	// dossier appartient à l'agent, le service d'aide revérifie tout.
	RequestsDir      = "/var/lib/horus-agent/requests"
	UpdateRequestDir = "/var/lib/horus-agent/requests/update"
	UninstallRequest = "/var/lib/horus-agent/requests/uninstall"

	HelperPathUnit    = "/etc/systemd/system/horus-agent-helper.path"
	HelperServiceUnit = "/etc/systemd/system/horus-agent-helper.service"
	HelperPathName    = "horus-agent-helper.path"

	// Complément de l'unité, écrit seulement dans un conteneur LXC.
	UnitDropInDir = "/etc/systemd/system/horus-agent.service.d"
	LXCDropIn     = "/etc/systemd/system/horus-agent.service.d/lxc.conf"
)

// DefaultDownloadBaseURL : Releases GitHub. Écrite dans la configuration
// pour pouvoir changer de source plus tard sans toucher au binaire.
const DefaultDownloadBaseURL = "https://github.com/Shams2a/horus-agent/releases/download"

type Config struct {
	APIURL            string   `json:"api_url"`
	DownloadBaseURL   string   `json:"download_base_url"`
	AgentID           string   `json:"agent_id"`
	OrgName           string   `json:"org_name,omitempty"`
	Collectors        []string `json:"collectors"`
	MultiProject      bool     `json:"multi_project"`
	AllowRemoteUpdate bool     `json:"allow_remote_update"`
}

// Has indique si un collecteur est actif.
func (c *Config) Has(collector string) bool {
	return slices.Contains(c.Collectors, collector)
}

// Load lit la configuration. Une configuration incomplète est une erreur.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("%s illisible : %w", path, err)
	}
	if cfg.APIURL == "" || cfg.AgentID == "" {
		return nil, errors.New(path + " incomplet : api_url ou agent_id manquant")
	}
	if cfg.DownloadBaseURL == "" {
		cfg.DownloadBaseURL = DefaultDownloadBaseURL
	}
	return &cfg, nil
}

// Save écrit la configuration de façon atomique, lisible par le groupe de l'agent.
func Save(path string, cfg *Config, gid int) error {
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o640); err != nil {
		return err
	}
	if gid >= 0 {
		if err := os.Chown(tmp, 0, gid); err != nil {
			return err
		}
	}
	return os.Rename(tmp, path)
}
