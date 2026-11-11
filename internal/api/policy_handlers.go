package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/farshadfighter/taktaplus/internal/changes"
	"github.com/farshadfighter/taktaplus/internal/inventory"
	"github.com/farshadfighter/taktaplus/internal/policy"
)

func (s *srv) policyError(w http.ResponseWriter, r *http.Request, err error) {
	var ie policy.InputError
	switch {
	case errors.As(err, &ie):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, errBadBody):
		writeError(w, http.StatusBadRequest, "invalid request body")
	case errors.Is(err, policy.ErrLocked):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, policy.ErrNotFound), errors.Is(err, inventory.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	default:
		s.changeError(w, r, err)
	}
}

var errBadBody = errors.New("invalid request body")

// decodeBody is decodeJSON for handlers that report errors themselves.
func decodeBody(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return errBadBody
	}
	return nil
}

func username(r *http.Request) string {
	sess, _ := sessionFrom(r.Context())
	return sess.User.Username
}

func (s *srv) listPolicyObjects(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	objs, err := s.policy.Objects(ctx)
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	usage, err := s.policy.Usage(ctx)
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	conf, err := s.policy.Conflicts(ctx)
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": objs, "usage": usage, "conflicts": conf})
}

func (s *srv) savePolicyObject(w http.ResponseWriter, r *http.Request) {
	var o policy.Object
	if !decodeJSON(w, r, &o) {
		return
	}
	o.ID = 0
	if r.Method == http.MethodPut {
		id, ok := PathID(w, r, "id")
		if !ok {
			return
		}
		o.ID = id
	}
	if o.Mappings == nil {
		o.Mappings = map[int64]policy.Value{}
	}
	id, err := s.policy.SaveObject(r.Context(), o, username(r))
	if err != nil {
		s.policyError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "policy_object.save", "policy_object", strconv.FormatInt(id, 10), map[string]any{"type": o.Type, "name": o.Name})
	writeJSON(w, http.StatusOK, map[string]int64{"id": id})
}

