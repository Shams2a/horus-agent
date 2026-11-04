// Commande horus-agent : agent de monitoring Horus Network.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/Shams2a/horus-agent/internal/agent"
	"github.com/Shams2a/horus-agent/internal/client"
	"github.com/Shams2a/horus-agent/internal/collect"
	"github.com/Shams2a/horus-agent/internal/config"
	"github.com/Shams2a/horus-agent/internal/identity"
	"github.com/Shams2a/horus-agent/internal/protocol"
	"github.com/Shams2a/horus-agent/internal/setup"
	"github.com/Shams2a/horus-agent/internal/update"
	"github.com/Shams2a/horus-agent/internal/version"
)

const usage = `horus-agent, agent de monitoring Horus Network

Usage :
  horus-agent setup --token <jeton> --api-url <url>   Inscrit la machine et démarre le service
  horus-agent configure                              Change ce qui est surveillé
  horus-agent status                                 Affiche la configuration et l'état du service
  horus-agent update [version]                       Installe la dernière version, ou celle indiquée
  horus-agent uninstall                              Prévient Network et supprime l'agent
  horus-agent run                                    Lance l'agent (utilisé par le service systemd)
  horus-agent helper                                 Service d'aide root (utilisé par systemd)
  horus-agent docker-read                            Lecture Docker en root (utilisé par systemd)
  horus-agent version                                Affiche la version

Ajoutez --help après une commande pour voir ses options.
`

// Code de sortie qui empêche systemd de relancer le service (RestartPreventExitStatus).
const exitDoNotRestart = 78

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	cmd, args := os.Args[1], os.Args[2:]
	switch cmd {
	case "version", "--version", "-v":
		fmt.Printf("horus-agent %s (%s/%s)\n", version.Version, runtime.GOOS, runtime.GOARCH)
	case "help", "--help", "-h":
		fmt.Print(usage)
	case "setup":
		err = cmdSetup(ctx, args)
	case "configure":
		err = cmdConfigure(args)
	case "status":
		err = setup.Status(os.Stdout)
	case "update":
		err = cmdUpdate(ctx, args)
	case "uninstall":
		err = cmdUninstall(ctx, args)
	case "run":
		os.Exit(cmdRun(ctx))
	case "helper":
		err = setup.Helper(ctx, os.Stdout)
	case "docker-read":
		err = cmdDockerRead(ctx)
	default:
		fmt.Fprintf(os.Stderr, "Commande inconnue : %s\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Erreur : %v\n", err)
		os.Exit(1)
	}
}

// boolFlag distingue « non précisé » de « faux ».
type boolFlag struct{ value *bool }

func (b *boolFlag) String() string   { return "" }
func (b *boolFlag) IsBoolFlag() bool { return true }
func (b *boolFlag) Set(s string) error {
	v := s == "true" || s == "1" || s == ""
	if s != "true" && s != "false" && s != "1" && s != "0" && s != "" {
		return fmt.Errorf("valeur attendue : true ou false")
	}
	b.value = &v
	return nil
}

func negated(b *boolFlag) *bool {
	if b.value == nil {
		return nil
	}
	v := !*b.value
	return &v
}

func cmdSetup(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	opts := setup.Options{Out: os.Stdout}
	var metrics string
	var multi, noRemote, noReplace boolFlag
	fs.StringVar(&opts.Token, "token", "", "jeton d'inscription généré dans l'admin Network")
	fs.StringVar(&opts.APIURL, "api-url", "", "adresse de l'API Network")
	fs.StringVar(&opts.DownloadBaseURL, "download-base", config.DefaultDownloadBaseURL, "adresse des Releases pour les mises à jour")
	fs.StringVar(&metrics, "metrics", "", "collecteurs séparés par des virgules : system, systemd, docker")
	fs.StringVar(&opts.Project, "project", "", "projet où placer la machine (identifiant ou nom), ou none")
	fs.Var(&multi, "multi-project", "la machine pourra être placée dans plusieurs projets")
	fs.Var(&noRemote, "no-remote-update", "refuser les mises à jour lancées depuis Network")
	fs.Var(&noReplace, "no-replace", "ne pas reprendre l'agent déjà inscrit sur cette machine")
	fs.BoolVar(&opts.Yes, "yes", false, "ne poser aucune question (valeurs par défaut et options ci-dessus)")
	fs.BoolVar(&opts.NoService, "no-service", false, "ne pas installer le service systemd (conteneurs sans systemd)")
	repair := fs.Bool("repair", false, "réparer une installation existante sans la réinscrire (binaire root, service d'aide)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *repair {
		return setup.Repair(os.Stdout)
	}
	if metrics != "" {
		m, err := setup.ParseMetrics(metrics)
		if err != nil {
			return err
		}
		opts.Metrics = m
	}
	opts.MultiProject = multi.value
	opts.RemoteUpdate = negated(&noRemote)
	opts.Replace = negated(&noReplace)
	return setup.Setup(ctx, opts)
}

