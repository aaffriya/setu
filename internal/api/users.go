package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"setu/internal/users"
)

// userBodyLimit bounds one account: a name, a role, and a device list.
const userBodyLimit = 8 << 10

// userResponse carries a freshly issued token beside the account it belongs to.
// The plaintext exists only in this one response — the state file holds its
// SHA-256 — so the screen that receives it must show it before moving on.
type userResponse struct {
	User users.User `json:"user"`
	// Token is present only on creation and rotation.
	Token string `json:"token,omitempty"`
}

func (s *Server) handleListUsers(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.users.List())
}

// handleCreateUser adds an account. Only a name is asked for: there is no
// password to choose, because signing in is the generated token and nothing
// else. Role and device grants default to the most restrictive combination —
// "read", with no devices — so a half-filled form cannot hand out access.
func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name    string   `json:"name"`
		Role    string   `json:"role"`
		Devices []string `json:"devices"`
	}
	if err := decodeUserBody(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Role == "" {
		body.Role = users.RoleRead
	}
	if err := s.knownDevices(body.Devices); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	user, token, err := s.users.Create(body.Name, body.Role, body.Devices)
	if err != nil {
		writeUserError(w, err)
		return
	}
	s.log.Info("user created", "user", user.ID, "role", user.Role, "devices", len(user.Devices))
	writeJSON(w, http.StatusCreated, userResponse{User: user, Token: token})
}

// handleUpdateUser is a real PATCH: an omitted field is left alone, so renaming
// an account cannot silently resend — and revert — its device grants.
func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name    *string   `json:"name"`
		Role    *string   `json:"role"`
		Devices *[]string `json:"devices"`
	}
	if err := decodeUserBody(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Devices != nil {
		if err := s.knownDevices(*body.Devices); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	user, err := s.users.Update(r.PathValue("id"), users.Patch{
		Name: body.Name, Role: body.Role, Devices: body.Devices,
	})
	if err != nil {
		writeUserError(w, err)
		return
	}
	s.log.Info("user updated", "user", user.ID, "role", user.Role, "devices", len(user.Devices))
	writeJSON(w, http.StatusOK, userResponse{User: user})
}

// handleDeleteUser removes an account, which invalidates its token immediately.
func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.users.Delete(id); err != nil {
		writeUserError(w, err)
		return
	}
	s.log.Info("user removed", "user", id)
	w.WriteHeader(http.StatusNoContent)
}

// handleRotateUserToken issues a replacement token and invalidates the old one —
// the recovery path when a token is lost or was shared with the wrong person.
func (s *Server) handleRotateUserToken(w http.ResponseWriter, r *http.Request) {
	user, token, err := s.users.RotateToken(r.PathValue("id"))
	if err != nil {
		writeUserError(w, err)
		return
	}
	s.log.Info("user token rotated", "user", user.ID)
	writeJSON(w, http.StatusOK, userResponse{User: user, Token: token})
}

// knownDevices rejects a grant naming hardware that does not exist. Nothing
// breaks if it does — a stale id simply matches nothing — but a typo would then
// look like a granted device that never appears, which is far harder to explain
// than a refused save.
func (s *Server) knownDevices(ids []string) error {
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, ok := s.mgr.Device(id); !ok {
			return errors.New("unknown device " + id)
		}
	}
	return nil
}

func decodeUserBody(w http.ResponseWriter, r *http.Request, v any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, userBodyLimit))
	decoder.DisallowUnknownFields()
	return decoder.Decode(v)
}

// writeUserError passes the registry's own message through: like the inventory's,
// those messages are written for the person who just typed the value.
func writeUserError(w http.ResponseWriter, err error) {
	if errors.Is(err, users.ErrNotFound) {
		writeError(w, http.StatusNotFound, "unknown user")
		return
	}
	writeError(w, http.StatusBadRequest, err.Error())
}
