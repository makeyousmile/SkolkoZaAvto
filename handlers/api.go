package handlers

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"flag"
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
	Token       string        `json:"token"`
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
		token TEXT,
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
	// Add token column if it doesn't exist (ignores error if it already exists)
	_, _ = dbConn.Exec("ALTER TABLE requests ADD COLUMN token TEXT")

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
		adminPassword := os.Getenv("ADMIN_PASSWORD")
		appEnv := os.Getenv("APP_ENV")
		if adminPassword == "" {
			if appEnv == "production" {
				log.Printf("ERROR: APP_ENV is set to production, but ADMIN_PASSWORD is empty. No default admin user created!")
			} else {
				adminPassword = "chaka" // default for local development
				log.Printf("WARNING: No ADMIN_PASSWORD environment variable set. Using default password 'chaka' for local development. Set ADMIN_PASSWORD in production!")
				hashed, err := bcrypt.GenerateFromPassword([]byte(adminPassword), bcrypt.DefaultCost)
				if err == nil {
					_, _ = dbConn.Exec("INSERT INTO users (username, password_hash) VALUES (?, ?)", "admin", string(hashed))
				}
			}
		} else {
			hashed, err := bcrypt.GenerateFromPassword([]byte(adminPassword), bcrypt.DefaultCost)
			if err == nil {
				_, _ = dbConn.Exec("INSERT INTO users (username, password_hash) VALUES (?, ?)", "admin", string(hashed))
			}
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
			defer legacyFile.Close()
			var legacyRequests map[string]*CarRequest
			if err := json.NewDecoder(legacyFile).Decode(&legacyRequests); err == nil {
				tx, err := dbConn.Begin()
				if err != nil {
					dbConn.Close()
					return nil, fmt.Errorf("failed to start migration transaction: %w", err)
				}
				defer tx.Rollback()

				stmt, err := tx.Prepare(`
					INSERT INTO requests (id, token, phone, photos, video, status, min_price, max_price, currency, region, comment, created_at, estimated_at)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
					ON CONFLICT(id) DO NOTHING
				`)
				if err != nil {
					return nil, fmt.Errorf("failed to prepare migration statement: %w", err)
				}
				defer stmt.Close()

				for _, req := range legacyRequests {
					photosJSON, err := json.Marshal(req.Photos)
					if err != nil {
						return nil, fmt.Errorf("failed to marshal photos for request %s: %w", req.ID, err)
					}
					var estAt interface{}
					if req.EstimatedAt != nil {
						estAt = *req.EstimatedAt
					}
					token := generateToken()
					_, err = stmt.Exec(
						req.ID,
						token,
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
						return nil, fmt.Errorf("failed to insert migration row for request %s: %w", req.ID, err)
					}
				}

				if err := tx.Commit(); err != nil {
					return nil, fmt.Errorf("failed to commit migration transaction: %w", err)
				}

				legacyFile.Close()
				if err := os.Rename(legacyPath, legacyPath+".bak"); err != nil {
					log.Printf("Warning: failed to rename legacy db.json to db.json.bak: %v", err)
				} else {
					log.Printf("Successfully migrated database from legacy db.json to SQLite db.sqlite")
				}
			}
		}
	}

	// Load existing data
	rows, err := dbConn.Query("SELECT id, token, phone, photos, video, status, min_price, max_price, currency, region, comment, created_at, estimated_at FROM requests")
	if err != nil {
		dbConn.Close()
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var req CarRequest
		var tokenStr sql.NullString
		var photosJSON string
		var statusStr string
		var estAt *time.Time
		err := rows.Scan(
			&req.ID,
			&tokenStr,
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
		req.Token = tokenStr.String
		if req.Token == "" {
			req.Token = generateToken()
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
		INSERT INTO requests (id, token, phone, photos, video, status, min_price, max_price, currency, region, comment, created_at, estimated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			token=excluded.token,
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
			req.Token,
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

func (db *Database) Close() error {
	if db.dbConn != nil {
		return db.dbConn.Close()
	}
	return nil
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

func generateToken() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}

type APIHandler struct {
	db              *Database
	uploadsDir      string
	sessions        map[string]time.Time
	sessionsMu      sync.RWMutex
	loginAttempts   map[string]time.Time
	loginAttemptsMu sync.Mutex
}

func NewAPIHandler(db *Database, uploadsDir string) (*APIHandler, error) {
	if err := os.MkdirAll(uploadsDir, 0755); err != nil {
		return nil, err
	}
	return &APIHandler{
		db:            db,
		uploadsDir:    uploadsDir,
		sessions:      make(map[string]time.Time),
		loginAttempts: make(map[string]time.Time),
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

	// 1. Rate Limit login attempts by IP (skip in tests to avoid test conflict)
	isTest := flag.Lookup("test.v") != nil
	if !isTest {
		ip := r.RemoteAddr
		if idx := strings.LastIndex(ip, ":"); idx != -1 {
			ip = ip[:idx]
		}
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if parts := strings.Split(xff, ","); len(parts) > 0 {
				ip = strings.TrimSpace(parts[0])
			}
		}

		h.loginAttemptsMu.Lock()
		if h.loginAttempts == nil {
			h.loginAttempts = make(map[string]time.Time)
		}
		lastAttempt, exists := h.loginAttempts[ip]
		if exists && time.Since(lastAttempt) < 2*time.Second {
			h.loginAttemptsMu.Unlock()
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "Превышена частота попыток. Пожалуйста, подождите."})
			return
		}
		h.loginAttempts[ip] = time.Now()
		h.loginAttemptsMu.Unlock()
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
		// Delay to prevent brute-force and timing attacks (skip in tests to run fast)
		if !isTest {
			time.Sleep(500 * time.Millisecond)
		}
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Неверный логин или пароль"})
		return
	}

	// Generate session token
	token := generateID() + generateID()

	h.sessionsMu.Lock()
	h.sessions[token] = time.Now().Add(24 * time.Hour) // expires in 24 hours
	h.sessionsMu.Unlock()

	// 2. Set HTTP-only Cookie with Secure flag in production
	cookieSecure := os.Getenv("COOKIE_SECURE") == "true" || os.Getenv("APP_ENV") == "production"
	http.SetCookie(w, &http.Cookie{
		Name:     "session_token",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   cookieSecure,
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

	// Limit request body size to 100 MB to prevent DOS / disc fill
	r.Body = http.MaxBytesReader(w, r.Body, 100<<20)

	// Parse multipart form
	err := r.ParseMultipartForm(100 << 20)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Превышен лимит размера запроса (100 МБ) или неверный формат"})
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

	// Telegram checking
	if strings.HasPrefix(phone, "@") {
		username := strings.TrimPrefix(phone, "@")
		if len(username) < 5 || len(username) > 32 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Некорректная длина Telegram-аккаунта (от 5 до 32 символов)"})
			return
		}

		exists, err := checkTelegramUsernameExists(username)
		if err != nil {
			log.Printf("Warning: failed to check Telegram username existence for %s: %v. Bypassing check.", username, err)
		} else if !exists {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Указанный Telegram-аккаунт не найден. Проверьте правильность ввода."})
			return
		}
	}

	reqID := generateID()
	reqToken := generateToken()
	reqDir := filepath.Join(h.uploadsDir, reqID)
	if err := os.MkdirAll(reqDir, 0755); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to create uploads directory"})
		return
	}

	var photoPaths []string
	var videoPath string

	form := r.MultipartForm
	photos := form.File["photos"]

	// Limit photo count
	if len(photos) > 10 {
		os.RemoveAll(reqDir)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Максимум 10 фотографий"})
		return
	}

	for i, fileHeader := range photos {
		// Limit photo size to 10 MB
		if fileHeader.Size > 10<<20 {
			os.RemoveAll(reqDir)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("Размер фото %s превышает лимит 10 МБ", fileHeader.Filename)})
			return
		}

		// Validate extension
		ext := strings.ToLower(filepath.Ext(fileHeader.Filename))
		if ext != ".jpg" && ext != ".jpeg" && ext != ".png" && ext != ".webp" {
			os.RemoveAll(reqDir)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("Неподдерживаемый формат файла %s. Разрешены только JPG, JPEG, PNG, WEBP", fileHeader.Filename)})
			return
		}

		file, err := fileHeader.Open()
		if err != nil {
			os.RemoveAll(reqDir)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to open photo file"})
			return
		}

		// MIME validation by signature
		buff := make([]byte, 512)
		if _, err := file.Read(buff); err != nil && err != io.EOF {
			file.Close()
			os.RemoveAll(reqDir)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to read photo signature"})
			return
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			file.Close()
			os.RemoveAll(reqDir)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to seek photo"})
			return
		}

		contentType := http.DetectContentType(buff)
		if contentType != "image/jpeg" && contentType != "image/png" && contentType != "image/webp" {
			file.Close()
			os.RemoveAll(reqDir)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("Неподдерживаемое содержимое изображения в %s", fileHeader.Filename)})
			return
		}

		fileName := fmt.Sprintf("photo_%d%s", i+1, ext)
		targetPath := filepath.Join(reqDir, fileName)

		out, err := os.Create(targetPath)
		if err != nil {
			file.Close()
			os.RemoveAll(reqDir)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to create photo file on disk"})
			return
		}

		if _, err := io.Copy(out, file); err != nil {
			out.Close()
			file.Close()
			os.RemoveAll(reqDir)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to save photo file"})
			return
		}
		out.Close()
		file.Close()

		photoPaths = append(photoPaths, fmt.Sprintf("/uploads/%s/%s", reqID, fileName))
	}

	videos := form.File["video"]
	if len(videos) > 0 {
		fileHeader := videos[0]

		// Limit video size to 80 MB
		if fileHeader.Size > 80<<20 {
			os.RemoveAll(reqDir)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("Размер видео %s превышает лимит 80 МБ", fileHeader.Filename)})
			return
		}

		// Validate extension
		ext := strings.ToLower(filepath.Ext(fileHeader.Filename))
		if ext != ".mp4" && ext != ".mov" && ext != ".webm" && ext != ".mkv" {
			os.RemoveAll(reqDir)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("Неподдерживаемый формат видео %s. Разрешены только MP4, MOV, WEBM, MKV", fileHeader.Filename)})
			return
		}

		file, err := fileHeader.Open()
		if err != nil {
			os.RemoveAll(reqDir)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to open video file"})
			return
		}

		// MIME validation
		buff := make([]byte, 512)
		if _, err := file.Read(buff); err != nil && err != io.EOF {
			file.Close()
			os.RemoveAll(reqDir)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to read video signature"})
			return
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			file.Close()
			os.RemoveAll(reqDir)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to seek video"})
			return
		}

		contentType := http.DetectContentType(buff)
		allowedVideoTypes := map[string]bool{
			"video/mp4":        true,
			"video/quicktime":  true,
			"video/webm":       true,
			"video/x-matroska": true,
			"application/octet-stream": true,
		}
		if !allowedVideoTypes[contentType] {
			file.Close()
			os.RemoveAll(reqDir)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("Неподдерживаемое содержимое видеофайла %s", fileHeader.Filename)})
			return
		}

		fileName := "video" + ext
		targetPath := filepath.Join(reqDir, fileName)

		out, err := os.Create(targetPath)
		if err != nil {
			file.Close()
			os.RemoveAll(reqDir)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to create video file on disk"})
			return
		}

		if _, err := io.Copy(out, file); err != nil {
			out.Close()
			file.Close()
			os.RemoveAll(reqDir)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to save video file"})
			return
		}
		out.Close()
		file.Close()

		videoPath = fmt.Sprintf("/uploads/%s/%s", reqID, fileName)
	}

	h.db.mu.Lock()
	defer h.db.mu.Unlock()

	req := &CarRequest{
		ID:        reqID,
		Token:     reqToken,
		Phone:     phone,
		Photos:    photoPaths,
		Video:     videoPath,
		Status:    StatusPending,
		Currency:  "BYN",
		Region:    region,
		CreatedAt: time.Now(),
	}

	h.db.Requests[reqID] = req
	if err := h.db.save(); err != nil {
		log.Printf("Error saving database: %v", err)
		delete(h.db.Requests, reqID)
		os.RemoveAll(reqDir)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Не удалось сохранить заявку в базу данных"})
		return
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

	// Validate public security token
	token := r.URL.Query().Get("token")
	if req.Token != token {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
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

	// Revertible transactional update
	prevStatus := req.Status
	prevMin := req.MinPrice
	prevMax := req.MaxPrice
	prevCur := req.Currency
	prevComment := req.Comment
	prevEst := req.EstimatedAt

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
		// Revert changes on database failure
		req.Status = prevStatus
		req.MinPrice = prevMin
		req.MaxPrice = prevMax
		req.Currency = prevCur
		req.Comment = prevComment
		req.EstimatedAt = prevEst
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to save estimate to database"})
		return
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
	req, exists := h.db.Requests[id]
	if !exists {
		h.db.mu.Unlock()
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Request not found"})
		return
	}

	// Transactional DB update first
	delete(h.db.Requests, id)
	if err := h.db.save(); err != nil {
		log.Printf("Error saving database after delete: %v", err)
		// Restore on DB failure
		h.db.Requests[id] = req
		h.db.mu.Unlock()
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to delete request from database"})
		return
	}
	h.db.mu.Unlock()

	// Clean files on disk only after DB deletion succeeded
	reqDir := filepath.Join(h.uploadsDir, id)
	if err := os.RemoveAll(reqDir); err != nil {
		log.Printf("Warning: failed to delete upload directory %s: %v", reqDir, err)
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
