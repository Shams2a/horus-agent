// Package protocol décrit les échanges entre l'agent et Network
// (docs/protocole.md) : structures JSON et signature des requêtes.
package protocol

import "time"

// Chemins de l'API, relatifs à l'adresse de base (api_url).
const (
	PathPreview   = "/agent/v1/enroll/preview"
	PathEnroll    = "/agent/v1/enroll"
	PathReport    = "/agent/v1/report"
	PathUninstall = "/agent/v1/uninstall"
)

// Collecteurs v1 (§ 3.3).
const (
	CollectorSystem  = "system"
	CollectorSystemd = "systemd"
	CollectorDocker  = "docker"
)

// CapabilityRemoteUninstall : le service d'aide root est installé, la
// désinstallation peut être demandée depuis Network (§ 5, § 7 bis).
const CapabilityRemoteUninstall = "remote_uninstall"

// MaxSamplesPerReport borne un envoi (§ 4).
const MaxSamplesPerReport = 500

// --- Inscription (§ 3) ---------------------------------------------------

type PreviewRequest struct {
	Token       string `json:"token"`
	MachineHash string `json:"machine_hash"`
}

type Org struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type ProjectRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type ExistingAgent struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	LastSeenAt *time.Time `json:"last_seen_at"`
}

type PreviewResponse struct {
	Org                Org            `json:"org"`
	ExpiresAt          time.Time      `json:"expires_at"`
	PreselectedProject *ProjectRef    `json:"preselected_project"`
	Projects           []ProjectRef   `json:"projects"`
	ExistingAgent      *ExistingAgent `json:"existing_agent"`
}

type EnrollOptions struct {
	MultiProject      bool `json:"multi_project"`
	AllowRemoteUpdate bool `json:"allow_remote_update"`
}

type EnrollRequest struct {
	Token          string        `json:"token"`
	PublicKey      string        `json:"public_key"`
	MachineHash    string        `json:"machine_hash"`
	AgentVersion   string        `json:"agent_version"`
	Facts          Facts         `json:"facts"`
	Collectors     []string      `json:"collectors"`
	Options        EnrollOptions `json:"options"`
	ProjectID      string        `json:"project_id,omitempty"`
	ReplaceAgentID string        `json:"replace_agent_id,omitempty"`
}

type EnrollResponse struct {
	AgentID         string `json:"agent_id"`
	Org             Org    `json:"org"`
	ReportIntervalS int    `json:"report_interval_s"`
	AgentURL        string `json:"agent_url"`
}

// --- Envoi des mesures (§ 4) ---------------------------------------------

type ReportRequest struct {
	AgentVersion string    `json:"agent_version"`
	SentAt       time.Time `json:"sent_at"`
	Facts        *Facts    `json:"facts,omitempty"`
	Samples      []Sample  `json:"samples"`
}

type UpdateOrder struct {
	Version string `json:"version"`
}

type ReportResponse struct {
	ServerTime      time.Time    `json:"server_time"`
	ReportIntervalS int          `json:"report_interval_s"`
	Update          *UpdateOrder `json:"update"`
	Uninstall       bool         `json:"uninstall"`
}

// ErrorBody est le format d'erreur de toutes les routes (§ 1).
type ErrorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		// ServerTimeMS accompagne clock_skew (0.3.1) : l'heure de Network,
		// pour que l'agent corrige ses horodatages.
		ServerTimeMS int64 `json:"server_time_ms,omitempty"`
	} `json:"error"`
}

// CodeClockSkew : horloge de la machine trop éloignée de celle de Network.
const CodeClockSkew = "clock_skew"

// --- Fiche de la machine (§ 5) -------------------------------------------

type OSInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

type CPUInfo struct {
	Model string `json:"model"`
	Cores int    `json:"cores"`
}

type Interface struct {
	Name      string   `json:"name"`
	MAC       string   `json:"mac"`
	Addresses []string `json:"addresses"`
}

type DiskInfo struct {
	Mount      string `json:"mount"`
	FS         string `json:"fs"`
	TotalBytes uint64 `json:"total_bytes"`
}

