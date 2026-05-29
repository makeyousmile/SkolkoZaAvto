package handlers

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type RequestStatus string

const (
	StatusPending   RequestStatus = "pending"
	StatusCompleted RequestStatus = "completed"
)

type CarRequest struct {
	ID          string        `json:"id"`
	Phone       string        `json:"phone"`
	Photos      []string      `json:"photos"` // Relative URLs to uploaded photos (e.g. /uploads/...)
	Video       string        `json:"video"`  // Relative URL to uploaded video (e.g. /uploads/...)
	Status      RequestStatus `json:"status"` // pending / completed
	MinPrice    float64       `json:"min_price"`
	MaxPrice    float64       `json:"max_price"`
	Comment     string        `json:"comment"`
	CreatedAt   time.Time     `json:"created_at"`
	EstimatedAt *time.Time    `json:"estimated_at,omitempty"`
}

type Database struct {
	mu       sync.RWMutex
	filePath string
	Requests map[string]*CarRequest `json:"requests"`
}

func NewDatabase(filePath string) (*Database, error) {
	db := &Database{
		filePath: filePath,
		Requests: make(map[string]*CarRequest),
	}

	// Create directory if not exists
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	// Read existing data if exists
	if _, err := os.Stat(filePath); err == nil {
		file, err := os.Open(filePath)
		if err != nil {
			return nil, err
		}
		defer file.Close()

		if err := json.NewDecoder(file).Decode(&db.Requests); err != nil {
			// If file is empty or corrupted, we start fresh but log it
			log.Printf("Warning: failed to decode database file, starting fresh: %v", err)
			db.Requests = make(map[string]*CarRequest)
		}
	}

	return db, nil
}

func (db *Database) save() error {
	file, err := os.Create(db.filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	return encoder.Encode(db.Requests)
}

func generateID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}

type APIHandler struct {
	db         *Database
	uploadsDir string
	sessions   map[string]time.Time
	sessionsMu sync.RWMutex
}

func NewAPIHandler(db *Database, uploadsDir string) (*APIHandler, error) {
	if err := os.MkdirAll(uploadsDir, 0755); err != nil {
		return nil, err
	}
	return &APIHandler{
		db:         db,
		uploadsDir: uploadsDir,
		sessions:   make(map[string]time.Time),
	}, nil
}

// Middleware to require admin authentication
func (h *APIHandler) RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("session_token")
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
			return
		}

		h.sessionsMu.RLock()
		expireTime, exists := h.sessions[cookie.Value]
		h.sessionsMu.RUnlock()

		if !exists || time.Now().After(expireTime) {
			if exists {
				h.sessionsMu.Lock()
				delete(h.sessions, cookie.Value)
				h.sessionsMu.Unlock()
			}
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
			return
		}

		next(w, r)
	}
}

// POST /api/admin/login
func (h *APIHandler) HandleAdminLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}

	err := json.NewDecoder(r.Body).Decode(&body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request body"})
		return
	}

	if body.Username != "admin" || body.Password != "chaka" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Неверный логин или пароль"})
		return
	}

	// Generate session token
	token := generateID() + generateID()

	h.sessionsMu.Lock()
	h.sessions[token] = time.Now().Add(24 * time.Hour) // expires in 24 hours
	h.sessionsMu.Unlock()

	// Set HTTP-only Cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "session_token",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400, // 1 day
	})

	writeJSON(w, http.StatusOK, map[string]string{"message": "Logged in successfully"})
}

// POST /api/admin/logout
func (h *APIHandler) HandleAdminLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	cookie, err := r.Cookie("session_token")
	if err == nil {
		h.sessionsMu.Lock()
		delete(h.sessions, cookie.Value)
		h.sessionsMu.Unlock()
	}

	// Expire cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "session_token",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})

	writeJSON(w, http.StatusOK, map[string]string{"message": "Logged out successfully"})
}


// Setup CORS and basic wrapper
func (h *APIHandler) EnableCORS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS, PUT, DELETE")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next(w, r)
	}
}

