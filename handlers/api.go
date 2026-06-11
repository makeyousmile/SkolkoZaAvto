package handlers

import (
	"crypto/rand"
	"database/sql"
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

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
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
	Currency    string        `json:"currency"` // BYN / $
	Region      string        `json:"region"`   // Минская область, etc.
	Comment     string        `json:"comment"`
	CreatedAt   time.Time     `json:"created_at"`
	EstimatedAt *time.Time    `json:"estimated_at,omitempty"`
}

type Database struct {
	mu       sync.RWMutex
	filePath string
	dbConn   *sql.DB
	Requests map[string]*CarRequest `json:"requests"`
}

func NewDatabase(filePath string) (*Database, error) {
	// Create directory if not exists
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	dbConn, err := sql.Open("sqlite", filePath)
	if err != nil {
		return nil, err
	}

	// Create table if not exists
	createTableSQL := `
	CREATE TABLE IF NOT EXISTS requests (
		id TEXT PRIMARY KEY,
		phone TEXT,
		photos TEXT,
		video TEXT,
		status TEXT,
		min_price REAL,
		max_price REAL,
		currency TEXT,
		region TEXT,
		comment TEXT,
		created_at DATETIME,
		estimated_at DATETIME
	);`
	if _, err := dbConn.Exec(createTableSQL); err != nil {
		dbConn.Close()
		return nil, err
	}

	// Create users table if not exists
	createUsersTableSQL := `
	CREATE TABLE IF NOT EXISTS users (
		username TEXT PRIMARY KEY,
		password_hash TEXT
	);`
	if _, err := dbConn.Exec(createUsersTableSQL); err != nil {
		dbConn.Close()
		return nil, err
	}

	// Insert default admin if table is empty
	var count int
	err = dbConn.QueryRow("SELECT COUNT(*) FROM users").Scan(&count)
	if err == nil && count == 0 {
		hashed, err := bcrypt.GenerateFromPassword([]byte("chaka"), bcrypt.DefaultCost)
		if err == nil {
			_, _ = dbConn.Exec("INSERT INTO users (username, password_hash) VALUES (?, ?)", "admin", string(hashed))
		}
	}

	db := &Database{
		filePath: filePath,
		dbConn:   dbConn,
		Requests: make(map[string]*CarRequest),
	}

	// Migrate legacy db.json if exists
	legacyPath := filepath.Join(filepath.Dir(filePath), "db.json")
	if _, err := os.Stat(legacyPath); err == nil {
		legacyFile, err := os.Open(legacyPath)
		if err == nil {
			var legacyRequests map[string]*CarRequest
			if err := json.NewDecoder(legacyFile).Decode(&legacyRequests); err == nil {
				tx, err := dbConn.Begin()
				if err == nil {
					stmt, err := tx.Prepare(`
						INSERT INTO requests (id, phone, photos, video, status, min_price, max_price, currency, region, comment, created_at, estimated_at)
						VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
						ON CONFLICT(id) DO NOTHING
					`)
					if err == nil {
						for _, req := range legacyRequests {
							photosJSON, _ := json.Marshal(req.Photos)
							var estAt interface{}
							if req.EstimatedAt != nil {
								estAt = *req.EstimatedAt
							}
							stmt.Exec(
								req.ID,
								req.Phone,
								string(photosJSON),
								req.Video,
								string(req.Status),
								req.MinPrice,
								req.MaxPrice,
								req.Currency,
								req.Region,
								req.Comment,
								req.CreatedAt,
								estAt,
							)
						}
						stmt.Close()
						tx.Commit()
					}
				}
			}
			legacyFile.Close()
			os.Rename(legacyPath, legacyPath+".bak")
		}
	}

	// Load existing data
	rows, err := dbConn.Query("SELECT id, phone, photos, video, status, min_price, max_price, currency, region, comment, created_at, estimated_at FROM requests")
	if err != nil {
		dbConn.Close()
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var req CarRequest
		var photosJSON string
		var statusStr string
		var estAt *time.Time
		err := rows.Scan(
			&req.ID,
			&req.Phone,
			&photosJSON,
			&req.Video,
			&statusStr,
			&req.MinPrice,
			&req.MaxPrice,
			&req.Currency,
			&req.Region,
			&req.Comment,
			&req.CreatedAt,
			&estAt,
		)
		if err != nil {
			dbConn.Close()
			return nil, err
		}
		req.Status = RequestStatus(statusStr)
		req.EstimatedAt = estAt
		if err := json.Unmarshal([]byte(photosJSON), &req.Photos); err != nil {
			req.Photos = []string{}
		}
		db.Requests[req.ID] = &req
	}

	return db, nil
}

