package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

func savePassword(path string, password []byte) ([]byte, error) {
	if len(password) == 0 || len(password) > 72 {
		return nil, errors.New("use a password between 1 and 72 UTF-8 bytes")
	}
	hash, err := bcrypt.GenerateFromPassword(password, bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".admin-password-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(hash); err != nil {
		f.Close()
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	return hash, os.Rename(f.Name(), path)
}

func loadPassword(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("set the initial admin password with tack-bridge passwd -data %s (reads one line from stdin)", filepath.Dir(path))
	}
	if err != nil {
		return nil, err
	}
	value := bytes.TrimSpace(raw)
	if strings.HasPrefix(string(value), "$2") {
		if _, err := bcrypt.Cost(value); err != nil || len(value) != 60 {
			return nil, errors.New("invalid admin password hash")
		}
		return value, nil
	}
	// Earlier bridges stored a generated token here. Replace it atomically.
	return savePassword(path, value)
}
