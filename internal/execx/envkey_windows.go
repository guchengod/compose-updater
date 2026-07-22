//go:build windows

package execx

import "strings"

func normalizeEnvKey(key string) string { return strings.ToUpper(key) }
