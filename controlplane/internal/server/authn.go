package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/ORG/gearbox/controlplane/internal/auth"
	"github.com/ORG/gearbox/controlplane/internal/model"
	"github.com/ORG/gearbox/controlplane/internal/store"
)

const (
	sessionCookie = "gbx_session"
	stateCookie   = "gbx_oidc"
	patPrefix     = "gbx_pat_"
	credPrefix    = "gbx_cred_"
)

// Principal is the authenticated caller.
type Principal struct {
	Member  model.Member
	Kind    string // dev | session | pat | credential
	Session *model.WebSession
	// Credential callers.
	CredID, CredApp, CredEnv string
}

// Actor is the name recorded in audit and change requests.
func (p *Principal) Actor() string {
	if p == nil {
		return "anonymous"
	}
	if p.Kind == "credential" {
		return "credential:" + p.CredID
	}
	return p.Member.Email
}

// allowedApp reports whether the member's app scope includes app.
func (p *Principal) allowedApp(app string) bool {
	if p.Kind == "credential" {
		return p.CredApp == app
	}
	return app == "" || len(p.Member.Apps) == 0 || slices.Contains(p.Member.Apps, app)
}

// require checks role and app scope.
func (p *Principal) require(role, app string) error {
	if p.Kind == "credential" {
		return errForbidden("App credentials can only call sidecar and runtime endpoints.")
	}
	if model.RoleRank(p.Member.Role) < model.RoleRank(role) {
		return errForbidden("This needs the %s role; you are %s.", role, p.Member.Role)
	}
	if !p.allowedApp(app) {
		return errForbidden("Your role does not cover app %s.", app)
	}
	return nil
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if v, ok := strings.CutPrefix(h, "Bearer "); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func isMutating(m string) bool {
	return m != http.MethodGet && m != http.MethodHead && m != http.MethodOptions
}

type systemKey struct{}

// SystemHandler serves the API in-process as an internal admin ("system"),
// for the seeder and tools running inside the server binary. It is never
// reachable from the network.
func (s *Server) SystemHandler() http.Handler {
	h := s.Handler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), systemKey{}, true)))
	})
}

func (s *Server) authenticate(r *http.Request, rt *route) (*Principal, error) {
	if rt.auth == authNone {
		return nil, nil
	}
	ctx := r.Context()
	if ctx.Value(systemKey{}) == true && bearer(r) == "" {
		return &Principal{Member: model.Member{ID: "system", Email: "system@gearbox.local", Name: "system", Role: model.RoleAdmin, Apps: []string{}}, Kind: "dev"}, nil
	}
	tok := bearer(r)
	switch {
	case strings.HasPrefix(tok, credPrefix):
		if rt.auth == authUser {
			return nil, errForbidden("App credentials can only call sidecar and runtime endpoints.")
		}
		c, err := s.st.CredentialByHash(ctx, s.hasher.Hash(tok))
		if err != nil || c.State != "active" {
			return nil, errUnauthenticated("The app credential is unknown or revoked.")
		}
		s.touchCredential(c)
		return &Principal{Kind: "credential", CredID: c.ID, CredApp: c.App, CredEnv: c.Environment}, nil
	case rt.auth == authCred:
		return nil, errUnauthenticated("Sidecar endpoints need an app credential (Authorization: Bearer gbx_cred_…).")
	case strings.HasPrefix(tok, patPrefix):
		t, err := s.st.TokenByHash(ctx, s.hasher.Hash(tok))
		if err != nil || !t.ExpiresAt.After(s.now()) {
			return nil, errUnauthenticated("The access token is unknown or expired.")
		}
		m, err := s.st.GetMember(ctx, t.MemberID)
		if err != nil {
			return nil, errUnauthenticated("The token's member no longer exists.")
		}
		return &Principal{Member: m, Kind: "pat"}, nil
	case tok != "":
		return nil, errUnauthenticated("Unrecognised bearer token.")
	}
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		ws, err := s.st.GetSession(ctx, s.hasher.Hash(c.Value))
		if err == nil && ws.ExpiresAt.After(s.now()) {
			m, err := s.st.GetMember(ctx, ws.MemberID)
			if err == nil {
				if isMutating(r.Method) && !hmac.Equal([]byte(r.Header.Get("X-CSRF-Token")), []byte(ws.CSRF)) {
					return nil, errForbidden("Missing or wrong X-CSRF-Token (get it from GET /session).")
				}
				return &Principal{Member: m, Kind: "session", Session: &ws}, nil
			}
		}
		if !s.cfg.Dev() {
			return nil, errUnauthenticated("Your session expired; sign in again.")
		}
	}
	if s.cfg.Dev() {
		return s.devPrincipal(ctx, r)
	}
	return nil, errUnauthenticated("Sign in (GET /auth/login) or send a personal access token.")
}