type Facts struct {
	Hostname          string      `json:"hostname"`
	OS                OSInfo      `json:"os"`
	Kernel            string      `json:"kernel"`
	Arch              string      `json:"arch"`
	CPU               CPUInfo     `json:"cpu"`
	MemoryTotalBytes  uint64      `json:"memory_total_bytes"`
	Virtualization    string      `json:"virtualization"`
	BootTime          *time.Time  `json:"boot_time,omitempty"`
	Interfaces        []Interface `json:"interfaces"`
	Disks             []DiskInfo  `json:"disks"`
	Collectors        []string    `json:"collectors"`
	AllowRemoteUpdate bool        `json:"allow_remote_update"`
	Capabilities      []string    `json:"capabilities,omitempty"`
	// Ce que « install.sh --repair » corrigerait (0.3.0) : liste vide si
	// rien. Absent à l'inscription et chez les agents plus anciens.
	Repair []string `json:"repair"`
	// ClockOffsetS : écart corrigé entre l'horloge de la machine et celle de
	// Network, en secondes (0.3.1). Absent si aucun écart n'a été corrigé.
	ClockOffsetS int64 `json:"clock_offset_s,omitempty"`
}

// Codes de Facts.Repair.
const (
	RepairBinaryWritable      = "binary_writable"
	RepairHelperMissing       = "helper_missing"
	RepairLXCUnitMissing      = "lxc_unit_missing"
	RepairDockerGroup         = "docker_group"
	RepairDockerReaderMissing = "docker_reader_missing"
)

// --- Mesures (§ 6) --------------------------------------------------------

type Target struct {
	Kind    string `json:"kind"`
	Address string `json:"address,omitempty"`
}

// TargetSelf est la seule cible de la v1 : la machine de l'agent.
var TargetSelf = Target{Kind: "self"}

type Sample struct {
	Target  Target    `json:"target"`
	TS      time.Time `json:"ts"`
	Metrics Metrics   `json:"metrics"`
}

type CPU struct {
	UsagePct float64 `json:"usage_pct"`
	Load1    float64 `json:"load1"`
	Load5    float64 `json:"load5"`
	Load15   float64 `json:"load15"`
}

type Memory struct {
	UsedBytes      uint64 `json:"used_bytes"`
	TotalBytes     uint64 `json:"total_bytes"`
	SwapUsedBytes  uint64 `json:"swap_used_bytes"`
	SwapTotalBytes uint64 `json:"swap_total_bytes"`
}

type DiskUsage struct {
	Mount      string `json:"mount"`
	UsedBytes  uint64 `json:"used_bytes"`
	TotalBytes uint64 `json:"total_bytes"`
	// Espace libre pour un compte autre que root, sans la réserve d'ext4
	// (0.2.3). Absent des agents plus anciens.
	AvailBytes *uint64 `json:"avail_bytes,omitempty"`
}

type NetRate struct {
	Iface string `json:"iface"`
	RxBps uint64 `json:"rx_bps"`
	TxBps uint64 `json:"tx_bps"`
}

type Systemd struct {
	// Toujours sérialisée, même vide : une liste vide veut dire « aucune
	// unité en échec », un bloc absent « non mesuré ».
	FailedUnits []string `json:"failed_units"`
}

type Container struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Image        string `json:"image"`
	State        string `json:"state"`
	RestartCount int    `json:"restart_count"`
}

type Docker struct {
	Containers []Container `json:"containers,omitempty"`
	Error      string      `json:"error,omitempty"`
}

// Metrics : chaque bloc n'est présent que si son collecteur est actif.
type Metrics struct {
	CPU     *CPU        `json:"cpu,omitempty"`
	Memory  *Memory     `json:"memory,omitempty"`
	Disks   []DiskUsage `json:"disks,omitempty"`
	Network []NetRate   `json:"network,omitempty"`
	Systemd *Systemd    `json:"systemd,omitempty"`
	Docker  *Docker     `json:"docker,omitempty"`
}
