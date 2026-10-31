package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/ORG/gearbox/controlplane/internal/auth"
	"github.com/ORG/gearbox/controlplane/internal/model"
	"github.com/ORG/gearbox/controlplane/internal/store"
)

func (s *Server) audit(ctx context.Context, p *Principal, action, app, env, resource string, details map[string]any) {
	s.auditBG(ctx, p, action, app, env, resource, false, details)
}

func (s *Server) auditBG(ctx context.Context, p *Principal, action, app, env, resource string, breakGlass bool, details map[string]any) {
	ev := model.AuditEvent{At: s.now(), Actor: p.Actor(), Action: action, App: app, Environment: env, Resource: resource, BreakGlass: breakGlass, Details: details}
	if _, err := s.st.AppendAudit(context.WithoutCancel(ctx), ev); err != nil {
		slog.ErrorContext(ctx, "audit write failed", "action", action, "err", err)
	}
}

// loadApp returns the app or 404, checking the caller's app scope.
func (s *Server) loadApp(ctx context.Context, p *Principal, name string) (model.App, error) {
	if p != nil && !p.allowedApp(name) {
		return model.App{}, errNotFound("App %s does not exist.", name)
	}
	a, err := s.st.GetApp(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		return a, errNotFound("App %s does not exist.", name)
	}
	if a.BundleLimitMB == 0 {
		a.BundleLimitMB = 64
	}
	return a, err
}

// loadEnv returns the app's environment state or 404.
func (s *Server) loadEnv(ctx context.Context, p *Principal, app, env string) (model.EnvState, int64, error) {
	if _, err := s.loadApp(ctx, p, app); err != nil {
		return model.EnvState{}, 0, err
	}
	st, ver, err := s.st.GetEnv(ctx, app, env)
	if errors.Is(err, store.ErrNotFound) {
		return st, 0, errNotFound("App %s has no environment %s.", app, env)
	}
	return st, ver, err
}

func envAPI(st model.EnvState) model.Environment {
	return model.Environment{Name: st.Name, Policy: st.Policy.API(), Generation: st.Generation, PublishedAt: st.PublishedAt}
}

// ---------------------------------------------------------------- apps

func (s *Server) handleListApps(w http.ResponseWriter, r *http.Request, p *Principal) error {
	pg, err := pagination(r)
	if err != nil {
		return err
	}
	page, err := s.st.ListApps(r.Context(), pg.Cursor, pg.Limit, func(a model.App) bool { return p.allowedApp(a.Name) })
	if err != nil {
		return err
	}
	for i := range page.Items {
		if page.Items[i].BundleLimitMB == 0 {
			page.Items[i].BundleLimitMB = 64
		}
	}
	writeJSON(w, http.StatusOK, list(page.Items, page.Next))
	return nil
}

func (s *Server) handleCreateApp(w http.ResponseWriter, r *http.Request, p *Principal) error {
	if err := p.require(model.RoleAdmin, ""); err != nil {
		return err
	}
	var in model.AppCreate
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	envs := in.Environments
	if len(envs) == 0 {
		envs = []string{"dev", "stage", "prod"}
	}
	seen := map[string]bool{}
	for i, e := range envs {
		if !model.EnvPattern.MatchString(e) || seen[e] {
			return errUnprocessable("VALIDATION_FAILED", "Invalid environments", "Environment names must match ^[a-z][a-z0-9-]{1,30}$ and be unique.").
				with(FieldError{Path: "/environments/" + itoa(int64(i)), Message: "invalid or duplicate environment name"})
		}
		seen[e] = true
	}
	a := model.App{Name: in.Name, Description: in.Description, Owners: in.Owners, Environments: envs, BundleLimitMB: 64, CreatedAt: s.now()}
	if a.Owners == nil {
		a.Owners = []string{}
	}
	states := make([]model.EnvState, 0, len(envs))
	for _, e := range envs {
		states = append(states, model.EnvState{App: a.Name, Name: e, Policy: model.DefaultPolicy(e), Live: map[string]model.LiveEntry{}, KilledGates: map[string]string{}})
	}
	if err := s.st.CreateApp(r.Context(), a, states); err != nil {
		if errors.Is(err, store.ErrExists) {
			return errConflict("NAME_TAKEN", "Name taken", "App %s already exists.", a.Name)
		}
		return err
	}
	s.audit(r.Context(), p, "app.created", a.Name, "", "apps/"+a.Name, nil)
	writeJSON(w, http.StatusCreated, a)
	return nil
}

func (s *Server) handleGetApp(w http.ResponseWriter, r *http.Request, p *Principal) error {
	a, err := s.loadApp(r.Context(), p, r.PathValue("app"))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, a)
	return nil
}

