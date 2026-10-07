package deviceauth

import (
	"io"
	"os"
)

// Sources stay pinned until the whole plan finishes. SQLite receives only a
// private copy of bytes read from this authenticated handle, never its pathname.
type restoreInput struct {
	file    *os.File
	parents []*os.File
	info    os.FileInfo
	max     int64
	source  RestoreSource
}

func (r *restoreInput) Close() {
	if r == nil {
		return
	}
	r.file.Close()
	for i := len(r.parents) - 1; i >= 0; i-- {
		r.parents[i].Close()
	}
}
func (r *restoreInput) read() ([]byte, error) {
	if err := restoreVerifyPinned(r); err != nil {
		return nil, err
	}
	before, err := r.file.Stat()
	if err != nil || before.Size() <= 0 || before.Size() > r.max {
		return nil, restoreFailure("offline_file_size")
	}
	if _, err = r.file.Seek(0, io.SeekStart); err != nil {
		return nil, restoreFailure("offline_file_read")
	}
	b, err := io.ReadAll(io.LimitReader(r.file, r.max+1))
	if err != nil || int64(len(b)) != before.Size() {
		return nil, restoreFailure("changed_offline_file")
	}
	after, err := r.file.Stat()
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return nil, restoreFailure("changed_offline_file")
	}
	if err = restoreVerifyPinned(r); err != nil {
		return nil, err
	}
	return b, nil
}
func restoreOpenInput(path string, max int64) (*restoreInput, []byte, error) {
	f, parents, err := restoreOpenPrivate(path)
	if err != nil {
		return nil, nil, restoreFailure("unsafe_offline_input")
	}
	r := &restoreInput{file: f, parents: parents, max: max}
	r.info, err = f.Stat()
	if err != nil {
		r.Close()
		return nil, nil, restoreFailure("offline_input_identity")
	}
	b, err := r.read()
	if err != nil {
		r.Close()
		return nil, nil, err
	}
	r.source = RestoreSource{SHA256: restoreDigest(b), Bytes: int64(len(b))}
	return r, b, nil
}
func (r *restoreInput) unchanged() error {
	b, err := r.read()
	if err != nil {
		return err
	}
	if int64(len(b)) != r.source.Bytes || restoreDigest(b) != r.source.SHA256 {
		return restoreFailure("changed_plan_input")
	}
	return nil
}
