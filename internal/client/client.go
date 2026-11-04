// Package client parle à l'API agent de Network (docs/protocole.md).
package client

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Shams2a/horus-agent/internal/protocol"
	"github.com/Shams2a/horus-agent/internal/version"
)

// APIError est une réponse d'erreur de Network, avec son code stable.
type APIError struct {
	Status       int
	Code         string
	Message      string
	ServerTimeMS int64
}

func (e *APIError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("Network a répondu %d", e.Status)
	}
	return fmt.Sprintf("Network a répondu %d (%s) : %s", e.Status, e.Code, e.Message)
}

// IsCode indique si err est une APIError portant ce code.
func IsCode(err error, code string) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Code == code
}

// StatusOf renvoie le statut HTTP d'une APIError, 0 sinon (erreur réseau).
func StatusOf(err error) int {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Status
	}
	return 0
}

// Client envoie les requêtes, signées dès que AgentID et Key sont connus.
type Client struct {
	BaseURL string
	AgentID string
	Key     ed25519.PrivateKey
	HTTP    *http.Client
	// Now est remplaçable en test.
	Now func() time.Time

	mu     sync.Mutex
	lastTS int64
	// offset : correction d'horloge apprise de Network (clock_skew).
	offset time.Duration
}

// maxClockOffset borne la correction acceptée : au-delà, l'horloge est trop
// fausse pour qu'on la rattrape sans risque.
const maxClockOffset = 30 * 24 * time.Hour

// clockTolerance : écart en deçà duquel l'horloge est tenue pour juste.
// L'en-tête Date est à la seconde, et Network tolère 5 minutes.
const clockTolerance = 30 * time.Second

// observeDate suit l'heure de Network d'après l'en-tête Date de chaque
// réponse : la correction s'ajuste dès que l'horloge de la machine dérive,
// et disparaît dès qu'elle redevient juste, sans attendre un refus
// clock_skew. Le dernier horodatage n'est pas remis à zéro : Network refuse
// tout horodatage inférieur au dernier accepté.
func (c *Client) observeDate(header string, sent, received time.Time) {
	server, err := http.ParseTime(header)
	if err != nil {
		return
	}
	measured := server.Sub(sent.Add(received.Sub(sent) / 2))
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case measured.Abs() < clockTolerance:
		c.offset = 0
	case measured.Abs() > maxClockOffset:
	case (measured - c.offset).Abs() >= clockTolerance:
		c.offset = measured.Round(time.Second)
	}
}

// Offset renvoie la correction d'horloge en cours (0 si l'horloge est juste).
func (c *Client) Offset() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.offset
}

// New prépare un client. Le transport par défaut respecte HTTPS_PROXY et NO_PROXY.
func New(baseURL string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP:    &http.Client{Timeout: 30 * time.Second},
		Now:     time.Now,
	}
}

// nextTimestamp garantit un horodatage strictement croissant (§ 2).
func (c *Client) nextTimestamp() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	ts := c.Now().Add(c.offset).UnixMilli()
	if ts <= c.lastTS {
		ts = c.lastTS + 1
	}
	c.lastTS = ts
	return ts
}

func (c *Client) post(ctx context.Context, path string, in, out any, signed bool) error {
	err := c.send(ctx, path, in, out, signed)
	// Horloge trop décalée : Network renvoie son heure. On corrige et on
	// renvoie une fois, avec un horodatage et une signature refaits.
	var apiErr *APIError
	if signed && errors.As(err, &apiErr) && apiErr.Code == protocol.CodeClockSkew && apiErr.ServerTimeMS > 0 {
		offset := time.UnixMilli(apiErr.ServerTimeMS).Sub(c.Now())
		if offset.Abs() > maxClockOffset {
			return err
		}
		c.mu.Lock()
		c.offset = offset
		c.lastTS = 0
		c.mu.Unlock()
		return c.send(ctx, path, in, out, signed)
	}
	return err
}

func (c *Client) send(ctx context.Context, path string, in, out any, signed bool) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	target, err := url.Parse(c.BaseURL + path)
	if err != nil {
		return fmt.Errorf("adresse de Network invalide : %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", fmt.Sprintf("horus-agent/%s (linux; %s)", version.Version, runtime.GOARCH))
	if signed {
		if c.AgentID == "" || c.Key == nil {
			return errors.New("agent non inscrit")
		}
		ts := c.nextTimestamp()
		// On signe le chemin réellement appelé, préfixe éventuel de api_url compris.
		req.Header.Set(protocol.HeaderAgent, c.AgentID)
		req.Header.Set(protocol.HeaderTimestamp, strconv.FormatInt(ts, 10))
		req.Header.Set(protocol.HeaderSignature, protocol.Sign(c.Key, http.MethodPost, target.EscapedPath(), ts, body))
	}

	sent := c.Now()
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if signed {
		c.observeDate(resp.Header.Get("Date"), sent, c.Now())
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		apiErr := &APIError{Status: resp.StatusCode}
		var eb protocol.ErrorBody
		if json.Unmarshal(raw, &eb) == nil {
			apiErr.Code, apiErr.Message = eb.Error.Code, eb.Error.Message
			apiErr.ServerTimeMS = eb.Error.ServerTimeMS
		}
		return apiErr
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("réponse de Network illisible : %w", err)
	}
	return nil
}

func (c *Client) Preview(ctx context.Context, req protocol.PreviewRequest) (*protocol.PreviewResponse, error) {
	var out protocol.PreviewResponse
	return &out, c.post(ctx, protocol.PathPreview, req, &out, false)
}

func (c *Client) Enroll(ctx context.Context, req protocol.EnrollRequest) (*protocol.EnrollResponse, error) {
	var out protocol.EnrollResponse
	return &out, c.post(ctx, protocol.PathEnroll, req, &out, false)
}

func (c *Client) Report(ctx context.Context, req protocol.ReportRequest) (*protocol.ReportResponse, error) {
	if req.Samples == nil {
		req.Samples = []protocol.Sample{}
	}
	var out protocol.ReportResponse
	return &out, c.post(ctx, protocol.PathReport, req, &out, true)
}

func (c *Client) Uninstall(ctx context.Context) error {
	return c.post(ctx, protocol.PathUninstall, struct{}{}, nil, true)
}
