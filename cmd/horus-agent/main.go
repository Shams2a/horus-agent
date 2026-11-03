// Commande horus-agent : agent de monitoring Horus Network.
//
// Lot 0 : seule la commande « version » existe. L'assistant d'installation,
// les collecteurs et l'envoi des mesures arrivent au lot 2.
package main

import (
	"fmt"
	"os"
	"runtime"

	"github.com/Shams2a/horus-agent/internal/version"
)

const usage = `horus-agent, agent de monitoring Horus Network

Usage :
  horus-agent version    Affiche la version
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version", "--version", "-v":
		fmt.Printf("horus-agent %s (%s/%s)\n", version.Version, runtime.GOOS, runtime.GOARCH)
	case "help", "--help", "-h":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "Commande inconnue : %s\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
}
