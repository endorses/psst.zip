package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"time"

	"github.com/endorses/psst.zip/client"
	"github.com/endorses/psst.zip/client/internal/state"
	"github.com/endorses/psst.zip/client/protocol"
)

func authRequired(message string) *Failure {
	return &Failure{Code: "authentication_required", Message: message, exit: ExitAuth}
}

func loadProfile(v *state.Store, name string) (profile, error) {
	var p profile
	if e := v.Get("profiles/"+name, &p); e != nil {
		if errors.Is(e, state.ErrNotFound) {
			return p, authRequired("No account is configured for this profile. Use auth login or auth pair.")
		}
		return p, e
	}
	o, e := protocol.NormalizeOrigin(p.Origin)
	if e != nil || o != p.Origin || !protocol.ValidID(p.Owner) {
		return p, storageFailure("The local account profile is invalid; restore a valid encrypted backup.")
	}
	return p, nil
}

func (a *App) accountClient(ctx context.Context, o options, v *state.Store) (*client.Client, scopedStore, error) {
	p, e := loadProfile(v, o.profile)
	if e != nil {
		return nil, scopedStore{}, e
	}
	if o.server != "" {
		server, e := protocol.NormalizeOrigin(o.server)
		if e != nil || server != p.Origin {
			return nil, scopedStore{}, usage("--server cannot override this account profile's origin. Use a separate profile or a public link.")
		}
	}
	if p.Session == nil || p.Session.Token == "" || p.Session.User.ID != p.Owner {
		return nil, scopedStore{}, authRequired("This profile is signed out. Sign in again; locally held decryption keys remain available.")
	}
	if !p.Session.ExpiresAt.After(time.Now()) {
		return nil, scopedStore{}, authRequired("The device session expired. Sign in again; device sessions currently last 30 days.")
	}
	c, e := client.New(p.Origin, a.HTTP, func(context.Context) (string, error) { return p.Session.Token, nil })
	if e != nil {
		return nil, scopedStore{}, e
	}
	s, e := scope(v, p.Origin, p.Owner)
	return c, s, e
}

func (a *App) authCommand(ctx context.Context, o options, v *state.Store, sub string, user *string, passwordInput *bool, device, jsonFile *string) (any, error) {
	if sub == "status" || sub == "logout" {
		p, e := loadProfile(v, o.profile)
		if e != nil {
			return nil, e
		}
		if sub == "logout" {
			if p.Session != nil {
				c, e := client.New(p.Origin, a.HTTP, nil)
				if e != nil {
					return nil, e
				}
				if e = c.WithToken(p.Session.Token).Logout(ctx); e != nil {
					return nil, e
				}
				p.Session = nil
				if e = v.Put("profiles/"+o.profile, p); e != nil {
					return nil, e
				}
			}
			return map[string]any{"profile": o.profile, "signed_out": true, "private_keys_retained": true}, nil
		}
		c, _, e := a.accountClient(ctx, o, v)
		if e != nil {
			return nil, e
		}
		u, e := c.Me(ctx)
		if e != nil {
			return nil, e
		}
		if u.ID != p.Owner {
			return nil, client.ErrProtocol
		}
		return map[string]any{"profile": o.profile, "origin": p.Origin, "user": u, "expires_at": p.Session.ExpiresAt, "session_id": p.Session.SessionID, "authenticated": true}, nil
	}
	var c *client.Client
	var session protocol.Session
	var e error
	if sub == "login" {
		c, e = client.New(o.server, a.HTTP, nil)
		if e != nil {
			return nil, usage("Login requires a valid server origin.")
		}
		var password []byte
		if *passwordInput {
			password, e = a.readInput(ctx, "-", 4096)
			password = bytes.TrimSuffix(bytes.TrimSuffix(password, []byte("\n")), []byte("\r"))
			if len(password) == 0 && e == nil {
				e = usage("A password cannot be empty.")
			}
		} else {
			password, e = a.secret(ctx, o, -1, "Account password: ")
		}
		if e != nil {
			return nil, e
		}
		defer clear(password)
		session, e = c.Login(ctx, *user, string(password), *device)
	} else {
		b, err := a.readInput(ctx, *jsonFile, 4096)
		if err != nil {
			return nil, err
		}
		if uniqueJSON(b) != nil {
			return nil, usage("Pairing input must be one unambiguous private JSON document.")
		}
		pair, err := client.ParsePairing(bytes.NewReader(b))
		if err != nil {
			return nil, usage("The file is not a supported private psst.zip pairing document.")
		}
		if o.server != "" {
			origin, err := protocol.NormalizeOrigin(o.server)
			if err != nil || origin != pair.ServerURL {
				return nil, usage("Pairing JSON and --server refer to different origins.")
			}
		}
		c, e = client.New(pair.ServerURL, a.HTTP, nil)
		if e == nil {
			session, e = c.Pair(ctx, pair, *device)
		}
	}
	if e != nil {
		return nil, e
	}
	p := profile{Origin: c.Origin(), Owner: session.User.ID, Session: &session}
	if e = v.Put("profiles/"+o.profile, p); e != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = c.WithToken(session.Token).Logout(cleanup)
		return nil, storageFailure("The new session could not be saved; login was not completed.")
	}
	return map[string]any{"profile": o.profile, "origin": p.Origin, "user": session.User, "expires_at": session.ExpiresAt, "authenticated": true}, nil
}

func (a *App) vaultCommand(ctx context.Context, o options, v *state.Store, sub string, output, input *string, backupFD int) (any, error) {
	if backupFD >= 0 && backupFD == o.passFD {
		return nil, usage("Use separate file descriptors for vault and backup credentials.")
	}
	if sub == "backup" && (output == nil || *output == "") || sub == "restore" && (input == nil || *input == "") {
		return nil, usage("Backup needs --output FILE; restore needs --input FILE.")
	}
	pass, e := a.secret(ctx, o, backupFD, "Encrypted backup passphrase: ")
	if e != nil {
		return nil, e
	}
	defer clear(pass)
	if sub == "restore" {
		f, e := os.Open(*input)
		if e != nil {
			return nil, e
		}
		defer f.Close()
		if e = v.Restore(f, pass); e != nil {
			return nil, e
		}
		return map[string]bool{"restored": true, "existing_keys_preserved": true}, nil
	}
	f, e := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return nil, e
	}
	ok := false
	defer func() {
		f.Close()
		if !ok {
			_ = os.Remove(*output)
		}
	}()
	if e = v.Backup(f, pass); e != nil {
		return nil, e
	}
	if e = f.Sync(); e != nil {
		return nil, e
	}
	if e = f.Close(); e != nil {
		return nil, e
	}
	ok = true
	return map[string]bool{"backup_written": true, "encrypted": true}, nil
}