// Write JSON utility
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// POST /api/upload
func (h *APIHandler) HandleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	// Parse multipart form up to 500 MB (generous for video)
	err := r.ParseMultipartForm(500 << 20)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Failed to parse form: " + err.Error()})
		return
	}

	phone := r.FormValue("phone")
	if strings.TrimSpace(phone) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Phone number or Telegram username is required"})
		return
	}

	reqID := generateID()
	reqDir := filepath.Join(h.uploadsDir, reqID)
	if err := os.MkdirAll(reqDir, 0755); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to create uploads directory"})
		return
	}

	var photoPaths []string
	var videoPath string

	// Handle Photos Upload
	form := r.MultipartForm
	photos := form.File["photos"]
	for i, fileHeader := range photos {
		file, err := fileHeader.Open()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to open photo file"})
			return
		}
		defer file.Close()

		// Keep original extension
		ext := filepath.Ext(fileHeader.Filename)
		if ext == "" {
			ext = ".jpg" // default fallback
		}
		fileName := fmt.Sprintf("photo_%d%s", i+1, ext)
		targetPath := filepath.Join(reqDir, fileName)

		out, err := os.Create(targetPath)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to save photo"})
			return
		}
		defer out.Close()

		if _, err := io.Copy(out, file); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to write photo file"})
			return
		}

		photoPaths = append(photoPaths, fmt.Sprintf("/uploads/%s/%s", reqID, fileName))
	}

	// Handle Video Upload (single)
	videos := form.File["video"]
	if len(videos) > 0 {
		fileHeader := videos[0]
		file, err := fileHeader.Open()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to open video file"})
			return
		}
		defer file.Close()

		ext := filepath.Ext(fileHeader.Filename)
		if ext == "" {
			ext = ".mp4"
		}
		fileName := "video" + ext
		targetPath := filepath.Join(reqDir, fileName)

		out, err := os.Create(targetPath)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to save video"})
			return
		}
		defer out.Close()

		if _, err := io.Copy(out, file); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to write video file"})
			return
		}

		videoPath = fmt.Sprintf("/uploads/%s/%s", reqID, fileName)
	}

	h.db.mu.Lock()
	defer h.db.mu.Unlock()

	req := &CarRequest{
		ID:        reqID,
		Phone:     phone,
		Photos:    photoPaths,
		Video:     videoPath,
		Status:    StatusPending,
		CreatedAt: time.Now(),
	}

	h.db.Requests[reqID] = req
	if err := h.db.save(); err != nil {
		log.Printf("Error saving database: %v", err)
	}

	writeJSON(w, http.StatusCreated, req)
}

// GET /api/requests/:id/status
func (h *APIHandler) HandleGetStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	// Extract ID from path
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request ID"})
		return
	}
	id := parts[3]

	h.db.mu.RLock()
	req, exists := h.db.Requests[id]
	h.db.mu.RUnlock()

	if !exists {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Request not found"})
		return
	}

	writeJSON(w, http.StatusOK, req)
}

// GET /api/admin/requests
func (h *APIHandler) HandleGetAdminRequests(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	h.db.mu.RLock()
	defer h.db.mu.RUnlock()

	// Convert map to slice
	var result []*CarRequest
	for _, req := range h.db.Requests {
		result = append(result, req)
	}

	// Sort: newest first
	for i := 0; i < len(result); i++ {
		for j := i + 1; j < len(result); j++ {
			if result[i].CreatedAt.Before(result[j].CreatedAt) {
				result[i], result[j] = result[j], result[i]
			}
		}
	}

	writeJSON(w, http.StatusOK, result)
}

// POST /api/admin/requests/:id/estimate
func (h *APIHandler) HandlePostEstimate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	// Extract ID from path: /api/admin/requests/{id}/estimate
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request URL"})
		return
	}
	id := parts[4]

	var body struct {
		MinPrice float64 `json:"min_price"`
		MaxPrice float64 `json:"max_price"`
		Comment  string  `json:"comment"`
	}

	err := json.NewDecoder(r.Body).Decode(&body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request body"})
		return
	}

	h.db.mu.Lock()
	defer h.db.mu.Unlock()

	req, exists := h.db.Requests[id]
	if !exists {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Request not found"})
		return
	}

	now := time.Now()
	req.Status = StatusCompleted
	req.MinPrice = body.MinPrice
	req.MaxPrice = body.MaxPrice
	req.Comment = body.Comment
	req.EstimatedAt = &now

	if err := h.db.save(); err != nil {
		log.Printf("Error saving database after estimate: %v", err)
	}

	writeJSON(w, http.StatusOK, req)
}
