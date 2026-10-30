package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/ORG/gearbox/controlplane/internal/model"
	"github.com/ORG/gearbox/controlplane/internal/store"
	"github.com/ORG/gearbox/engine"
)

// applyItems returns the state with change items made live.
func applyItems(st model.EnvState, items []model.ChangeItem) model.EnvState {
	ns := st.Clone()
	for _, it := range items {
		k := model.LiveKey(it.Kind, it.Name)
		prev, had := st.Live[k]
		e := model.LiveEntry{Kind: it.Kind, Name: it.Name, Version: it.Version}
		if it.Kind == model.KindDecisions && it.Rollout != nil && it.Rollout.Percent < 100 && had && prev.Version != it.Version {
			from := prev.Version
			if prev.Rolling && prev.RolloutPercent == 0 {
				from = prev.RolloutFrom
			}
			e.Rolling, e.RolloutFrom, e.RolloutPercent = true, from, it.Rollout.Percent
			e.RolloutSalt = fmt.Sprintf("%s.rollout.%d", it.Name, it.Version)
		}
		ns.Live[k] = e
	}
	return ns
}

// checkItems verifies that every item names an existing version.
func (s *Server) checkItems(ctx context.Context, app string, items []model.ChangeItem, allowedKinds []string) *Problem {
	var errs []FieldError
	seen := map[string]bool{}
	for i, it := range items {
		path := fmt.Sprintf("/items/%d", i)
		if len(allowedKinds) > 0 && !slices.Contains(allowedKinds, it.Kind) {
			errs = append(errs, FieldError{Path: path + "/kind", Message: fmt.Sprintf("%s cannot be released this way", it.Kind)})
			continue
		}
		if it.Rollout != nil && it.Kind != model.KindDecisions {
			errs = append(errs, FieldError{Path: path + "/rollout", Message: "rollouts apply to decisions only"})
		}
		k := it.Kind + "/" + it.Name
		if seen[k] {
			errs = append(errs, FieldError{Path: path, Message: "listed twice"})
		}
		seen[k] = true
		if _, err := s.loadVersion(ctx, app, it.Kind, it.Name, it.Version); err != nil {
			errs = append(errs, FieldError{Path: path + "/version", Code: "NOT_FOUND", Message: fmt.Sprintf("%s/%s@%d does not exist", it.Kind, it.Name, it.Version)})
		}
	}
	if len(errs) > 0 {
		return errUnprocessable("VALIDATION_FAILED", "Invalid items", "%s %s", errs[0].Path, errs[0].Message).with(errs...)
	}
	return nil
}

// verifyState compiles the full bundle for a state and runs every test.
func (s *Server) verifyState(ctx context.Context, app string, st model.EnvState, gen int64) (engine.Set, model.TestCounts, error) {
	set, errs := s.fullSet(ctx, app, st, gen)
	if len(errs) > 0 {
		return set, model.TestCounts{}, errUnprocessable("VALIDATION_FAILED", "Bundle incomplete", "%s", errs[0].Message).with(errs...)
	}
	snap, err := s.eng.Compile(set)
	if err != nil {
		return set, model.TestCounts{}, compileProblem(err, nil)
	}
	results, err := snap.RunTests(ctx, "", 0)
	if err != nil {
		return set, model.TestCounts{}, errUnprocessable("TESTS_FAILED", "Tests could not run", "%v", err)
	}
	var counts model.TestCounts
	var fails []FieldError
	byDecision := map[string][]engine.TestResult{}
	for _, t := range results {
		if t.OK {
			counts.Passed++
		} else {
			counts.Failed++
		}
	}
	if counts.Failed > 0 {
		// RunTests over all decisions does not say which decision a result
		// belongs to; rerun per decision for useful paths.
		for _, d := range snap.Decisions() {
			rs, err := snap.RunTests(ctx, d.Name, 0)
			if err == nil {
				byDecision[d.Name] = rs
			}
		}
		for name, rs := range byDecision {
			fails = append(fails, failedTests(name, rs)...)
		}
		slices.SortFunc(fails, func(a, b FieldError) int {
			if a.Path < b.Path {
				return -1
			}
			if a.Path > b.Path {
				return 1
			}
			return 0
		})
		return set, counts, errUnprocessable("TESTS_FAILED", "Tests failed", "%d of %d test cases fail with this bundle.", counts.Failed, counts.Passed+counts.Failed).with(fails...)
	}
	return set, counts, nil
}

func (s *Server) liveDiff(ctx context.Context, app string, st model.EnvState, it model.ChangeItem) (*int64, string) {
	next, err := s.loadVersion(ctx, app, it.Kind, it.Name, it.Version)
	if err != nil {
		return nil, ""
	}
	e, ok := st.Live[model.LiveKey(it.Kind, it.Name)]
	if !ok {
		d, _ := diffSpecs(nil, viewSpec(it.Kind, next.Spec, next.Data), "(not live)", fmt.Sprintf("%s/%s@%d", it.Kind, it.Name, it.Version))
		return nil, d
	}
	lv := e.Version
	prev, err := s.loadVersion(ctx, app, it.Kind, it.Name, lv)
	if err != nil {
		return &lv, ""
	}
	d, _ := diffSpecs(viewSpec(it.Kind, prev.Spec, prev.Data), viewSpec(it.Kind, next.Spec, next.Data),
		fmt.Sprintf("%s/%s@%d", it.Kind, it.Name, lv), fmt.Sprintf("%s/%s@%d", it.Kind, it.Name, it.Version))
	return &lv, d
}

