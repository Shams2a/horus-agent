// Package setup installe, reconfigure et désinstalle l'agent sur la machine.
package setup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/Shams2a/horus-agent/internal/client"
	"github.com/Shams2a/horus-agent/internal/collect"
	"github.com/Shams2a/horus-agent/internal/config"
	"github.com/Shams2a/horus-agent/internal/identity"
	"github.com/Shams2a/horus-agent/internal/protocol"
	"github.com/Shams2a/horus-agent/internal/version"
)

const dockerWarning = "Ajoute le compte horus-agent au groupe docker, ce qui revient à lui donner des droits administrateur sur la machine."

// Options de `horus-agent setup`. Un pointeur nil veut dire « demander »
// (ou la valeur par défaut avec --yes).
type Options struct {
	Token           string
	APIURL          string
	DownloadBaseURL string
	Metrics         []string
	Project         string
	MultiProject    *bool
	RemoteUpdate    *bool
	Replace         *bool
	Yes             bool
	NoService       bool
	Out             io.Writer
}

func defaultCollectors() []string {
	out := []string{protocol.CollectorSystem}
	if _, err := exec.LookPath("systemctl"); err == nil {
		out = append(out, protocol.CollectorSystemd)
	}
	return out
}

func collectorOptions(selected []string) []Option {
	return []Option{
		{Label: "Système : processeur, mémoire, disques, réseau", Checked: slices.Contains(selected, protocol.CollectorSystem)},
		{Label: "Services systemd en échec", Checked: slices.Contains(selected, protocol.CollectorSystemd)},
		{Label: "Conteneurs Docker", Detail: dockerWarning, Checked: slices.Contains(selected, protocol.CollectorDocker)},
	}
}

func checkedCollectors(options []Option) []string {
	names := []string{protocol.CollectorSystem, protocol.CollectorSystemd, protocol.CollectorDocker}
	var out []string
	for i, o := range options {
		if o.Checked {
			out = append(out, names[i])
		}
	}
	return out
}

// ParseMetrics lit la valeur de --metrics (« system,systemd »).
func ParseMetrics(value string) ([]string, error) {
	out := []string{}
	for _, m := range strings.Split(value, ",") {
		m = strings.TrimSpace(m)
		switch m {
		case "":
		case protocol.CollectorSystem, protocol.CollectorSystemd, protocol.CollectorDocker:
			if !slices.Contains(out, m) {
				out = append(out, m)
			}
		default:
			return nil, fmt.Errorf("collecteur inconnu : %s (system, systemd ou docker)", m)
		}
	}
	return out, nil
}

func yesNo(v bool) string {
	if v {
		return "oui"
	}
	return "non"
}

