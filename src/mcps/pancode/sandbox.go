package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var (
	workDir    string
	sandboxDir string
	tmpDir     string
)

// InitSandbox initializes the working directory and sandbox directory.
// If either path is empty, reasonable defaults are used (cwd and os.TempDir()).
func InitSandbox(wd, sd string) error {
	var err error
	if wd == "" {
		wd, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get cwd: %w", err)
		}
	}
	if sd == "" {
		sd = os.TempDir()
	}

	workDir, err = filepath.Abs(filepath.Clean(wd))
	if err != nil {
		return fmt.Errorf("invalid workdir: %w", err)
	}
	sandboxDir, err = filepath.Abs(filepath.Clean(sd))
	if err != nil {
		return fmt.Errorf("invalid sandboxdir: %w", err)
	}
	tmpDir, err = filepath.Abs(filepath.Clean(os.TempDir()))
	if err != nil {
		return fmt.Errorf("invalid tmpdir: %w", err)
	}

	return nil
}

// resolvePath returns an absolute path for p. If p is relative, it is
// interpreted relative to the configured workDir.
func resolvePath(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("empty path")
	}
	var abs string
	var err error
	if filepath.IsAbs(p) {
		abs, err = filepath.Abs(filepath.Clean(p))
	} else {
		abs, err = filepath.Abs(filepath.Join(workDir, p))
	}
	if err != nil {
		return "", err
	}
	return abs, nil
}

// isWithin reports whether target is inside base (or equal to it).
func isWithin(base, target string) bool {
	base = filepath.Clean(base)
	target = filepath.Clean(target)
	if base == target {
		return true
	}
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	return !strings.HasPrefix(rel, "..") && rel != ".."
}

// AllowedPath verifies that the given path is permitted to be accessed.
// It returns the resolved absolute path or an error if not allowed.
// Relative paths are resolved against the configured workDir.
func AllowedPath(p string) (string, error) {
	abs, err := resolvePath(p)
	if err != nil {
		return "", err
	}

	// allow paths inside workDir, sandboxDir, or tmpDir
	if isWithin(workDir, abs) || isWithin(sandboxDir, abs) || isWithin(tmpDir, abs) {
		return abs, nil
	}

	return "", fmt.Errorf("path %s is outside allowed directories", abs)
}

// FilterAllowed filters a list of file paths, returning only those allowed.
func FilterAllowed(paths []string) []string {
	var out []string
	for _, p := range paths {
		if abs, err := AllowedPath(p); err == nil {
			out = append(out, abs)
		}
	}
	return out
}
