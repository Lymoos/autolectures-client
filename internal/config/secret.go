package config

import (
	"encoding/base64"
	"strings"
)

const (
	secretPrefix = "dpapi:"
	obfKey       = "autolectures-local-key"
)

func sealSecret(plain string) string {
	if plain == "" {
		return ""
	}
	if sealed, ok := protect(plain); ok {
		return secretPrefix + sealed
	}
	return obfuscate(plain)
}

func openSecret(stored string) (string, bool) {
	if stored == "" {
		return "", true
	}
	if rest, cut := strings.CutPrefix(stored, secretPrefix); cut {
		return unprotect(rest)
	}
	return deobfuscate(stored), true
}

func sealPlain(plain string) string {
	if plain == "" {
		return ""
	}
	if sealed, ok := protect(plain); ok {
		return secretPrefix + sealed
	}
	return plain
}

func openPlain(stored string) (string, bool) {
	if rest, cut := strings.CutPrefix(stored, secretPrefix); cut {
		return unprotect(rest)
	}
	return stored, true
}

func obfuscate(plain string) string {
	if plain == "" {
		return ""
	}
	b := []byte(plain)
	for i := range b {
		b[i] ^= obfKey[i%len(obfKey)]
	}
	return base64.StdEncoding.EncodeToString(b)
}

func deobfuscate(stored string) string {
	if stored == "" {
		return ""
	}
	b, err := base64.StdEncoding.DecodeString(stored)
	if err != nil {
		return ""
	}
	for i := range b {
		b[i] ^= obfKey[i%len(obfKey)]
	}
	return string(b)
}
