package setup

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// ErrNoTerminal : pas de terminal pour poser les questions.
var ErrNoTerminal = errors.New("aucun terminal disponible : relancez avec --yes et les options voulues (voir horus-agent setup --help)")

// Prompter pose les questions dans le terminal. Avec `curl … | sudo bash`,
// l'entrée standard est le script : on lit donc /dev/tty.
type Prompter struct {
	in  *bufio.Reader
	out io.Writer
}

// OpenTTY ouvre /dev/tty, ou l'entrée standard si c'est déjà un terminal.
func OpenTTY() (*Prompter, func(), error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, func() {}, ErrNoTerminal
	}
	return &Prompter{in: bufio.NewReader(tty), out: tty}, func() { tty.Close() }, nil
}

// NewPrompter sert aux tests.
func NewPrompter(in io.Reader, out io.Writer) *Prompter {
	return &Prompter{in: bufio.NewReader(in), out: out}
}

func (p *Prompter) Printf(format string, args ...any) {
	fmt.Fprintf(p.out, format, args...)
}

func (p *Prompter) line() (string, error) {
	s, err := p.in.ReadString('\n')
	if err != nil && s == "" {
		return "", err
	}
	return strings.TrimSpace(s), nil
}

// Confirm pose une question oui / non. Entrée seule : la réponse par défaut.
func (p *Prompter) Confirm(question string, def bool) (bool, error) {
	hint := "[o/N]"
	if def {
		hint = "[O/n]"
	}
	for {
		p.Printf("%s %s ", question, hint)
		answer, err := p.line()
		if err != nil {
			return false, err
		}
		switch strings.ToLower(answer) {
		case "":
			return def, nil
		case "o", "oui", "y", "yes":
			return true, nil
		case "n", "non", "no":
			return false, nil
		}
		p.Printf("Répondez o (oui) ou n (non).\n")
	}
}

// Choice affiche une liste numérotée et renvoie l'index choisi.
func (p *Prompter) Choice(question string, options []string, def int) (int, error) {
	p.Printf("%s\n", question)
	for i, o := range options {
		marker := " "
		if i == def {
			marker = ">"
		}
		p.Printf(" %s %d) %s\n", marker, i+1, o)
	}
	for {
		p.Printf("Votre choix [%d] : ", def+1)
		answer, err := p.line()
		if err != nil {
			return 0, err
		}
		if answer == "" {
			return def, nil
		}
		if n, err := strconv.Atoi(answer); err == nil && n >= 1 && n <= len(options) {
			return n - 1, nil
		}
		p.Printf("Tapez un numéro entre 1 et %d.\n", len(options))
	}
}

// Option d'une liste à cocher.
type Option struct {
	Label   string
	Detail  string
	Checked bool
}

// Checklist affiche des cases à cocher. Un numéro coche ou décoche, Entrée valide.
func (p *Prompter) Checklist(question string, options []Option) ([]Option, error) {
	for {
		p.Printf("%s\n", question)
		for i, o := range options {
			box := "[ ]"
			if o.Checked {
				box = "[x]"
			}
			p.Printf("  %d. %s %s\n", i+1, box, o.Label)
			if o.Detail != "" {
				p.Printf("         %s\n", o.Detail)
			}
		}
		p.Printf("Tapez un numéro pour cocher ou décocher, Entrée pour valider : ")
		answer, err := p.line()
		if err != nil {
			return nil, err
		}
		if answer == "" {
			return options, nil
		}
		for _, field := range strings.FieldsFunc(answer, func(r rune) bool { return r == ' ' || r == ',' }) {
			if n, err := strconv.Atoi(field); err == nil && n >= 1 && n <= len(options) {
				options[n-1].Checked = !options[n-1].Checked
			}
		}
		p.Printf("\n")
	}
}
