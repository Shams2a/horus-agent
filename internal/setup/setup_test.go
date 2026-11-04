package setup

import (
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/Shams2a/horus-agent/internal/config"
	"github.com/Shams2a/horus-agent/internal/protocol"
)

func prompter(input string) *Prompter {
	return NewPrompter(strings.NewReader(input), io.Discard)
}

func TestChecklistTogglesAndValidates(t *testing.T) {
	options := collectorOptions([]string{"system", "systemd"})
	got, err := prompter("3\n2\n\n").Checklist("?", options)
	if err != nil {
		t.Fatal(err)
	}
	if c := checkedCollectors(got); !slices.Equal(c, []string{"system", "docker"}) {
		t.Errorf("collecteurs = %v", c)
	}
}

func TestConfirmDefaultsAndRetries(t *testing.T) {
	if ok, _ := prompter("\n").Confirm("?", true); !ok {
		t.Error("Entrée doit valider la réponse par défaut")
	}
	if ok, _ := prompter("peut-être\nn\n").Confirm("?", true); ok {
		t.Error("« n » attendu après une réponse invalide")
	}
}

func TestChooseProject(t *testing.T) {
	preview := &protocol.PreviewResponse{
		Projects:           []protocol.ProjectRef{{ID: "p1", Name: "Infra"}, {ID: "p2", Name: "Labo"}},
		PreselectedProject: &protocol.ProjectRef{ID: "p2", Name: "Labo"},
	}
	cases := []struct {
		name   string
		opts   Options
		input  string
		wantID string
	}{
		{"présélection par défaut", Options{}, "\n", "p2"},
		{"choix au clavier", Options{}, "2\n", "p1"},
		{"aucun projet", Options{}, "1\n", ""},
		{"option par nom", Options{Project: "Infra"}, "", "p1"},
		{"option none", Options{Project: "none"}, "", ""},
		{"--yes garde la présélection", Options{Yes: true}, "", "p2"},
	}
	for _, c := range cases {
		id, _, err := chooseProject(prompter(c.input), preview, c.opts)
		if err != nil || id != c.wantID {
			t.Errorf("%s : id = %q, erreur = %v", c.name, id, err)
		}
	}
	if _, _, err := chooseProject(prompter(""), preview, Options{Project: "Inconnu"}); err == nil {
		t.Error("projet inconnu accepté")
	}
}

func TestParseMetrics(t *testing.T) {
	got, err := ParseMetrics("system, docker,system")
	if err != nil || !slices.Equal(got, []string{"system", "docker"}) {
		t.Errorf("ParseMetrics = %v, %v", got, err)
	}
	if _, err := ParseMetrics("snmp"); err == nil {
		t.Error("collecteur inconnu accepté")
	}
}

func TestUnitHardening(t *testing.T) {
	unit := string(unitFile)
	for _, line := range []string{
		"User=horus-agent",
		"ExecStart=/opt/horus-agent/bin/horus-agent run",
		"RestartPreventExitStatus=78",
		"ProtectSystem=strict",
		"ReadWritePaths=/var/lib/horus-agent",
		"NoNewPrivileges=yes",
	} {
		if !strings.Contains(unit, line) {
			t.Errorf("service : %q manquant", line)
		}
	}
}

func TestRecapProject(t *testing.T) {
	cases := []struct {
		replacing bool
		id, name  string
		want      string
	}{
		{false, "", "à placer plus tard", "à placer plus tard"},
		{false, "p1", "Infra", "Infra"},
		{true, "", "à placer plus tard", "placement conservé"},
		{true, "p1", "Infra", "Infra (en plus des placements existants)"},
	}
	for _, c := range cases {
		if got := recapProject(c.replacing, c.id, c.name); got != c.want {
			t.Errorf("recapProject(%v, %q, %q) = %q, attendu %q", c.replacing, c.id, c.name, got, c.want)
		}
	}
}

func TestHelperUnits(t *testing.T) {
	path := string(helperPathFile)
	for _, line := range []string{
		"PathExists=/var/lib/horus-agent/requests/uninstall",
		"PathExists=/var/lib/horus-agent/requests/update/ready",
		"Unit=horus-agent-helper.service",
	} {
		if !strings.Contains(path, line) {
			t.Errorf("unité path : %q manquant", line)
		}
	}
	service := string(helperServiceFile)
	if !strings.Contains(service, "ExecStart=/opt/horus-agent/bin/horus-agent helper") || strings.Contains(service, "User=") {
		t.Errorf("le service d'aide doit lancer « horus-agent helper » en root :\n%s", service)
	}
	if strings.Contains(string(unitFile), "/opt/horus-agent /var") {
		t.Error("l'agent ne doit plus pouvoir écrire dans /opt/horus-agent")
	}
}

func TestLXCDropInOnlyInLXC(t *testing.T) {
	if _, ok := serviceFiles(false)[config.LXCDropIn]; ok {
		t.Error("complément LXC écrit hors d'un conteneur LXC")
	}
	dropIn := string(serviceFiles(true)[config.LXCDropIn])
	for _, want := range []string{"[Service]", "ProtectKernelTunables=no", "ProtectControlGroups=no"} {
		if !strings.Contains(dropIn, want) {
			t.Errorf("complément LXC sans %q", want)
		}
	}
	// Le reste de la protection reste en place.
	if strings.Contains(dropIn, "ProtectSystem") || strings.Contains(dropIn, "NoNewPrivileges") {
		t.Error("le complément LXC ne doit relâcher que les deux protections qui masquent LXCFS")
	}
}