// Setup inscrit la machine et démarre le service.
func Setup(ctx context.Context, opts Options) error {
	if err := requireRoot(); err != nil {
		return err
	}
	if opts.Token == "" || opts.APIURL == "" {
		return errors.New("--token et --api-url sont obligatoires : copiez la commande depuis l'admin Network")
	}
	if !opts.NoService && !hasSystemd() {
		return errors.New("systemd est introuvable : l'agent v1 a besoin de systemd (ou relancez avec --no-service)")
	}
	if opts.DownloadBaseURL == "" {
		opts.DownloadBaseURL = config.DefaultDownloadBaseURL
	}

	var p *Prompter
	if opts.Yes {
		p = NewPrompter(strings.NewReader(""), opts.Out)
	} else {
		tty, closeTTY, err := OpenTTY()
		if err != nil {
			return err
		}
		defer closeTTY()
		p = tty
	}

	machineHash, err := identity.MachineHash(config.MachineID)
	if err != nil {
		return err
	}
	api := client.New(opts.APIURL)
	preview, err := api.Preview(ctx, protocol.PreviewRequest{Token: opts.Token, MachineHash: machineHash})
	if err != nil {
		return explainEnrollError(err)
	}
	org := preview.Org.Name
	if org == "" {
		org = preview.Org.ID
	}
	p.Printf("\nOrganisation : %s\nJeton valable jusqu'à %s\n\n", org, preview.ExpiresAt.Local().Format("15:04"))

	// Réinstallation : on propose de reprendre l'agent pour garder son placement.
	replaceID := ""
	if existing := preview.ExistingAgent; existing != nil {
		p.Printf("Cette machine est déjà inscrite sous « %s ».\n", existing.Name)
		p.Printf("Si c'est une VM clonée depuis une autre, répondez non, régénérez /etc/machine-id, puis relancez.\n")
		replace := true
		if opts.Replace != nil {
			replace = *opts.Replace
		} else if !opts.Yes {
			if replace, err = p.Confirm("Reprendre cet agent et garder son placement sur les canvas ?", true); err != nil {
				return err
			}
		}
		if replace {
			replaceID = existing.ID
		}
		p.Printf("\n")
	}

	collectors := opts.Metrics
	if collectors == nil {
		collectors = defaultCollectors()
		if !opts.Yes {
			chosen, err := p.Checklist("Que voulez-vous surveiller ?", collectorOptions(collectors))
			if err != nil {
				return err
			}
			collectors = checkedCollectors(chosen)
			p.Printf("\n")
		}
	}

	projectID, projectName, err := chooseProject(p, preview, opts)
	if err != nil {
		return err
	}

	multi := false
	if opts.MultiProject != nil {
		multi = *opts.MultiProject
	} else if !opts.Yes {
		if multi, err = p.Confirm("Pourra-t-on placer cette machine dans plusieurs projets ?", false); err != nil {
			return err
		}
	}
	remote := true
	if opts.RemoteUpdate != nil {
		remote = *opts.RemoteUpdate
	} else if !opts.Yes {
		if remote, err = p.Confirm("Autoriser les mises à jour lancées depuis Network ?", true); err != nil {
			return err
		}
	}

	p.Printf("\nRécapitulatif\n")
	p.Printf("  Organisation          %s\n", org)
	p.Printf("  Surveillance          %s\n", strings.Join(collectors, ", "))
	p.Printf("  Projet                %s\n", projectName)
	p.Printf("  Multi-projets         %s\n", yesNo(multi))
	p.Printf("  Mises à jour à distance %s\n", yesNo(remote))
	if !opts.Yes {
		ok, err := p.Confirm("Installer l'agent avec ces réglages ?", true)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("installation annulée")
		}
	}

	uid, gid, err := agentAccount()
	if err != nil {
		return err
	}
	stopService()

	key, err := identity.Generate()
	if err != nil {
		return err
	}
	collector := collect.New()
	facts := collector.Facts(ctx)
	facts.Collectors = collectors
	facts.AllowRemoteUpdate = remote

	enrolled, err := api.Enroll(ctx, protocol.EnrollRequest{
		Token:          opts.Token,
		PublicKey:      identity.PublicKeyBase64(key),
		MachineHash:    machineHash,
		AgentVersion:   version.Version,
		Facts:          facts,
		Collectors:     collectors,
		Options:        protocol.EnrollOptions{MultiProject: multi, AllowRemoteUpdate: remote},
		ProjectID:      projectID,
		ReplaceAgentID: replaceID,
	})
	if err != nil {
		return explainEnrollError(err)
	}

	if err := identity.Save(config.KeyPath, key, uid, gid); err != nil {
		return err
	}
	cfg := &config.Config{
		APIURL:            opts.APIURL,
		DownloadBaseURL:   opts.DownloadBaseURL,
		AgentID:           enrolled.AgentID,
		OrgName:           enrolled.Org.Name,
		Collectors:        collectors,
		MultiProject:      multi,
		AllowRemoteUpdate: remote,
	}
	if err := config.Save(config.ConfigPath, cfg, gid); err != nil {
		return err
	}
	if slices.Contains(collectors, protocol.CollectorDocker) {
		if err := setDockerAccess(true); err != nil {
			p.Printf("Attention : %v\n", err)
		}
	}

	// Premier signal envoyé tout de suite : l'admin Network passe à « Connecté ».
	api.AgentID, api.Key = enrolled.AgentID, key
	collector.Prime()
	time.Sleep(time.Second)
	sample := collector.Sample(ctx, collectors)
	if _, err := api.Report(ctx, protocol.ReportRequest{
		AgentVersion: version.Version,
		SentAt:       time.Now().UTC(),
		Facts:        &facts,
		Samples:      []protocol.Sample{sample},
	}); err != nil {
		return fmt.Errorf("inscription faite, mais le premier envoi a échoué : %w", err)
	}

	if opts.NoService {
		p.Printf("\nAgent inscrit. Service non installé (--no-service) : lancez « horus-agent run » sous le compte horus-agent.\n")
	} else {
		if err := installService(); err != nil {
			return err
		}
		p.Printf("\nAgent installé et connecté.\n")
	}
	p.Printf("Retrouvez la machine dans Network : %s\n", enrolled.AgentURL)
	return nil
}

