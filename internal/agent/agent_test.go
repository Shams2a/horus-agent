package agent

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Shams2a/horus-agent/internal/client"
	"github.com/Shams2a/horus-agent/internal/config"
	"github.com/Shams2a/horus-agent/internal/protocol"
)

type fakeCollector struct{ n int }

func (f *fakeCollector) Prime() {}
func (f *fakeCollector) Sample(context.Context, []string) protocol.Sample {
	f.n++
	return protocol.Sample{Target: protocol.TargetSelf, TS: time.Unix(int64(f.n), 0).UTC()}
}
func (f *fakeCollector) Facts(context.Context) protocol.Facts {
	return protocol.Facts{Hostname: "srv"}
}

// fakeNetwork vérifie chaque signature et répond selon `respond`.
type fakeNetwork struct {
	t       *testing.T
	pub     ed25519.PublicKey
	mu      sync.Mutex
	reports []protocol.ReportRequest
	respond func(n int, req protocol.ReportRequest) (int, string)
}

func (f *fakeNetwork) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	ts, _ := strconv.ParseInt(r.Header.Get(protocol.HeaderTimestamp), 10, 64)
	if err := protocol.Verify(f.pub, r.Method, r.URL.Path, ts, body, r.Header.Get(protocol.HeaderSignature)); err != nil {
		f.t.Errorf("signature invalide : %v", err)
	}
	var req protocol.ReportRequest
	if err := json.Unmarshal(body, &req); err != nil {
		f.t.Errorf("corps illisible : %v", err)
	}
	f.mu.Lock()
	f.reports = append(f.reports, req)
	n := len(f.reports)
	f.mu.Unlock()
	status, payload := http.StatusOK, `{"report_interval_s":30,"update":null}`
	if f.respond != nil {
		status, payload = f.respond(n, req)
	}
	w.WriteHeader(status)
	w.Write([]byte(payload))
}