func crView(cr model.ChangeRequest) model.ChangeRequest {
	if cr.Approvals == nil {
		cr.Approvals = []model.Approval{}
	}
	if cr.Comments == nil {
		cr.Comments = []model.Comment{}
	}
	return cr
}

func (s *Server) handleListCRs(w http.ResponseWriter, r *http.Request, p *Principal) error {
	ctx := r.Context()
	app := r.PathValue("app")
	if _, err := s.loadApp(ctx, p, app); err != nil {
		return err
	}
	pg, err := pagination(r)
	if err != nil {
		return err
	}
	status, env := r.URL.Query().Get("status"), r.URL.Query().Get("environment")
	page, err := s.st.ListCRs(ctx, app, pg.Cursor, pg.Limit, func(cr model.ChangeRequest) bool {
		return (status == "" || cr.Status == status) && (env == "" || cr.Environment == env)
	})
	if err != nil {
		return errInvalid("%v", err)
	}
	for i := range page.Items {
		page.Items[i] = crView(page.Items[i])
	}
	writeJSON(w, http.StatusOK, list(page.Items, page.Next))
	return nil
}

func (s *Server) handleCreateCR(w http.ResponseWriter, r *http.Request, p *Principal) error {
	ctx := r.Context()
	app := r.PathValue("app")
	if _, err := s.loadApp(ctx, p, app); err != nil {
		return err
	}
	if err := p.require(model.RoleAuthor, app); err != nil {
		return err
	}
	var in model.ChangeRequestCreate
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	st, _, err := s.st.GetEnv(ctx, app, in.Environment)
	if errors.Is(err, store.ErrNotFound) {
		return errUnprocessable("VALIDATION_FAILED", "Unknown environment", "App %s has no environment %s.", app, in.Environment).with(FieldError{Path: "/environment", Message: "unknown environment"})
	}
	if err != nil {
		return err
	}
	if pr := s.checkItems(ctx, app, in.Items, nil); pr != nil {
		return pr
	}
	_, counts, err := s.verifyState(ctx, app, applyItems(st, in.Items), st.Generation+1)
	if err != nil {
		return err
	}
	items := make([]model.ChangeItem, 0, len(in.Items))
	for _, it := range in.Items {
		it.LiveVersion, it.Diff = s.liveDiff(ctx, app, st, it)
		items = append(items, it)
	}
	cr := model.ChangeRequest{
		App: app, Environment: in.Environment, Title: in.Title, Description: in.Description, Items: items,
		Status: model.CROpen, Author: p.Actor(), CreatedAt: s.now(), RequiredApprovals: st.Policy.RequiredApprovals,
		Approvals: []model.Approval{}, Comments: []model.Comment{}, Tests: &counts,
	}
	if cr.RequiredApprovals == 0 {
		cr.Status = model.CRApproved
	}
	cr, err = s.st.CreateCR(ctx, cr)
	if err != nil {
		return err
	}
	s.audit(ctx, p, "change_request.opened", app, cr.Environment, "change-requests/"+cr.ID, map[string]any{"items": in.Items})
	s.notify(ctx, "change_request.opened", map[string]any{"app": app, "environment": cr.Environment, "changeRequest": crView(cr)})
	if cr.Status == model.CRApproved {
		s.notify(ctx, "change_request.approved", map[string]any{"app": app, "environment": cr.Environment, "changeRequest": crView(cr)})
	}
	writeJSON(w, http.StatusCreated, crView(cr))
	return nil
}

func (s *Server) loadCR(ctx context.Context, p *Principal, app, id string) (model.ChangeRequest, error) {
	if _, err := s.loadApp(ctx, p, app); err != nil {
		return model.ChangeRequest{}, err
	}
	cr, err := s.st.GetCR(ctx, app, id)
	if errors.Is(err, store.ErrNotFound) {
		return cr, errNotFound("Change request %s does not exist.", id)
	}
	return cr, err
}

func (s *Server) handleGetCR(w http.ResponseWriter, r *http.Request, p *Principal) error {
	cr, err := s.loadCR(r.Context(), p, r.PathValue("app"), r.PathValue("id"))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, crView(cr))
	return nil
}

func invalidState(cr model.ChangeRequest, action string) *Problem {
	return errConflict("INVALID_STATE", "Not possible now", "Change request %s is %s; it cannot be %s.", cr.ID, cr.Status, action)
}