func chooseProject(p *Prompter, preview *protocol.PreviewResponse, opts Options) (string, string, error) {
	none := "à placer plus tard"
	switch {
	case opts.Project == "none":
		return "", none, nil
	case opts.Project != "":
		for _, pr := range preview.Projects {
			if pr.ID == opts.Project || pr.Name == opts.Project {
				return pr.ID, pr.Name, nil
			}
		}
		return "", "", fmt.Errorf("projet « %s » introuvable parmi les projets proposés", opts.Project)
	}

	def := 0
	options := []string{"Ne pas placer maintenant (la machine attendra dans l'onglet Monitoring)"}
	for i, pr := range preview.Projects {
		label := pr.Name
		if preview.PreselectedProject != nil && pr.ID == preview.PreselectedProject.ID {
			label += " (choisi dans Network)"
			def = i + 1
		}
		options = append(options, label)
	}
	if opts.Yes || len(preview.Projects) == 0 {
		if def == 0 {
			return "", none, nil
		}
		pr := preview.Projects[def-1]
		return pr.ID, pr.Name, nil
	}
	index, err := p.Choice("Dans quel projet placer cette machine ?", options, def)
	if err != nil {
		return "", "", err
	}
	p.Printf("\n")
	if index == 0 {
		return "", none, nil
	}
	pr := preview.Projects[index-1]
	return pr.ID, pr.Name, nil
}

func explainEnrollError(err error) error {
	switch {
	case client.IsCode(err, "token_expired"):
		return errors.New("le jeton a expiré : générez une nouvelle commande dans l'admin Network")
	case client.IsCode(err, "token_used"):
		return errors.New("ce jeton a déjà servi : générez une nouvelle commande dans l'admin Network")
	case client.IsCode(err, "token_invalid"):
		return errors.New("jeton inconnu : vérifiez la commande copiée depuis l'admin Network")
	case client.IsCode(err, "monitoring_not_installed"):
		return errors.New("l'intégration Monitoring n'est pas installée pour cette organisation : installez-la depuis la Marketplace de Network, puis relancez la même commande")
	case client.IsCode(err, "agent_quota_reached"):
		return errors.New("la limite d'agents de l'organisation est atteinte")
	}
	return err
}

// ConfigureOptions de `horus-agent configure`.
type ConfigureOptions struct {
	Metrics      []string
	RemoteUpdate *bool
	Yes          bool
	Out          io.Writer
}