func (s *Server) handleUpdateApp(w http.ResponseWriter, r *http.Request, p *Principal) error {
	name := r.PathValue("app")
	if err := p.require(model.RoleAdmin, name); err != nil {
		return err
	}
	if _, err := s.loadApp(r.Context(), p, name); err != nil {
		return err
	}
	var in model.AppUpdate
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	a, err := s.st.UpdateApp(r.Context(), name, func(a *model.App) error {
		if in.Description != nil {
			a.Description = *in.Description
		}
		if in.Owners != nil {
			a.Owners = *in.Owners
		}
		if in.BundleLimitMB != nil {
			a.BundleLimitMB = *in.BundleLimitMB
		}
		return nil
	})
	if err != nil {
		return err
	}
	if a.BundleLimitMB == 0 {
		a.BundleLimitMB = 64
	}
	s.audit(r.Context(), p, "app.updated", name, "", "apps/"+name, nil)
	writeJSON(w, http.StatusOK, a)
	return nil
}

func (s *Server) handleListEnvironments(w http.ResponseWriter, r *http.Request, p *Principal) error {
	a, err := s.loadApp(r.Context(), p, r.PathValue("app"))
	if err != nil {
		return err
	}
	out := []model.Environment{}
	for _, name := range a.Environments {
		st, _, err := s.st.GetEnv(r.Context(), a.Name, name)
		if err != nil {
			return err
		}
		out = append(out, envAPI(st))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
	return nil
}

func (s *Server) handleUpdateEnvironment(w http.ResponseWriter, r *http.Request, p *Principal) error {
	app, env := r.PathValue("app"), r.PathValue("env")
	if err := p.require(model.RoleAdmin, app); err != nil {
		return err
	}
	if _, _, err := s.loadEnv(r.Context(), p, app, env); err != nil {
		return err
	}
	var in model.EnvironmentPolicy
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	st, err := s.st.UpdateEnvPolicy(r.Context(), app, env, in)
	if err != nil {
		return err
	}
	s.audit(r.Context(), p, "environment.policy_changed", app, env, "environments/"+env, map[string]any{"policy": st.Policy})
	writeJSON(w, http.StatusOK, envAPI(st))
	return nil
}

// ---------------------------------------------------------------- credentials

func (s *Server) handleListCredentials(w http.ResponseWriter, r *http.Request, p *Principal) error {
	app, env := r.PathValue("app"), r.PathValue("env")
	if err := p.require(model.RoleAdmin, app); err != nil {
		return err
	}
	if _, _, err := s.loadEnv(r.Context(), p, app, env); err != nil {
		return err
	}
	recs, err := s.st.ListCredentials(r.Context(), app, env)
	if err != nil {
		return err
	}
	out := make([]model.Credential, 0, len(recs))
	for _, c := range recs {
		out = append(out, c.Credential)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
	return nil
}

// CreateCredential issues a credential; secret "" generates one.
func (s *Server) CreateCredential(ctx context.Context, p *Principal, app, env, label, secret string) (model.CredentialCreated, error) {
	id := auth.RandomID("cred_")
	if secret == "" {
		secret = credPrefix + env + "_" + strings.TrimPrefix(id, "cred_") + "_" + auth.RandomString(32)
	}
	if !strings.HasPrefix(secret, credPrefix) {
		return model.CredentialCreated{}, errInvalid("credential secrets start with %s", credPrefix)
	}
	c := model.CredentialRecord{
		Credential: model.Credential{ID: id, Environment: env, Label: label, State: "active", CreatedAt: s.now(), CreatedBy: p.Actor()},
		App:        app, Hash: s.hasher.Hash(secret),
	}
	if err := s.st.CreateCredential(ctx, c); err != nil {
		if errors.Is(err, store.ErrExists) {
			return model.CredentialCreated{}, errConflict("NAME_TAKEN", "Credential exists", "That credential secret is already registered.")
		}
		return model.CredentialCreated{}, err
	}
	s.audit(ctx, p, "credential.created", app, env, "credentials/"+id, map[string]any{"label": label})
	return model.CredentialCreated{Credential: c.Credential, Secret: secret}, nil
}

func (s *Server) handleCreateCredential(w http.ResponseWriter, r *http.Request, p *Principal) error {
	app, env := r.PathValue("app"), r.PathValue("env")
	if err := p.require(model.RoleAdmin, app); err != nil {
		return err
	}
	if _, _, err := s.loadEnv(r.Context(), p, app, env); err != nil {
		return err
	}
	var in struct {
		Label string `json:"label"`
	}
	if err := decodeOptionalJSON(r, &in); err != nil {
		return err
	}
	out, err := s.CreateCredential(r.Context(), p, app, env, in.Label, "")
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, out)
	return nil
}

func (s *Server) handleRevokeCredential(w http.ResponseWriter, r *http.Request, p *Principal) error {
	app, env, id := r.PathValue("app"), r.PathValue("env"), r.PathValue("credentialId")
	if err := p.require(model.RoleAdmin, app); err != nil {
		return err
	}
	if _, _, err := s.loadEnv(r.Context(), p, app, env); err != nil {
		return err
	}
	c, err := s.st.UpdateCredential(r.Context(), app, env, id, func(c *model.CredentialRecord) error {
		c.State = "revoked"
		return nil
	})
	if errors.Is(err, store.ErrNotFound) {
		return errNotFound("Credential %s does not exist.", id)
	}
	if err != nil {
		return err
	}
	s.audit(r.Context(), p, "credential.revoked", app, env, "credentials/"+id, nil)
	writeJSON(w, http.StatusOK, c.Credential)
	return nil
}

// ---------------------------------------------------------------- members and tokens

func (s *Server) handleListMembers(w http.ResponseWriter, r *http.Request, p *Principal) error {
	if err := p.require(model.RoleViewer, ""); err != nil {
		return err
	}
	ms, err := s.st.ListMembers(r.Context())
	if err != nil {
		return err
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].Email < ms[j].Email })
	for i := range ms {
		if ms[i].Apps == nil {
			ms[i].Apps = []string{}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": ms})
	return nil
}

func (s *Server) handleAddMember(w http.ResponseWriter, r *http.Request, p *Principal) error {
	if err := p.require(model.RoleAdmin, ""); err != nil {
		return err
	}
	var in model.MemberInput
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	m := model.Member{ID: auth.RandomID("mem_"), Email: strings.ToLower(in.Email), Name: in.Name, Role: in.Role, Apps: in.Apps}
	if m.Apps == nil {
		m.Apps = []string{}
	}
	if err := s.st.CreateMember(r.Context(), m); err != nil {
		if errors.Is(err, store.ErrExists) {
			return errConflict("NAME_TAKEN", "Member exists", "%s is already a member.", m.Email)
		}
		return err
	}
	s.audit(r.Context(), p, "member.added", "", "", "members/"+m.ID, map[string]any{"email": m.Email, "role": m.Role})
	writeJSON(w, http.StatusCreated, m)
	return nil
}

func (s *Server) handleUpdateMember(w http.ResponseWriter, r *http.Request, p *Principal) error {
	if err := p.require(model.RoleAdmin, ""); err != nil {
		return err
	}
	var in model.MemberInput
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	id := r.PathValue("memberId")
	m, err := s.st.UpdateMember(r.Context(), id, func(m *model.Member) error {
		m.Email, m.Role = strings.ToLower(in.Email), in.Role
		if in.Name != "" {
			m.Name = in.Name
		}
		if in.Apps != nil {
			m.Apps = in.Apps
		}
		return nil
	})
	if errors.Is(err, store.ErrNotFound) {
		return errNotFound("Member %s does not exist.", id)
	}
	if err != nil {
		return err
	}
	if m.Apps == nil {
		m.Apps = []string{}
	}
	s.audit(r.Context(), p, "member.role_changed", "", "", "members/"+id, map[string]any{"role": m.Role, "apps": m.Apps})
	writeJSON(w, http.StatusOK, m)
	return nil
}

func (s *Server) handleRemoveMember(w http.ResponseWriter, r *http.Request, p *Principal) error {
	if err := p.require(model.RoleAdmin, ""); err != nil {
		return err
	}
	id := r.PathValue("memberId")
	if id == p.Member.ID {
		return errConflict("IN_USE", "Cannot remove yourself", "Ask another admin to remove you.")
	}
	if err := s.st.DeleteMember(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return errNotFound("Member %s does not exist.", id)
		}
		return err
	}
	s.audit(r.Context(), p, "member.removed", "", "", "members/"+id, nil)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request, p *Principal) error {
	if err := p.require(model.RoleViewer, ""); err != nil {
		return err
	}
	var in struct {
		Label         string `json:"label"`
		ExpiresInDays int    `json:"expiresInDays"`
	}
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	if in.ExpiresInDays == 0 {
		in.ExpiresInDays = 90
	}
	m := p.Member
	if p.Kind == "dev" {
		// Dev users may not exist as members yet; tokens need one.
		if existing, err := s.st.MemberByEmail(r.Context(), m.Email); err == nil {
			m = existing
		} else {
			m.ID = auth.RandomID("mem_")
			if err := s.st.CreateMember(r.Context(), m); err != nil && !errors.Is(err, store.ErrExists) {
				return err
			}
		}
	}
	secret := patPrefix + auth.RandomString(40)
	t := model.Token{ID: auth.RandomID("tok_"), MemberID: m.ID, Label: in.Label, Hash: s.hasher.Hash(secret), CreatedAt: s.now(), ExpiresAt: s.now().Add(time.Duration(in.ExpiresInDays) * 24 * time.Hour)}
	if err := s.st.CreateToken(r.Context(), t); err != nil {
		return err
	}
	s.audit(r.Context(), p, "token.created", "", "", "tokens/"+t.ID, map[string]any{"label": t.Label})
	writeJSON(w, http.StatusCreated, map[string]any{"id": t.ID, "token": secret, "expiresAt": t.ExpiresAt})
	return nil
}
