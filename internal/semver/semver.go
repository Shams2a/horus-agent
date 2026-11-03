// Package semver compare des numéros de version (MAJEUR.MINEUR.CORRECTIF[-pré]).
package semver

import (
	"strconv"
	"strings"
)

type version struct {
	core [3]int
	pre  []string
}

func parse(v string) (version, bool) {
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	var out version
	core, pre, hasPre := strings.Cut(v, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || (len(p) > 1 && p[0] == '0') {
			return out, false
		}
		out.core[i] = n
	}
	if hasPre {
		if pre == "" {
			return out, false
		}
		out.pre = strings.Split(pre, ".")
	}
	return out, true
}

// Valid indique si v est un numéro de version semver.
func Valid(v string) bool {
	_, ok := parse(v)
	return ok
}

// Compare renvoie -1, 0 ou 1. Une version illisible passe avant toutes les autres.
func Compare(a, b string) int {
	va, okA := parse(a)
	vb, okB := parse(b)
	switch {
	case !okA && !okB:
		return 0
	case !okA:
		return -1
	case !okB:
		return 1
	}
	for i := range 3 {
		if va.core[i] != vb.core[i] {
			return cmpInt(va.core[i], vb.core[i])
		}
	}
	// Une préversion passe avant la version finale.
	switch {
	case va.pre == nil && vb.pre == nil:
		return 0
	case va.pre == nil:
		return 1
	case vb.pre == nil:
		return -1
	}
	for i := 0; i < len(va.pre) && i < len(vb.pre); i++ {
		if c := cmpIdent(va.pre[i], vb.pre[i]); c != 0 {
			return c
		}
	}
	return cmpInt(len(va.pre), len(vb.pre))
}

// IsNewer indique si candidate est strictement plus récente que current.
func IsNewer(candidate, current string) bool {
	return Valid(candidate) && Compare(candidate, current) > 0
}

func cmpIdent(a, b string) int {
	na, errA := strconv.Atoi(a)
	nb, errB := strconv.Atoi(b)
	switch {
	case errA == nil && errB == nil:
		return cmpInt(na, nb)
	case errA == nil:
		return -1
	case errB == nil:
		return 1
	}
	return strings.Compare(a, b)
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
