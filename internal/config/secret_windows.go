//go:build windows

package config

import (
	"encoding/base64"
	"unsafe"

	"golang.org/x/sys/windows"
)

var entropy = []byte("Autolectures/config/v1")

func blob(b []byte) *windows.DataBlob {
	if len(b) == 0 {
		return &windows.DataBlob{}
	}
	return &windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
}

func take(out windows.DataBlob) []byte {
	if out.Data == nil {
		return nil
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...)
}

func protect(plain string) (string, bool) {
	in := []byte(plain)
	ent := entropy
	var out windows.DataBlob
	if windows.CryptProtectData(blob(in), nil, blob(ent), 0, nil, 0, &out) != nil {
		return "", false
	}
	return base64.StdEncoding.EncodeToString(take(out)), true
}

func unprotect(stored string) (string, bool) {
	raw, err := base64.StdEncoding.DecodeString(stored)
	if err != nil || len(raw) == 0 {
		return "", false
	}
	ent := entropy
	var out windows.DataBlob
	if windows.CryptUnprotectData(blob(raw), nil, blob(ent), 0, nil, 0, &out) != nil {
		return "", false
	}
	return string(take(out)), true
}
