//go:build windows

package deviceauth

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// A task-temporary junction exercises Windows directory aliases without needing
// symlink privilege, an elevated shell, registry changes or a real user home.
func createDirectoryAlias(alias, target string) error {
	if err := os.Mkdir(alias, 0700); err != nil {
		return err
	}
	p, err := windows.UTF16PtrFromString(alias)
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	sub, err := windows.UTF16FromString(`\??\` + target)
	if err != nil {
		return err
	}
	print, err := windows.UTF16FromString(target)
	if err != nil {
		return err
	}
	names := append(sub, print...)
	buffer := make([]byte, 16+2*len(names))
	binary.LittleEndian.PutUint32(buffer[0:4], windows.IO_REPARSE_TAG_MOUNT_POINT)
	binary.LittleEndian.PutUint16(buffer[4:6], uint16(len(buffer)-8))
	binary.LittleEndian.PutUint16(buffer[10:12], uint16((len(sub)-1)*2))
	binary.LittleEndian.PutUint16(buffer[12:14], uint16(len(sub)*2))
	binary.LittleEndian.PutUint16(buffer[14:16], uint16((len(print)-1)*2))
	for i, c := range names {
		binary.LittleEndian.PutUint16(buffer[16+2*i:18+2*i], c)
	}
	var returned uint32
	return windows.DeviceIoControl(h, windows.FSCTL_SET_REPARSE_POINT, &buffer[0], uint32(len(buffer)), nil, 0, &returned, nil)
}

func TestWindowsCaseAndShortPathAliasesShareFence(t *testing.T) {
	s, _, path := newTestStore(t)
	t.Run("case", func(t *testing.T) {
		alias := strings.ToUpper(path)
		other, err := New(context.Background(), openTestDB(t, alias), testOptions())
		if err != nil {
			t.Fatal(err)
		}
		if other.fencePath != s.fencePath {
			t.Fatalf("case alias split fence: %s vs %s", other.fencePath, s.fencePath)
		}
	})
	t.Run("8dot3", func(t *testing.T) {
		p, err := windows.UTF16PtrFromString(path)
		if err != nil {
			t.Fatal(err)
		}
		buffer := make([]uint16, 32768)
		n, err := windows.GetShortPathName(p, &buffer[0], uint32(len(buffer)))
		if err != nil {
			t.Fatal(err)
		}
		alias := windows.UTF16ToString(buffer[:n])
		if strings.EqualFold(alias, path) {
			t.Skip("8.3 aliases are disabled on this fixture volume")
		}
		other, err := New(context.Background(), openTestDB(t, alias), testOptions())
		if err != nil {
			t.Fatal(err)
		}
		if other.fencePath != s.fencePath {
			t.Fatalf("8.3 alias split fence: %s vs %s", other.fencePath, s.fencePath)
		}
	})
}

func TestWindowsRejectsUNCAndReparseFenceBeforeMutation(t *testing.T) {
	if _, err := openRegular(`\\synthetic-invalid-host\share\authority.sqlite`, true); !errors.Is(err, ErrPolicy) {
		t.Fatalf("UNC must reject without network access: %v", err)
	}
	s, _, path := newTestStore(t)
	fence := path + ".deviceauth.lock"
	if err := os.Remove(fence); err != nil {
		t.Fatal(err)
	}
	if err := createDirectoryAlias(fence, filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	if err := s.Enroll(context.Background(), Enrollment{DeviceID: "blocked", OwnerID: "owner", Role: "customer", ValidUntil: testNow.Add(time.Hour)}); err == nil {
		t.Fatalf("reparse fence accepted: %v", err)
	}
	if _, err := s.Device(context.Background(), "blocked"); err == nil {
		t.Fatal("rejected reparse fence still committed enrollment")
	}
}

func TestWindowsPinsDatabaseAgainstRenameForWholeFence(t *testing.T) {
	s, db, path := newTestStore(t)
	db.SetMaxIdleConns(0)
	unlock, err := s.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(filepath.Dir(path), "replacement.sqlite")
	if err := os.Rename(path, archive); err == nil {
		unlock()
		t.Fatal("database could be replaced while trusted operation held fence")
	}
	unlock()
	// Confirm the failure came from the held operation handle, rather than an
	// idle SQLite connection or an unrelated permanent filesystem restriction.
	if err := os.Rename(path, archive); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(archive, path); err != nil {
		t.Fatal(err)
	}
}