// devPrincipal: every request is the configured dev user unless the
// X-Gearbox-Dev-User header names another (an existing member keeps its
// role; otherwise X-Gearbox-Dev-Role or the default role applies).
func (s *Server) devPrincipal(ctx context.Context, r *http.Request) (*Principal, error) {
	email := r.Header.Get("X-Gearbox-Dev-User")
	if email == "" {
		email = s.cfg.DevUser
	}
	if m, err := s.st.MemberByEmail(ctx, email); err == nil {
		return &Principal{Member: m, Kind: "dev"}, nil
	}
	role := r.Header.Get("X-Gearbox-Dev-Role")
	if role == "" {
		role = s.cfg.DevRole
	}
	if model.RoleRank(role) == 0 {
		return nil, errInvalid("X-Gearbox-Dev-Role must be viewer, author, approver or admin")
	}
	return &Principal{Member: model.Member{ID: "dev_" + strings.ReplaceAll(email, "@", "_"), Email: email, Name: email, Role: role, Apps: []string{}}, Kind: "dev"}, nil
}

// touchCredential records lastUsedAt at most once a minute.
func (s *Server) touchCredential(c model.CredentialRecord) {
	now := s.now()
	if v, ok := s.lastUsed.Load(c.Hash); ok && now.Sub(v.(time.Time)) < time.Minute {
		return
	}
	s.lastUsed.Store(c.Hash, now)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = s.st.UpdateCredential(ctx, c.App, c.Environment, c.ID, func(r *model.CredentialRecord) error {
			r.LastUsedAt = &now
			return nil
		})
	}()
}

// ---------------------------------------------------------------- session endpoints

func safeReturnTo(v string) string {
	if v == "" || !strings.HasPrefix(v, "/") || strings.HasPrefix(v, "//") || strings.Contains(v, "\\") {
		return "/"
	}
	return v
}

func (s *Server) stateKey() []byte {
	if s.cfg.SessionKey != "" {
		return auth.LoadSecret(s.cfg.SessionKey)
	}
	return s.hasher.Derive("oidc-state")
}

func (s *Server) signState(v string) string {
	m := hmac.New(sha256.New, s.stateKey())
	m.Write([]byte(v))
	return base64.RawURLEncoding.EncodeToString([]byte(v)) + "." + hex.EncodeToString(m.Sum(nil))
}

func (s *Server) verifyStateCookie(c string) (string, bool) {
	b64, sig, ok := strings.Cut(c, ".")
	if !ok {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(b64)
	if err != nil {
		return "", false
	}
	m := hmac.New(sha256.New, s.stateKey())
	m.Write(raw)
	return string(raw), hmac.Equal([]byte(sig), []byte(hex.EncodeToString(m.Sum(nil))))
}

func (s *Server) secureCookies() bool { return strings.HasPrefix(s.cfg.PublicURL, "https://") }

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request, _ *Principal) error {
	ret := safeReturnTo(r.URL.Query().Get("returnTo"))
	if s.oidc == nil {
		// Development mode: everyone is already signed in.
		http.Redirect(w, r, ret, http.StatusFound)
		return nil
	}
	state, nonce := auth.RandomString(24), auth.RandomString(24)
	http.SetCookie(w, &http.Cookie{
		Name: stateCookie, Value: s.signState(state + "|" + nonce + "|" + ret), Path: "/api/v1/auth",
		HttpOnly: true, Secure: s.secureCookies(), SameSite: http.SameSiteLaxMode, MaxAge: 600,
	})
	http.Redirect(w, r, s.oidc.AuthURL(state, nonce), http.StatusFound)
	return nil
}

