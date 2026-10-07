package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"zongheng-vpn/hub/admin/internal/db"
)

var invalidInput = errors.New("invalid_input_file")

func checkParents(path string, missing bool) error {
	absolute, e := filepath.Abs(path)
	if e != nil {
		return invalidInput
	}
	for parent := filepath.Dir(absolute); ; parent = filepath.Dir(parent) {
		info, e := os.Lstat(parent)
		if e != nil {
			if !(missing && os.IsNotExist(e)) {
				return invalidInput
			}
		} else if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !ordinaryDirectory(parent) {
			return invalidInput
		}
		next := filepath.Dir(parent)
		if next == parent {
			break
		}
	}
	return nil
}
func readInventoryInput(path string) ([]byte, error) {
	if !locationAllowed(path) || checkParents(path, false) != nil {
		return nil, invalidInput
	}
	before, e := os.Lstat(path)
	if e != nil || !before.Mode().IsRegular() || before.Size() < 1 || before.Size() > db.MaxInventoryBytes {
		return nil, invalidInput
	}
	f, e := openOrdinary(path)
	if e != nil {
		return nil, invalidInput
	}
	defer f.Close()
	opened, e := f.Stat()
	if e != nil || !os.SameFile(before, opened) || opened.Size() != before.Size() || !opened.ModTime().Equal(before.ModTime()) {
		return nil, invalidInput
	}
	raw, e := io.ReadAll(io.LimitReader(f, db.MaxInventoryBytes+1))
	after, ae := f.Stat()
	if e != nil || ae != nil || len(raw) > db.MaxInventoryBytes || !os.SameFile(opened, after) || opened.Size() != after.Size() || !opened.ModTime().Equal(after.ModTime()) {
		return nil, invalidInput
	}
	return raw, nil
}