func newRunner(t *testing.T, respond func(int, protocol.ReportRequest) (int, string)) (*Runner, *fakeNetwork) {
	pub, key, _ := ed25519.GenerateKey(nil)
	net := &fakeNetwork{t: t, pub: pub, respond: respond}
	srv := httptest.NewServer(net)
	t.Cleanup(srv.Close)
	api := client.New(srv.URL + "/prefixe")
	api.AgentID, api.Key = "agent-1", key
	r := &Runner{
		Config:    &config.Config{Collectors: []string{"system"}, AllowRemoteUpdate: true},
		Client:    api,
		Collector: &fakeCollector{},
		Version:   "1.0.0",
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	r.init()
	return r, net
}

func TestBufferSurvivesOutageAndIsSentInOrder(t *testing.T) {
	down := true
	r, net := newRunner(t, func(n int, _ protocol.ReportRequest) (int, string) {
		if down {
			return http.StatusBadGateway, ``
		}
		return http.StatusOK, `{"report_interval_s":30}`
	})
	ctx := context.Background()
	for range 3 {
		r.collect(ctx)
	}
	if err := r.Flush(ctx); err == nil {
		t.Fatal("erreur attendue pendant la coupure")
	}
	if len(r.buffer) != 3 {
		t.Fatalf("tampon = %d, attendu 3", len(r.buffer))
	}
	down = false
	if err := r.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	last := net.reports[len(net.reports)-1]
	if len(last.Samples) != 3 || last.Samples[0].TS.Unix() != 1 || len(r.buffer) != 0 {
		t.Errorf("envoi = %+v, tampon = %d", last.Samples, len(r.buffer))
	}
	// La fiche ratée pendant la coupure est renvoyée.
	if last.Facts == nil {
		t.Error("la fiche doit être renvoyée après un échec")
	}
}

func TestFactsOnlyWhenChanged(t *testing.T) {
	r, net := newRunner(t, nil)
	ctx := context.Background()
	r.Flush(ctx)
	r.Flush(ctx)
	if net.reports[0].Facts == nil || net.reports[1].Facts != nil {
		t.Errorf("fiche : 1er envoi %v, 2e envoi %v", net.reports[0].Facts != nil, net.reports[1].Facts != nil)
	}
	if net.reports[1].Samples == nil {
		t.Error("samples doit valoir [] et non null")
	}
}

func TestRevokedStopsTheAgent(t *testing.T) {
	r, _ := newRunner(t, func(int, protocol.ReportRequest) (int, string) {
		return http.StatusGone, `{"error":{"code":"agent_revoked","message":"révoqué"}}`
	})
	if err := r.Flush(context.Background()); !errors.Is(err, ErrRevoked) {
		t.Errorf("erreur = %v, attendu ErrRevoked", err)
	}
}

func TestPayloadTooLargeSplitsTheBatch(t *testing.T) {
	r, net := newRunner(t, func(n int, req protocol.ReportRequest) (int, string) {
		if len(req.Samples) > 2 {
			return http.StatusRequestEntityTooLarge, `{"error":{"code":"payload_too_large","message":""}}`
		}
		return http.StatusOK, `{}`
	})
	ctx := context.Background()
	for range 5 {
		r.collect(ctx)
	}
	if err := r.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if len(r.buffer) != 0 {
		t.Errorf("tampon = %d", len(r.buffer))
	}
	sent := 0
	for _, rep := range net.reports {
		if len(rep.Samples) <= 2 {
			sent += len(rep.Samples)
		}
	}
	if sent != 5 {
		t.Errorf("mesures envoyées = %d, attendu 5", sent)
	}
}

func TestRemoteUpdate(t *testing.T) {
	r, _ := newRunner(t, func(int, protocol.ReportRequest) (int, string) {
		return http.StatusOK, `{"update":{"version":"1.1.0"}}`
	})
	var got string
	r.Update = func(_ context.Context, v string) (bool, error) { got = v; return true, nil }
	if err := r.Flush(context.Background()); !errors.Is(err, ErrRestart) || got != "1.1.0" {
		t.Errorf("erreur = %v, version = %q", err, got)
	}

	r.Config.AllowRemoteUpdate = false
	got = ""
	if err := r.Flush(context.Background()); err != nil || got != "" {
		t.Errorf("mise à jour refusée attendue, erreur = %v, version = %q", err, got)
	}
}

func TestStagedUpdateIsNotRepeated(t *testing.T) {
	r, _ := newRunner(t, func(int, protocol.ReportRequest) (int, string) {
		return http.StatusOK, `{"update":{"version":"1.1.0"}}`
	})
	calls := 0
	r.Update = func(context.Context, string) (bool, error) { calls++; return false, nil }
	for range 3 {
		if err := r.Flush(context.Background()); err != nil {
			t.Fatalf("une mise à jour déposée ne doit pas arrêter l'agent : %v", err)
		}
	}
	if calls != 1 {
		t.Errorf("version déposée %d fois, attendu 1", calls)
	}
}

func TestUninstallRequest(t *testing.T) {
	r, net := newRunner(t, func(int, protocol.ReportRequest) (int, string) {
		return http.StatusOK, `{"uninstall":true}`
	})
	r.Capabilities = []string{protocol.CapabilityRemoteUninstall}
	asked := false
	r.Uninstall = func(context.Context) error { asked = true; return nil }
	if err := r.Flush(context.Background()); !errors.Is(err, ErrUninstall) || !asked {
		t.Errorf("erreur = %v, désinstallation lancée = %v", err, asked)
	}
	if caps := net.reports[0].Facts.Capabilities; len(caps) != 1 || caps[0] != "remote_uninstall" {
		t.Errorf("capacités envoyées = %v", caps)
	}
}

// La boucle s'arrête d'elle-même, sans retenter l'envoi ni reconfirmer.
func TestRunStopsOnUninstall(t *testing.T) {
	r, net := newRunner(t, func(int, protocol.ReportRequest) (int, string) {
		return http.StatusOK, `{"uninstall":true}`
	})
	calls := 0
	r.Uninstall = func(context.Context) error { calls++; return nil }
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := r.Run(ctx); !errors.Is(err, ErrUninstall) {
		t.Fatalf("erreur = %v, attendu ErrUninstall", err)
	}
	if calls != 1 || len(net.reports) != 1 {
		t.Errorf("désinstallations = %d, envois = %d, attendu 1 et 1", calls, len(net.reports))
	}
}

func TestIntervalIsClamped(t *testing.T) {
	r, _ := newRunner(t, func(int, protocol.ReportRequest) (int, string) {
		return http.StatusOK, `{"report_interval_s":2}`
	})
	r.Flush(context.Background())
	if r.interval != MinReportInterval {
		t.Errorf("intervalle = %s", r.interval)
	}
}

func TestBufferIsBounded(t *testing.T) {
	r, _ := newRunner(t, nil)
	for range MaxBuffered + 10 {
		r.collect(context.Background())
	}
	if len(r.buffer) != MaxBuffered || r.buffer[0].TS.Unix() != 11 {
		t.Errorf("tampon = %d, première = %d", len(r.buffer), r.buffer[0].TS.Unix())
	}
}

func TestBackoffIsCappedAtOneMinute(t *testing.T) {
	var got []time.Duration
	d := time.Duration(0)
	for range 7 {
		d = nextBackoff(d)
		got = append(got, d)
	}
	want := []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second, time.Minute, time.Minute, time.Minute}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("attentes = %v, attendu %v", got, want)
		}
	}
}