func (s *Server) handleCallback(w http.ResponseWriter, r *http.Request) error {
	if s.oidc == nil {
		http.Redirect(w, r, "/", http.StatusFound)
		return nil
	}
	c, err := r.Cookie(stateCookie)
	if err != nil {
		return errInvalid("Sign-in state is missing; start again at /api/v1/auth/login.")
	}
	raw, ok := s.verifyStateCookie(c.Value)
	parts := strings.SplitN(raw, "|", 3)
	if !ok || len(parts) != 3 || parts[0] != r.URL.Query().Get("state") {
		return errInvalid("Sign-in state does not match; start again.")
	}
	if e := r.URL.Query().Get("error"); e != "" {
		return errUnauthenticated("The identity provider refused sign-in: %s", e)
	}
	id, err := s.oidc.Exchange(r.Context(), r.URL.Query().Get("code"), parts[1])
	if err != nil {
		return errUnauthenticated("Sign-in failed: %v", err)
	}
	ctx := r.Context()
	m, err := s.st.MemberByEmail(ctx, id.Email)
	if errors.Is(err, store.ErrNotFound) {
		if !slices.ContainsFunc(s.cfg.AdminEmails, func(e string) bool { return strings.EqualFold(e, id.Email) }) {
			return errForbidden("%s is not a member of this Gearbox installation; ask an admin to add you.", id.Email)
		}
		m = model.Member{ID: auth.RandomID("mem_"), Email: id.Email, Name: id.Name, Role: model.RoleAdmin, Apps: []string{}}
		if err := s.st.CreateMember(ctx, m); err != nil {
			return err
		}
		s.audit(ctx, &Principal{Member: m, Kind: "session"}, "member.bootstrapped", "", "", "members/"+m.ID, nil)
	} else if err != nil {
		return err
	}
	cookie := auth.RandomString(40)
	ws := model.WebSession{ID: s.hasher.Hash(cookie), MemberID: m.ID, CSRF: auth.RandomString(32), CreatedAt: s.now(), ExpiresAt: s.now().Add(s.cfg.SessionTTL)}
	if err := s.st.CreateSession(ctx, ws); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: stateCookie, Value: "", Path: "/api/v1/auth", MaxAge: -1})
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: cookie, Path: "/", HttpOnly: true, Secure: s.secureCookies(),
		SameSite: http.SameSiteLaxMode, Expires: ws.ExpiresAt,
	})
	http.Redirect(w, r, safeReturnTo(parts[2]), http.StatusFound)
	return nil
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request, p *Principal) error {
	if p.Session != nil {
		if err := s.st.DeleteSession(r.Context(), p.Session.ID); err != nil {
			return err
		}
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.secureCookies(), SameSite: http.SameSiteLaxMode})
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request, p *Principal) error {
	if p.Kind == "credential" {
		return errForbidden("App credentials have no session.")
	}
	csrf := "dev"
	if p.Session != nil {
		csrf = p.Session.CSRF
	} else if p.Kind == "pat" {
		csrf = "not-needed-for-tokens"
	}
	m := p.Member
	if m.Apps == nil {
		m.Apps = []string{}
	}
	writeJSON(w, http.StatusOK, model.Session{Member: m, CSRFToken: csrf})
	return nil
}

// absoluteURL joins the public URL and a path.
func (s *Server) absoluteURL(path string) string {
	u, err := url.Parse(s.cfg.PublicURL)
	if err != nil {
		return path
	}
	return u.JoinPath(path).String()
}
