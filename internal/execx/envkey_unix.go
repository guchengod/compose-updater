//go:build linux || darwin

package execx

func normalizeEnvKey(key string) string { return key }
