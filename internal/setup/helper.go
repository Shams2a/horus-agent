package setup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/Shams2a/horus-agent/internal/config"
	"github.com/Shams2a/horus-agent/internal/update"
	"github.com/Shams2a/horus-agent/internal/version"
)

// HelperInstalled indique si le service d'aide root est en place (installation
// 0.2.0 ou réparée). Sans lui, ni la désinstallation à distance ni la mise à
// jour par dépôt ne sont possibles.
func HelperInstalled() bool {
	_, err := os.Stat(config.HelperPathUnit)
	return err == nil
}

func exists(name string) bool {
	_, err := os.Lstat(name)
	return err == nil || !errors.Is(err, fs.ErrNotExist)
}

// Helper traite les demandes déposées par l'agent (docs/protocole.md § 7 et
// § 7 bis). Lancé en root par horus-agent-helper.service.
func Helper(ctx context.Context, out io.Writer) error {
	if err := requireRoot(); err != nil {
		return err
	}
	if exists(config.UninstallRequest) {
		_ = os.Remove(config.UninstallRequest)
		fmt.Fprintln(out, "Désinstallation demandée depuis Network.")
		// L'agent a déjà confirmé auprès de Network : pas de second appel.
		return uninstall(ctx, out, false)
	}

	ready := filepath.Join(config.UpdateRequestDir, update.ReadyFile)
	if !exists(ready) {
		return nil
	}
	// Retirer la demande d'abord : un échec ne doit pas relancer le service
	// d'aide en boucle.
	_ = os.Remove(ready)
	defer os.RemoveAll(config.UpdateRequestDir)

	u, err := update.New("", config.BinaryPath, version.Version)
	if err != nil {
		return err
	}
	installed, err := u.InstallStaged(config.UpdateRequestDir)
	if err != nil {
		return fmt.Errorf("mise à jour déposée refusée : %w", err)
	}
	fmt.Fprintf(out, "horus-agent %s installé, redémarrage de l'agent.\n", installed)
	return restartService()
}
