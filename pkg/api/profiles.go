package api

import (
	"encoding/json"
	"net/http"
	"strings"
)

type createProfileRequest struct {
	Name   string `json:"name"`
	Icon   string `json:"icon"`
	IsKids bool   `json:"is_kids"`
}

func (h *Handler) handleProfiles(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	if h.playback == nil {
		http.Error(w, "Playback store not initialized", http.StatusServiceUnavailable)
		return
	}

	switch r.Method {
	case http.MethodGet:
		profiles, err := h.playback.GetProfiles()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(profiles)

	case http.MethodPost:
		var req createProfileRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid JSON body: "+err.Error(), http.StatusBadRequest)
			return
		}
		req.Name = strings.TrimSpace(req.Name)
		if req.Name == "" {
			http.Error(w, "Profile name is required", http.StatusBadRequest)
			return
		}
		p, err := h.playback.CreateProfile(req.Name, req.Icon, req.IsKids)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(p)

	case http.MethodDelete:
		// Also support DELETE /api/profiles?id=xxx
		id := strings.TrimSpace(r.URL.Query().Get("id"))
		if id == "" {
			http.Error(w, "id parameter required", http.StatusBadRequest)
			return
		}
		if id == "default" {
			http.Error(w, "Cannot delete default profile", http.StatusBadRequest)
			return
		}
		if err := h.playback.DeleteProfile(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleProfileByID(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	if h.playback == nil {
		http.Error(w, "Playback store not initialized", http.StatusServiceUnavailable)
		return
	}

	// Extract ID from path: /api/profiles/{id}
	path := strings.TrimPrefix(r.URL.Path, "/api/profiles/")
	id := strings.Trim(path, "/")
	if id == "" {
		http.Error(w, "Profile ID required", http.StatusBadRequest)
		return
	}

	if r.Method == http.MethodDelete {
		if id == "default" {
			http.Error(w, "Cannot delete default profile", http.StatusBadRequest)
			return
		}
		if err := h.playback.DeleteProfile(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}
