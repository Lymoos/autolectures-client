//go:build !windows

package config

func protect(string) (string, bool) { return "", false }

func unprotect(string) (string, bool) { return "", false }
