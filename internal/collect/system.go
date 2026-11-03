package collect

import (
	"errors"
	"strconv"
	"strings"

	"github.com/Shams2a/horus-agent/internal/protocol"
)

type cpuTimes struct {
	idle, total uint64
}

func (c *Collector) readCPU() (cpuTimes, error) {
	raw, err := c.read("proc/stat")
	if err != nil {
		return cpuTimes{}, err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Fields(line)
		if len(f) < 5 || f[0] != "cpu" {
			continue
		}
		var t cpuTimes
		// user nice system idle iowait irq softirq steal. guest et guest_nice
		// sont déjà comptés dans user et nice.
		for i, v := range f[1:] {
			if i >= 8 {
				break
			}
			n, _ := strconv.ParseUint(v, 10, 64)
			t.total += n
			if i == 3 || i == 4 {
				t.idle += n
			}
		}
		return t, nil
	}
	return cpuTimes{}, errors.New("ligne cpu absente de /proc/stat")
}

func (c *Collector) cpu() *protocol.CPU {
	out := &protocol.CPU{}
	if t, err := c.readCPU(); err == nil {
		if prev := c.prevCPU; prev != nil && t.total > prev.total {
			busy := float64((t.total-prev.total)-(t.idle-prev.idle)) / float64(t.total-prev.total)
			out.UsagePct = round2(busy * 100)
		}
		c.prevCPU = &t
	}
	if raw, err := c.read("proc/loadavg"); err == nil {
		if f := strings.Fields(string(raw)); len(f) >= 3 {
			out.Load1, _ = strconv.ParseFloat(f[0], 64)
			out.Load5, _ = strconv.ParseFloat(f[1], 64)
			out.Load15, _ = strconv.ParseFloat(f[2], 64)
		}
	}
	return out
}

// meminfo renvoie les valeurs de /proc/meminfo, en octets.
func (c *Collector) meminfo() map[string]uint64 {
	out := map[string]uint64{}
	raw, err := c.read("proc/meminfo")
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(raw), "\n") {
		key, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) == 0 {
			continue
		}
		n, err := strconv.ParseUint(f[0], 10, 64)
		if err != nil {
			continue
		}
		if len(f) > 1 && f[1] == "kB" {
			n *= 1024
		}
		out[key] = n
	}
	return out
}

// memory : la mémoire utilisée exclut le cache et les tampons (MemTotal - MemAvailable).
func (c *Collector) memory() *protocol.Memory {
	m := c.meminfo()
	out := &protocol.Memory{TotalBytes: m["MemTotal"], SwapTotalBytes: m["SwapTotal"]}
	if m["MemTotal"] >= m["MemAvailable"] {
		out.UsedBytes = m["MemTotal"] - m["MemAvailable"]
	}
	if m["SwapTotal"] >= m["SwapFree"] {
		out.SwapUsedBytes = m["SwapTotal"] - m["SwapFree"]
	}
	return out
}

func (c *Collector) mounts() []mount {
	raw, err := c.read("proc/self/mounts")
	if err != nil {
		return nil
	}
	var out []mount
	for _, m := range parseMounts(raw) {
		if c.IsDir(m.point) {
			out = append(out, m)
		}
	}
	return out
}

func (c *Collector) diskUsage() []protocol.DiskUsage {
	var out []protocol.DiskUsage
	for _, m := range c.mounts() {
		total, free, err := c.Statfs(m.point)
		if err != nil || total == 0 {
			continue
		}
		out = append(out, protocol.DiskUsage{Mount: m.point, UsedBytes: total - free, TotalBytes: total})
	}
	return out
}

type netCounters struct {
	rx, tx uint64
}

func (c *Collector) readNet() (map[string]netCounters, error) {
	raw, err := c.read("proc/net/dev")
	if err != nil {
		return nil, err
	}
	out := map[string]netCounters{}
	for _, line := range strings.Split(string(raw), "\n") {
		name, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		f := strings.Fields(rest)
		if len(f) < 9 || ignoredInterface(name) {
			continue
		}
		rx, _ := strconv.ParseUint(f[0], 10, 64)
		tx, _ := strconv.ParseUint(f[8], 10, 64)
		out[name] = netCounters{rx: rx, tx: tx}
	}
	return out, nil
}

// network : débits moyens depuis la mesure précédente.
func (c *Collector) network() []protocol.NetRate {
	now := c.Now()
	current, err := c.readNet()
	if err != nil {
		return nil
	}
	prev, prevAt := c.prevNet, c.prevNetAt
	c.prevNet, c.prevNetAt = current, now
	elapsed := now.Sub(prevAt).Seconds()
	if prev == nil || elapsed <= 0 {
		return nil
	}
	var out []protocol.NetRate
	for name, cur := range current {
		old, ok := prev[name]
		// Compteur remis à zéro (interface redémarrée) : on saute un tour.
		if !ok || cur.rx < old.rx || cur.tx < old.tx {
			continue
		}
		out = append(out, protocol.NetRate{
			Iface: name,
			RxBps: uint64(float64(cur.rx-old.rx) / elapsed),
			TxBps: uint64(float64(cur.tx-old.tx) / elapsed),
		})
	}
	sortRates(out)
	return out
}

func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}