func (s *srv) deletePolicyObject(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.policy.DeleteObject(r.Context(), id); err != nil {
		s.policyError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "policy_object.delete", "policy_object", strconv.FormatInt(id, 10), nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *srv) resolvePolicyConflict(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		How string `json:"how"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if err := s.policy.ResolveConflict(r.Context(), id, in.How, username(r)); err != nil {
		s.policyError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "policy_conflict.resolve", "policy_conflict", strconv.FormatInt(id, 10), map[string]any{"how": in.How})
	w.WriteHeader(http.StatusNoContent)
}

func (s *srv) policyInterfaces(w http.ResponseWriter, r *http.Request) {
	m, err := s.policy.Interfaces(r.Context())
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	out := map[string]map[string]string{}
	for d, z := range m {
		out[strconv.FormatInt(d, 10)] = z
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *srv) setPolicyInterfaces(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	var m map[string]string
	if !decodeJSON(w, r, &m) {
		return
	}
	if err := s.policy.SetInterfaces(r.Context(), id, m); err != nil {
		s.policyError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "policy_interfaces.set", "device", strconv.FormatInt(id, 10), map[string]any{"zones": m})
	w.WriteHeader(http.StatusNoContent)
}

func (s *srv) listPolicyPackages(w http.ResponseWriter, r *http.Request) {
	list, err := s.policy.Packages(r.Context())
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	type row struct {
		*policy.Package
		Statuses []policy.Status `json:"statuses"`
	}
	out := make([]row, 0, len(list))
	for _, p := range list {
		st, err := s.policy.Statuses(r.Context(), p)
		if err != nil {
			internalError(s.log, w, r, err)
			return
		}
		p.Draft = nil
		out = append(out, row{p, st})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *srv) getPolicyPackage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	p, err := s.policy.Package(ctx, id)
	if err != nil {
		s.policyError(w, r, err)
		return
	}
	st, err := s.policy.Statuses(ctx, p)
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	versions, err := s.policy.Versions(ctx, id)
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	published := []policy.Policy{}
	if p.Version > 0 {
		v, err := s.policy.Version(ctx, id, p.Version)
		if err != nil {
			internalError(s.log, w, r, err)
			return
		}
		published = v.Policies
	}
	hits := map[string]uint64{}
	for k, v := range s.policy.HitCounts(ctx, p) {
		hits[strconv.Itoa(k)] = v
	}
	writeJSON(w, http.StatusOK, map[string]any{"package": p, "published": published, "statuses": st, "versions": versions, "hits": hits})
}

func (s *srv) savePolicyPackage(w http.ResponseWriter, r *http.Request) {
	var p policy.Package
	if !decodeJSON(w, r, &p) {
		return
	}
	p.ID = 0
	if r.Method == http.MethodPut {
		id, ok := PathID(w, r, "id")
		if !ok {
			return
		}
		p.ID = id
	}
	id, err := s.policy.SavePackage(r.Context(), p)
	if err != nil {
		s.policyError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "policy_package.save", "policy_package", strconv.FormatInt(id, 10), map[string]any{"name": p.Name, "devices": p.DeviceIDs})
	writeJSON(w, http.StatusOK, map[string]int64{"id": id})
}

func (s *srv) deletePolicyPackage(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.policy.DeletePackage(r.Context(), id); err != nil {
		s.policyError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "policy_package.delete", "policy_package", strconv.FormatInt(id, 10), nil)
	w.WriteHeader(http.StatusNoContent)
}

// packageAction runs a package operation that needs only the package ID.
func (s *srv) packageAction(action string, fn func(r *http.Request, id int64) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := PathID(w, r, "id")
		if !ok {
			return
		}
		out, err := fn(r, id)
		if err != nil {
			s.policyError(w, r, err)
			return
		}
		if action != "" {
			sess, _ := sessionFrom(r.Context())
			s.audit(r, sess, action, "policy_package", strconv.FormatInt(id, 10), nil)
		}
		if out == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func (s *srv) policyRoutes() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"lock": s.packageAction("", func(r *http.Request, id int64) (any, error) {
			return s.policy.Lock(r.Context(), id, username(r))
		}),
		"unlock": s.packageAction("", func(r *http.Request, id int64) (any, error) {
			return nil, s.policy.Unlock(r.Context(), id, username(r))
		}),
		"draft": s.packageAction("", func(r *http.Request, id int64) (any, error) {
			var in struct {
				Policies []policy.Policy `json:"policies"`
			}
			if err := decodeBody(r, &in); err != nil {
				return nil, err
			}
			return nil, s.policy.SaveDraft(r.Context(), id, username(r), in.Policies)
		}),
		"discard": s.packageAction("policy_package.discard", func(r *http.Request, id int64) (any, error) {
			return nil, s.policy.DiscardDraft(r.Context(), id, username(r))
		}),
		"publish": s.packageAction("policy_package.publish", func(r *http.Request, id int64) (any, error) {
			var in struct {
				Note string `json:"note"`
			}
			if err := decodeBody(r, &in); err != nil {
				return nil, err
			}
			v, err := s.policy.Publish(r.Context(), id, username(r), in.Note)
			return map[string]int{"version": v}, err
		}),
		"revert": s.packageAction("policy_package.revert", func(r *http.Request, id int64) (any, error) {
			var in struct {
				Version int `json:"version"`
			}
			if err := decodeBody(r, &in); err != nil {
				return nil, err
			}
			return nil, s.policy.Revert(r.Context(), id, username(r), in.Version)
		}),
		"import": s.packageAction("policy_package.import", func(r *http.Request, id int64) (any, error) {
			var in struct {
				DeviceID int64 `json:"device_id"`
			}
			if err := decodeBody(r, &in); err != nil {
				return nil, err
			}
			return s.policy.Import(r.Context(), id, in.DeviceID, username(r))
		}),
		"accept": s.packageAction("policy_package.accept_local", func(r *http.Request, id int64) (any, error) {
			var in struct {
				DeviceID int64 `json:"device_id"`
			}
			if err := decodeBody(r, &in); err != nil {
				return nil, err
			}
			return nil, s.policy.AcceptLocal(r.Context(), id, in.DeviceID, username(r))
		}),
		"preview": s.packageAction("", func(r *http.Request, id int64) (any, error) {
			v, _ := strconv.Atoi(r.URL.Query().Get("version"))
			plans, version, err := s.policy.Plans(r.Context(), id, v)
			return map[string]any{"plans": plans, "version": version}, err
		}),
		"install": s.packageAction("policy_package.install", func(r *http.Request, id int64) (any, error) {
			var in struct {
				Version      int        `json:"version"`
				RunMode      string     `json:"run_mode"`
				RunAt        *time.Time `json:"run_at"`
				UrgentReason string     `json:"urgent_reason"`
			}
			if err := decodeBody(r, &in); err != nil {
				return nil, err
			}
			return s.policy.Install(r.Context(), id, in.Version, changes.CreateInput{RunMode: in.RunMode, RunAt: in.RunAt, UrgentReason: in.UrgentReason}, actorOf(r))
		}),
		"version": func(w http.ResponseWriter, r *http.Request) {
			id, ok := PathID(w, r, "id")
			if !ok {
				return
			}
			v, err := strconv.Atoi(r.URL.Query().Get("v"))
			if err != nil {
				writeError(w, http.StatusBadRequest, "version is required")
				return
			}
			out, err := s.policy.Version(r.Context(), id, v)
			if err != nil {
				s.policyError(w, r, err)
				return
			}
			writeJSON(w, http.StatusOK, out)
		},
	}
}