func (s *Server) updateCR(ctx context.Context, p *Principal, app, id string, fn func(*model.ChangeRequest) error) (model.ChangeRequest, error) {
	if _, err := s.loadCR(ctx, p, app, id); err != nil {
		return model.ChangeRequest{}, err
	}
	return s.st.UpdateCR(ctx, app, id, fn)
}

func (s *Server) handleApproveCR(w http.ResponseWriter, r *http.Request, p *Principal) error {
	ctx := r.Context()
	app, id := r.PathValue("app"), r.PathValue("id")
	if err := p.require(model.RoleApprover, app); err != nil {
		if _, lerr := s.loadCR(ctx, p, app, id); lerr != nil {
			return lerr
		}
		return err
	}
	var in struct {
		Comment string `json:"comment"`
	}
	if err := decodeOptionalJSON(r, &in); err != nil {
		return err
	}
	becameApproved := false
	cr, err := s.updateCR(ctx, p, app, id, func(cr *model.ChangeRequest) error {
		if cr.Status != model.CROpen {
			if cr.Status == model.CRApproved && slices.ContainsFunc(cr.Approvals, func(a model.Approval) bool { return a.By == p.Actor() }) {
				return nil // idempotent
			}
			return invalidState(*cr, "approved")
		}
		st, _, err := s.st.GetEnv(ctx, app, cr.Environment)
		if err != nil {
			return err
		}
		if cr.Author == p.Actor() && !st.Policy.AuthorCanApprove {
			return problemf(http.StatusForbidden, "SELF_APPROVAL", "Self-approval not allowed", "%s does not allow authors to approve their own change requests.", cr.Environment)
		}
		if !slices.ContainsFunc(cr.Approvals, func(a model.Approval) bool { return a.By == p.Actor() }) {
			cr.Approvals = append(cr.Approvals, model.Approval{By: p.Actor(), At: s.now(), Comment: in.Comment})
		}
		if len(cr.Approvals) >= cr.RequiredApprovals {
			cr.Status = model.CRApproved
			becameApproved = true
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.audit(ctx, p, "change_request.approved", app, cr.Environment, "change-requests/"+cr.ID, map[string]any{"approvals": len(cr.Approvals), "required": cr.RequiredApprovals})
	if becameApproved {
		s.notify(ctx, "change_request.approved", map[string]any{"app": app, "environment": cr.Environment, "changeRequest": crView(cr)})
	}
	writeJSON(w, http.StatusOK, crView(cr))
	return nil
}

func (s *Server) handleRejectCR(w http.ResponseWriter, r *http.Request, p *Principal) error {
	ctx := r.Context()
	app, id := r.PathValue("app"), r.PathValue("id")
	if err := p.require(model.RoleApprover, app); err != nil {
		return err
	}
	var in struct {
		Comment string `json:"comment"`
	}
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	cr, err := s.updateCR(ctx, p, app, id, func(cr *model.ChangeRequest) error {
		if cr.Status != model.CROpen && cr.Status != model.CRApproved {
			return invalidState(*cr, "rejected")
		}
		cr.Status = model.CRRejected
		cr.Comments = append(cr.Comments, model.Comment{By: p.Actor(), At: s.now(), Body: in.Comment})
		return nil
	})
	if err != nil {
		return err
	}
	s.audit(ctx, p, "change_request.rejected", app, cr.Environment, "change-requests/"+cr.ID, map[string]any{"comment": in.Comment})
	writeJSON(w, http.StatusOK, crView(cr))
	return nil
}

func (s *Server) handleCommentCR(w http.ResponseWriter, r *http.Request, p *Principal) error {
	ctx := r.Context()
	app, id := r.PathValue("app"), r.PathValue("id")
	if err := p.require(model.RoleViewer, app); err != nil {
		return err
	}
	var in struct {
		Body string `json:"body"`
	}
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	cr, err := s.updateCR(ctx, p, app, id, func(cr *model.ChangeRequest) error {
		cr.Comments = append(cr.Comments, model.Comment{By: p.Actor(), At: s.now(), Body: in.Body})
		return nil
	})
	if err != nil {
		return err
	}
	s.audit(ctx, p, "change_request.commented", app, cr.Environment, "change-requests/"+cr.ID, nil)
	writeJSON(w, http.StatusOK, crView(cr))
	return nil
}

func (s *Server) handleWithdrawCR(w http.ResponseWriter, r *http.Request, p *Principal) error {
	ctx := r.Context()
	app, id := r.PathValue("app"), r.PathValue("id")
	cr, err := s.updateCR(ctx, p, app, id, func(cr *model.ChangeRequest) error {
		if cr.Author != p.Actor() {
			return errForbidden("Only the author (%s) can withdraw this change request.", cr.Author)
		}
		if cr.Status != model.CROpen && cr.Status != model.CRApproved {
			return invalidState(*cr, "withdrawn")
		}
		cr.Status = model.CRWithdrawn
		return nil
	})
	if err != nil {
		return err
	}
	s.audit(ctx, p, "change_request.withdrawn", app, cr.Environment, "change-requests/"+cr.ID, nil)
	writeJSON(w, http.StatusOK, crView(cr))
	return nil
}
