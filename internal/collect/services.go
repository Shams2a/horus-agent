package collect

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Shams2a/horus-agent/internal/protocol"
)

// systemd liste les unités en échec. Nil (bloc absent) si systemctl est
// indisponible : on ne confond pas « non mesuré » et « aucune unité en échec ».
func (c *Collector) systemd(ctx context.Context) *protocol.Systemd {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := c.Exec(ctx, "systemctl", "list-units", "--state=failed", "--plain", "--no-legend", "--no-pager")
	if err != nil {
		return nil
	}
	return &protocol.Systemd{FailedUnits: parseFailedUnits(out)}
}

func parseFailedUnits(raw []byte) []string {
	units := []string{}
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		// Certaines versions de systemctl préfixent encore la ligne par un point.
		name := f[0]
		if (name == "●" || name == "*") && len(f) > 1 {
			name = f[1]
		}
		units = append(units, name)
	}
	return units
}

type dockerContainer struct {
	ID    string   `json:"Id"`
	Names []string `json:"Names"`
	Image string   `json:"Image"`
	State string   `json:"State"`
}

type dockerInspect struct {
	RestartCount int `json:"RestartCount"`
}

// docker interroge le socket Docker en lecture seule (liste et inspection).
func (c *Collector) docker(ctx context.Context) *protocol.Docker {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	httpc := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", c.DockerSocket)
		},
	}}
	defer httpc.CloseIdleConnections()

	var list []dockerContainer
	if err := dockerGet(ctx, httpc, "/containers/json?all=1", &list); err != nil {
		return &protocol.Docker{Error: "socket_unavailable"}
	}
	out := &protocol.Docker{Containers: []protocol.Container{}}
	for _, ct := range list {
		var details dockerInspect
		_ = dockerGet(ctx, httpc, "/containers/"+ct.ID+"/json", &details)
		name := ""
		if len(ct.Names) > 0 {
			name = strings.TrimPrefix(ct.Names[0], "/")
		}
		id := ct.ID
		if len(id) > 12 {
			id = id[:12]
		}
		out.Containers = append(out.Containers, protocol.Container{
			ID: id, Name: name, Image: ct.Image, State: ct.State, RestartCount: details.RestartCount,
		})
	}
	return out
}

func dockerGet(ctx context.Context, httpc *http.Client, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker"+path, nil)
	if err != nil {
		return err
	}
	resp, err := httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("docker : statut %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