func (db *Database) save() error {
	tx, err := db.dbConn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 1. Delete rows not in the map
	var placeholders []string
	var args []interface{}
	for id := range db.Requests {
		placeholders = append(placeholders, "?")
		args = append(args, id)
	}
	if len(placeholders) > 0 {
		query := fmt.Sprintf("DELETE FROM requests WHERE id NOT IN (%s)", strings.Join(placeholders, ","))
		if _, err := tx.Exec(query, args...); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec("DELETE FROM requests"); err != nil {
			return err
		}
	}

	// 2. Upsert all requests
	stmt, err := tx.Prepare(`
		INSERT INTO requests (id, phone, photos, video, status, min_price, max_price, currency, region, comment, created_at, estimated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			phone=excluded.phone,
			photos=excluded.photos,
			video=excluded.video,
			status=excluded.status,
			min_price=excluded.min_price,
			max_price=excluded.max_price,
			currency=excluded.currency,
			region=excluded.region,
			comment=excluded.comment,
			created_at=excluded.created_at,
			estimated_at=excluded.estimated_at
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, req := range db.Requests {
		photosJSON, _ := json.Marshal(req.Photos)
		var estAt interface{}
		if req.EstimatedAt != nil {
			estAt = *req.EstimatedAt
		}
		_, err = stmt.Exec(
			req.ID,
			req.Phone,
			string(photosJSON),
			req.Video,
			string(req.Status),
			req.MinPrice,
			req.MaxPrice,
			req.Currency,
			req.Region,
			req.Comment,
			req.CreatedAt,
			estAt,
		)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (db *Database) AuthenticateAdmin(username, password string) (bool, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	var hash string
	err := db.dbConn.QueryRow("SELECT password_hash FROM users WHERE username = ?", username).Scan(&hash)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}

	err = bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	if err != nil {
		return false, nil
	}

	return true, nil
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

	ok, err := h.db.AuthenticateAdmin(body.Username, body.Password)
	if err != nil || !ok {
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

	phone := strings.TrimSpace(r.FormValue("phone"))
	if phone == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Пожалуйста, укажите телефон или Telegram"})
		return
	}

	region := strings.TrimSpace(r.FormValue("region"))
	if region == "" {
		region = "Не указана"
	}

	// Если пользователь ввел Telegram-аккаунт (начинается с @)
	if strings.HasPrefix(phone, "@") {
		username := strings.TrimPrefix(phone, "@")
		if len(username) < 5 || len(username) > 32 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Некорректная длина Telegram-аккаунта (от 5 до 32 символов)"})
			return
		}

		// Выполняем быструю проверку существования аккаунта
		exists, err := checkTelegramUsernameExists(username)
		if err != nil {
			// Логируем ошибку сети/таймаута, но разрешаем отправку (чтобы не блокировать пользователей в случае сетевых проблем)
			log.Printf("Warning: failed to check Telegram username existence for %s: %v. Bypassing check.", username, err)
		} else if !exists {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Указанный Telegram-аккаунт не найден. Проверьте правильность ввода."})
			return
		}
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
		Currency:  "BYN", // default until estimated
		Region:    region,
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

// POST /api/admin/requests/:id/estimate or DELETE /api/admin/requests/:id
func (h *APIHandler) HandlePostEstimate(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodDelete {
		h.HandleDeleteRequest(w, r)
		return
	}

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
		Currency string  `json:"currency"`
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
	req.Currency = body.Currency
	if req.Currency == "" {
		req.Currency = "BYN"
	}
	req.Comment = body.Comment
	req.EstimatedAt = &now

	if err := h.db.save(); err != nil {
		log.Printf("Error saving database after estimate: %v", err)
	}

	writeJSON(w, http.StatusOK, req)
}

// DELETE /api/admin/requests/:id
func (h *APIHandler) HandleDeleteRequest(w http.ResponseWriter, r *http.Request) {
	// Extract ID from path: /api/admin/requests/{id}
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request URL"})
		return
	}
	id := parts[4]

	h.db.mu.Lock()
	defer h.db.mu.Unlock()

	_, exists := h.db.Requests[id]
	if !exists {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Request not found"})
		return
	}

	// Delete uploads directory for this request
	reqDir := filepath.Join(h.uploadsDir, id)
	if err := os.RemoveAll(reqDir); err != nil {
		log.Printf("Warning: failed to delete upload directory %s: %v", reqDir, err)
	}

	delete(h.db.Requests, id)

	if err := h.db.save(); err != nil {
		log.Printf("Error saving database after delete: %v", err)
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "Request deleted successfully"})
}

// Вспомогательная функция для быстрой проверки существования публичного аккаунта Telegram
func checkTelegramUsernameExists(username string) (bool, error) {
	client := &http.Client{
		Timeout: 2 * time.Second,
	}

	resp, err := client.Get("https://t.me/" + username)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, nil
	}

	// Читаем первые 100 КБ ответа (этого более чем достаточно для мета-тегов)
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1024*100))
	if err != nil {
		return false, err
	}

	bodyStr := string(bodyBytes)

	// Запрет индексации поисковиками - главный признак несуществующего профиля на t.me
	if strings.Contains(bodyStr, "noindex, nofollow") {
		return false, nil
	}

	// Сравниваем со стандартным заголовком заглушки
	fallbackTitle := fmt.Sprintf(`content="Telegram: Contact @%s"`, username)
	if strings.Contains(bodyStr, fallbackTitle) {
		return false, nil
	}

	return true, nil
}
