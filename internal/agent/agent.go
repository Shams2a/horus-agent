// Package agent fait tourner l'agent : une mesure par minute, un envoi toutes
// les 30 secondes, mise en tampon quand Network est injoignable (§ 4).
package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/Shams2a/horus-agent/internal/client"
	"github.com/Shams2a/horus-agent/internal/config"
	"github.com/Shams2a/horus-agent/internal/protocol"
)

const (
	SampleInterval        = time.Minute
	DefaultReportInterval = 30 * time.Second
	MinReportInterval     = 15 * time.Second
	MaxReportInterval     = 300 * time.Second
	// 24 h de mesures à une par minute (§ 4).
	MaxBuffered  = 24 * 60
	FactsMaxAge  = time.Hour
	backoffStart = 5 * time.Second
	backoffMax   = 5 * time.Minute
)

// ErrRevoked : Network a révoqué l'agent. Le service doit s'arrêter pour de
// bon (code de sortie 78, § 8).
var ErrRevoked = errors.New("agent révoqué depuis Network")

// ErrRestart : une mise à jour est installée, le service doit redémarrer.
var ErrRestart = errors.New("mise à jour installée, redémarrage")

// Collector est le sous-ensemble de collect.Collector utilisé ici.
type Collector interface {
	Prime()
	Sample(ctx context.Context, collectors []string) protocol.Sample
	Facts(ctx context.Context) protocol.Facts
}

// Updater installe une version demandée par Network.
type Updater func(ctx context.Context, version string) error

type Runner struct {
	Config    *config.Config
	Client    *client.Client
	Collector Collector
	Update    Updater
	Version   string
	Log       *slog.Logger
	Now       func() time.Time

	buffer      []protocol.Sample
	batch       int
	interval    time.Duration
	factsHash   [32]byte
	factsSentAt time.Time
}

func (r *Runner) init() {
	if r.Now == nil {
		r.Now = time.Now
	}
	if r.Log == nil {
		r.Log = slog.Default()
	}
	r.batch = protocol.MaxSamplesPerReport
	if r.interval == 0 {
		r.interval = DefaultReportInterval
	}
}

// Run tourne jusqu'à l'annulation du contexte, une révocation (ErrRevoked)
// ou une mise à jour installée (ErrRestart).
func (r *Runner) Run(ctx context.Context) error {
	r.init()
	r.Collector.Prime()
	// Laisse une seconde aux compteurs pour que la première mesure ait un sens.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Second):
	}
	r.collect(ctx)

	sampleTicker := time.NewTicker(SampleInterval)
	defer sampleTicker.Stop()
	nextReport := time.NewTimer(0)
	defer nextReport.Stop()
	backoff := time.Duration(0)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-sampleTicker.C:
			r.collect(ctx)
		case <-nextReport.C:
			err := r.Flush(ctx)
			switch {
			case errors.Is(err, ErrRevoked), errors.Is(err, ErrRestart):
				return err
			case err != nil:
				backoff = nextBackoff(backoff)
				r.Log.Warn("envoi impossible, nouvel essai plus tard",
					"erreur", err, "dans", backoff.String(), "en_tampon", len(r.buffer))
				nextReport.Reset(backoff)
			default:
				backoff = 0
				nextReport.Reset(r.interval)
			}
		}
	}
}

func nextBackoff(current time.Duration) time.Duration {
	if current == 0 {
		return backoffStart
	}
	return min(current*2, backoffMax)
}

func (r *Runner) collect(ctx context.Context) {
	r.buffer = append(r.buffer, r.Collector.Sample(ctx, r.Config.Collectors))
	if over := len(r.buffer) - MaxBuffered; over > 0 {
		r.buffer = r.buffer[over:]
		r.Log.Warn("tampon plein, mesures les plus anciennes supprimées", "supprimées", over)
	}
}

// facts renvoie la fiche à joindre à l'envoi, ou nil si elle n'a pas changé
// depuis moins d'une heure.
func (r *Runner) facts(ctx context.Context) *protocol.Facts {
	f := r.Collector.Facts(ctx)
	f.Collectors = r.Config.Collectors
	f.AllowRemoteUpdate = r.Config.AllowRemoteUpdate
	raw, _ := json.Marshal(f)
	hash := sha256.Sum256(raw)
	if hash == r.factsHash && r.Now().Sub(r.factsSentAt) < FactsMaxAge {
		return nil
	}
	r.factsHash = hash
	return &f
}

// Flush envoie le tampon, par paquets, jusqu'à le vider. Sans mesure en
// attente, il envoie quand même un signal de vie.
func (r *Runner) Flush(ctx context.Context) error {
	if r.batch == 0 {
		r.init()
	}
	facts := r.facts(ctx)
	for first := true; first || len(r.buffer) > 0; first = false {
		n := min(r.batch, len(r.buffer))
		req := protocol.ReportRequest{
			AgentVersion: r.Version,
			SentAt:       r.Now().UTC(),
			Facts:        facts,
			Samples:      r.buffer[:n],
		}
		resp, err := r.Client.Report(ctx, req)
		if client.StatusOf(err) == http.StatusGone {
			return ErrRevoked
		}
		if err != nil {
			if done, handled := r.handleError(err, n); handled {
				if done {
					continue
				}
				return nil
			}
			// La fiche n'est pas partie : on la renverra.
			r.factsHash = [32]byte{}
			return err
		}
		r.buffer = r.buffer[n:]
		if facts != nil {
			r.factsSentAt = r.Now()
			facts = nil
		}
		r.applyInterval(resp.ReportIntervalS)
		if resp.Update != nil && resp.Update.Version != "" {
			if err := r.applyUpdate(ctx, resp.Update.Version); err != nil {
				return err
			}
		}
	}
	return nil
}

// handleError traite les erreurs qui ne demandent pas d'attendre. Renvoie
// handled=false pour les erreurs à réessayer plus tard (réseau, 401, 429, 5xx).
// done=true : on peut continuer à vider le tampon tout de suite.
func (r *Runner) handleError(err error, n int) (done, handled bool) {
	switch client.StatusOf(err) {
	case http.StatusBadRequest:
		r.Log.Error("Network a refusé l'envoi, mesures abandonnées", "erreur", err, "mesures", n)
		r.buffer = r.buffer[n:]
		return len(r.buffer) > 0, true
	case http.StatusRequestEntityTooLarge:
		if n <= 1 {
			r.Log.Error("mesure trop volumineuse, abandonnée", "erreur", err)
			r.buffer = r.buffer[n:]
		} else {
			r.batch = max(1, n/2)
		}
		return len(r.buffer) > 0, true
	}
	return false, false
}

func (r *Runner) applyInterval(seconds int) {
	if seconds <= 0 {
		return
	}
	d := time.Duration(seconds) * time.Second
	r.interval = min(max(d, MinReportInterval), MaxReportInterval)
}

func (r *Runner) applyUpdate(ctx context.Context, version string) error {
	if !r.Config.AllowRemoteUpdate || r.Update == nil {
		return nil
	}
	r.Log.Info("mise à jour demandée depuis Network", "version", version)
	if err := r.Update(ctx, version); err != nil {
		// Une mise à jour ratée ne doit pas arrêter la surveillance.
		r.Log.Error("mise à jour impossible", "version", version, "erreur", err)
		return nil
	}
	return ErrRestart
}