// Configure change ce qui est surveillé et l'option de mise à jour à distance.
// Network l'apprend au prochain envoi, par la fiche.
func Configure(opts ConfigureOptions) error {
	if err := requireRoot(); err != nil {
		return err
	}
	cfg, err := config.Load(config.ConfigPath)
	if err != nil {
		return err
	}
	var p *Prompter
	if opts.Yes || (opts.Metrics != nil && opts.RemoteUpdate != nil) {
		p = NewPrompter(strings.NewReader(""), opts.Out)
	} else {
		tty, closeTTY, err := OpenTTY()
		if err != nil {
			return err
		}
		defer closeTTY()
		p = tty
	}

	before := slices.Contains(cfg.Collectors, protocol.CollectorDocker)
	if opts.Metrics != nil {
		cfg.Collectors = opts.Metrics
	} else if !opts.Yes {
		chosen, err := p.Checklist("Que voulez-vous surveiller ?", collectorOptions(cfg.Collectors))
		if err != nil {
			return err
		}
		cfg.Collectors = checkedCollectors(chosen)
	}
	if opts.RemoteUpdate != nil {
		cfg.AllowRemoteUpdate = *opts.RemoteUpdate
	} else if !opts.Yes {
		if cfg.AllowRemoteUpdate, err = p.Confirm("Autoriser les mises à jour lancées depuis Network ?", cfg.AllowRemoteUpdate); err != nil {
			return err
		}
	}

	_, gid, err := agentAccount()
	if err != nil {
		return err
	}
	if err := config.Save(config.ConfigPath, cfg, gid); err != nil {
		return err
	}
	if after := slices.Contains(cfg.Collectors, protocol.CollectorDocker); after != before {
		if err := setDockerAccess(after); err != nil {
			p.Printf("Attention : %v\n", err)
		}
	}
	if err := restartService(); err != nil {
		return err
	}
	p.Printf("Configuration enregistrée : %s. Mises à jour à distance : %s.\n",
		strings.Join(cfg.Collectors, ", "), yesNo(cfg.AllowRemoteUpdate))
	return nil
}

// Uninstall prévient Network puis supprime l'agent de la machine.
func Uninstall(ctx context.Context, yes bool, out io.Writer) error {
	if err := requireRoot(); err != nil {
		return err
	}
	if !yes {
		p, closeTTY, err := OpenTTY()
		if err != nil {
			return err
		}
		ok, err := p.Confirm("Désinstaller l'agent et supprimer sa configuration ?", false)
		closeTTY()
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("désinstallation annulée")
		}
	}

	if cfg, err := config.Load(config.ConfigPath); err == nil {
		if key, err := identity.Load(config.KeyPath); err == nil {
			api := client.New(cfg.APIURL)
			api.AgentID, api.Key = cfg.AgentID, key
			notifyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			if err := api.Uninstall(notifyCtx); err != nil {
				fmt.Fprintf(out, "Network n'a pas pu être prévenu (%v), la désinstallation continue.\n", err)
			}
			cancel()
		}
	}

	if hasSystemd() {
		_ = run("systemctl", "disable", "--now", config.ServiceName)
		_ = os.Remove(config.UnitPath)
		_ = run("systemctl", "daemon-reload")
	}
	for _, dir := range []string{"/etc/horus-agent", config.StateDir, config.InstallDir} {
		if err := os.RemoveAll(dir); err != nil {
			fmt.Fprintf(out, "Impossible de supprimer %s : %v\n", dir, err)
		}
	}
	_ = os.Remove("/usr/local/bin/horus-agent")
	_ = run("userdel", config.UserName)
	fmt.Fprintln(out, "Agent désinstallé.")
	return nil
}

// Status affiche la configuration et l'état du service.
func Status(out io.Writer) error {
	cfg, err := config.Load(config.ConfigPath)
	if err != nil {
		return fmt.Errorf("agent non installé ou configuration illisible : %w", err)
	}
	fmt.Fprintf(out, "Version          %s\n", version.Version)
	fmt.Fprintf(out, "Service          %s\n", serviceState())
	fmt.Fprintf(out, "Agent            %s\n", cfg.AgentID)
	if cfg.OrgName != "" {
		fmt.Fprintf(out, "Organisation     %s\n", cfg.OrgName)
	}
	fmt.Fprintf(out, "Network          %s\n", cfg.APIURL)
	fmt.Fprintf(out, "Surveillance     %s\n", strings.Join(cfg.Collectors, ", "))
	fmt.Fprintf(out, "Multi-projets    %s\n", yesNo(cfg.MultiProject))
	fmt.Fprintf(out, "Mises à jour à distance %s\n", yesNo(cfg.AllowRemoteUpdate))
	return nil
}