func cmdConfigure(args []string) error {
	fs := flag.NewFlagSet("configure", flag.ContinueOnError)
	opts := setup.ConfigureOptions{Out: os.Stdout}
	var metrics string
	var remote boolFlag
	fs.StringVar(&metrics, "metrics", "", "collecteurs séparés par des virgules : system, systemd, docker")
	fs.Var(&remote, "remote-update", "autoriser (true) ou refuser (false) les mises à jour lancées depuis Network")
	fs.BoolVar(&opts.Yes, "yes", false, "ne poser aucune question")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if metrics != "" {
		m, err := setup.ParseMetrics(metrics)
		if err != nil {
			return err
		}
		opts.Metrics = m
	}
	opts.RemoteUpdate = remote.value
	return setup.Configure(opts)
}

func cmdUninstall(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "ne pas demander de confirmation")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return setup.Uninstall(ctx, *yes, os.Stdout)
}

func cmdUpdate(ctx context.Context, args []string) error {
	if os.Geteuid() != 0 {
		return errors.New("cette commande doit être lancée en root (sudo)")
	}
	cfg, err := config.Load(config.ConfigPath)
	if err != nil {
		return err
	}
	u, err := update.New(cfg.DownloadBaseURL, config.BinaryPath, version.Version)
	if err != nil {
		return err
	}
	target := ""
	if len(args) > 0 {
		target = args[0]
	} else if target, err = u.Latest(ctx); err != nil {
		return err
	}
	if err := u.Apply(ctx, target); err != nil {
		return err
	}
	fmt.Printf("horus-agent %s installé.\n", target)
	if _, statErr := os.Stat("/run/systemd/system"); statErr == nil {
		return execRestart()
	}
	return nil
}

func execRestart() error {
	return runCommand("systemctl", "restart", config.ServiceName)
}

// cmdRun est la boucle du service. Il renvoie le code de sortie.
// cmdDockerRead : lancé en root par horus-agent-docker@.service, une fois par
// connexion de l'agent. Ne lit rien sur l'entrée : renvoie la liste des
// conteneurs sur la sortie (le socket), puis s'arrête.
func cmdDockerRead(ctx context.Context) error {
	if os.Geteuid() != 0 {
		return errors.New("cette commande doit être lancée en root (sudo)")
	}
	return json.NewEncoder(os.Stdout).Encode(collect.New().DockerDirect(ctx))
}

func cmdRun(ctx context.Context) int {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	cfg, err := config.Load(config.ConfigPath)
	if err != nil {
		log.Error("configuration inutilisable", "erreur", err)
		return exitDoNotRestart
	}
	key, err := identity.Load(config.KeyPath)
	if err != nil {
		log.Error("clé de l'agent illisible", "erreur", err)
		return exitDoNotRestart
	}
	api := client.New(cfg.APIURL)
	api.AgentID, api.Key = cfg.AgentID, key

	// Avec le service d'aide (installation 0.2.0), le binaire appartient à
	// root : l'agent dépose les mises à jour et la désinstallation, le service
	// d'aide les exécute. Sans lui (installation 0.1.x), l'agent remplace son
	// binaire lui-même et ne peut pas se désinstaller.
	helper := setup.HelperInstalled()
	var capabilities []string
	if helper {
		capabilities = []string{protocol.CapabilityRemoteUninstall}
	}
	runner := &agent.Runner{
		Config:       cfg,
		Client:       api,
		Collector:    collect.New(),
		Version:      version.Version,
		Log:          log,
		Capabilities: capabilities,
		Repair:       setup.RepairNeeded(cfg),
		Update: func(ctx context.Context, target string) (bool, error) {
			u, err := update.New(cfg.DownloadBaseURL, config.BinaryPath, version.Version)
			if err != nil {
				return false, err
			}
			if helper {
				return false, u.Stage(ctx, target, config.UpdateRequestDir)
			}
			return true, u.Apply(ctx, target)
		},
		Uninstall: func(ctx context.Context) error {
			// Confirmer d'abord : Network efface alors l'agent de son côté.
			if err := api.Uninstall(ctx); err != nil {
				log.Warn("confirmation de la désinstallation impossible", "erreur", err)
			}
			if !helper {
				log.Warn("service d'aide absent : l'agent s'arrête, mais ses fichiers restent. « sudo horus-agent uninstall » les supprime.")
				return nil
			}
			return os.WriteFile(config.UninstallRequest, nil, 0o640)
		},
	}
	log.Info("agent démarré", "version", version.Version, "agent", cfg.AgentID, "collecteurs", cfg.Collectors)
	err = runner.Run(ctx)
	switch {
	case errors.Is(err, agent.ErrRevoked):
		log.Error("agent révoqué depuis Network : arrêt définitif. Réinstallez-le pour le reconnecter.")
		return exitDoNotRestart
	case errors.Is(err, agent.ErrRestart):
		log.Info("nouvelle version installée, redémarrage")
		return 0
	case errors.Is(err, agent.ErrUninstall):
		log.Info("désinstallation demandée depuis Network : arrêt définitif, le service d'aide supprime l'agent.")
		return exitDoNotRestart
	case errors.Is(err, context.Canceled):
		return 0
	case err != nil:
		log.Error("arrêt sur erreur", "erreur", err)
		return 1
	}
	return 0
}

func runCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}
