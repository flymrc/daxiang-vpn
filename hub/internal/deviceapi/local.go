package deviceapi

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"zongheng-vpn/hub/internal/deviceauth"
)

// OpenLocalAuthority is used by a trusted Unix local issuer, with the same exact
// policy and DB fence as the opted-in API. It creates no listener or WG adapter.
func OpenLocalAuthority(ctx context.Context, dbPath, policyPath string) (*deviceauth.Store, *sql.DB, error) {
	if !filepath.IsAbs(dbPath) {
		return nil, nil, deviceauth.ErrInvalid
	}
	if err := privateHostingDirectory(filepath.Dir(dbPath)); err != nil {
		return nil, nil, err
	}
	f, err := os.OpenFile(dbPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err == nil {
		err = f.Close()
	} else if errors.Is(err, os.ErrExist) {
		err = nil
	}
	if err != nil {
		return nil, nil, err
	}
	p, err := ReadPolicy(policyPath)
	if err != nil {
		return nil, nil, err
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, nil, err
	}
	s, err := deviceauth.New(ctx, db, deviceauth.Options{Policy: p})
	if err != nil {
		db.Close()
		return nil, nil, err
	}
	return s, db, nil
}
