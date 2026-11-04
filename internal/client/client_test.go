package client

import (
	"context"
	"crypto/ed25519"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Shams2a/horus-agent/internal/protocol"
)

// L'en-tête Date de chaque réponse suffit à suivre l'heure de Network :
// correction dès qu'elle dérive, abandon dès que l'horloge redevient juste.
func TestOffsetFollowsServerDate(t *testing.T) {
	serverClock := time.Now().Add(time.Hour)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Date", serverClock.UTC().Format(http.TimeFormat))
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	_, key, _ := ed25519.GenerateKey(nil)
	c := New(srv.URL)
	c.AgentID, c.Key = "agent-1", key

	if _, err := c.Report(context.Background(), protocol.ReportRequest{AgentVersion: "1.0.0"}); err != nil {
		t.Fatal(err)
	}
	if off := c.Offset(); (off - time.Hour).Abs() > 2*time.Second {
		t.Fatalf("correction = %s, attendu 1 h", off)
	}
	first := c.nextTimestamp()

	// Horloge remise à l'heure : la correction disparaît, sans revenir en arrière.
	serverClock = time.Now()
	c.Report(context.Background(), protocol.ReportRequest{AgentVersion: "1.0.0"})
	if off := c.Offset(); off != 0 {
		t.Errorf("correction = %s, attendu 0", off)
	}
	if next := c.nextTimestamp(); next <= first {
		t.Errorf("horodatage %d revenu en arrière (dernier %d)", next, first)
	}

	// Petit écart : pas de correction.
	serverClock = time.Now().Add(10 * time.Second)
	c.Report(context.Background(), protocol.ReportRequest{AgentVersion: "1.0.0"})
	if off := c.Offset(); off != 0 {
		t.Errorf("écart de 10 s corrigé : %s", off)
	}
}
