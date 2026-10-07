package updateclient

import (
	"context"
	"io"
	"os"
)

// Inputs are public untrusted bytes, never authority paths. A regular opened
// handle is pinned and matched to the no-symlink Lstat identity before reading.
// The signed digest/approved raw SHA remains the actual content trust boundary.
func openInput(path string, limit int64) (*os.File, os.FileInfo, error) {
	if !inputLocationAllowed(path) {
		return nil, nil, fail("invalid_input_file")
	}
	before, e := os.Lstat(path)
	if e != nil || !before.Mode().IsRegular() || before.Size() < 0 || before.Size() > limit {
		return nil, nil, fail("invalid_input_file")
	}
	f, e := openRegularNoFollow(path)
	if e != nil {
		return nil, nil, fail("invalid_input_file")
	}
	after, e := f.Stat()
	if e != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		f.Close()
		return nil, nil, fail("invalid_input_file")
	}
	return f, after, nil
}

func unchanged(f *os.File, before os.FileInfo) bool {
	after, e := f.Stat()
	return e == nil && os.SameFile(before, after) && after.Size() == before.Size() && after.ModTime().Equal(before.ModTime())
}

func readInput(ctx context.Context, path string, limit int) ([]byte, error) {
	if ctx.Err() != nil {
		return nil, fail("command_cancelled")
	}
	f, before, e := openInput(path, int64(limit))
	if e != nil {
		return nil, e
	}
	defer f.Close()
	raw, e := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if e != nil || len(raw) > limit || !unchanged(f, before) {
		return nil, fail("invalid_input_file")
	}
	if ctx.Err() != nil {
		return nil, fail("command_cancelled")
	}
	return raw, nil
}