// Horloge de la machine en retard d'une heure : Network refuse avec son
// heure, l'agent corrige et renvoie aussitôt, puis date ses mesures à
// l'heure de Network et signale l'écart dans sa fiche.
func TestClockSkewIsCorrected(t *testing.T) {
	serverTime := time.Now().Add(time.Hour)
	r, net := newRunner(t, func(n int, _ protocol.ReportRequest) (int, string) {
		if n == 1 {
			return http.StatusUnauthorized, fmt.Sprintf(
				`{"error":{"code":"clock_skew","message":"horloge","server_time_ms":%d}}`, serverTime.UnixMilli())
		}
		return http.StatusOK, `{"report_interval_s":30}`
	})
	r.collect(context.Background())
	if err := r.Flush(context.Background()); err != nil {
		t.Fatalf("erreur = %v", err)
	}
	if off := r.Client.Offset(); (off - time.Hour).Abs() > 5*time.Second {
		t.Fatalf("correction = %s, attendu 1 h", off)
	}
	if len(net.reports) != 2 {
		t.Fatalf("envois = %d, attendu 2 (refus puis renvoi)", len(net.reports))
	}

	r.factsHash = [32]byte{}
	r.collect(context.Background())
	r.Flush(context.Background())
	last := net.reports[len(net.reports)-1]
	if last.Facts == nil || (last.Facts.ClockOffsetS < 3595 || last.Facts.ClockOffsetS > 3605) {
		t.Errorf("écart dans la fiche = %+v", last.Facts)
	}
	if got := last.Samples[0].TS; got.Before(time.Unix(3600, 0)) {
		t.Errorf("mesure datée %s, attendu décalée d'une heure", got)
	}
}

// Un écart absurde n'est pas rattrapé.
func TestClockSkewBeyondLimitIsNotApplied(t *testing.T) {
	r, net := newRunner(t, func(int, protocol.ReportRequest) (int, string) {
		return http.StatusUnauthorized, fmt.Sprintf(
			`{"error":{"code":"clock_skew","message":"horloge","server_time_ms":%d}}`, time.Now().AddDate(1, 0, 0).UnixMilli())
	})
	if err := r.Flush(context.Background()); err == nil {
		t.Fatal("erreur attendue")
	}
	if r.Client.Offset() != 0 || len(net.reports) != 1 {
		t.Errorf("correction = %s, envois = %d", r.Client.Offset(), len(net.reports))
	}
}
