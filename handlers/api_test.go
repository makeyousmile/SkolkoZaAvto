package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewDatabase(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "db_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "db.json")

	// Initialize new database
	db, err := NewDatabase(dbPath)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}

	if len(db.Requests) != 0 {
		t.Errorf("expected empty database, got %d requests", len(db.Requests))
	}

	// Add request
	req := &CarRequest{
		ID:       "test-id",
		Phone:    "+375291234567",
		Region:   "Минская область",
		Currency: "BYN",
		Status:   StatusPending,
	}
	db.Requests[req.ID] = req

	// Save database
	err = db.save()
	if err != nil {
		t.Fatalf("failed to save database: %v", err)
	}

	// Load database again
	db2, err := NewDatabase(dbPath)
	if err != nil {
		t.Fatalf("failed to load database: %v", err)
	}

	loadedReq, exists := db2.Requests["test-id"]
	if !exists {
		t.Fatalf("expected request 'test-id' to exist in loaded database")
	}

	if loadedReq.Phone != req.Phone || loadedReq.Region != req.Region || loadedReq.Currency != req.Currency {
		t.Errorf("loaded request mismatch: got Phone=%s, Region=%s, Currency=%s", loadedReq.Phone, loadedReq.Region, loadedReq.Currency)
	}
}

func TestHandleAdminLoginLogout(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "api_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "db.json")
	db, _ := NewDatabase(dbPath)
	uploadsDir := filepath.Join(tempDir, "uploads")
	h, _ := NewAPIHandler(db, uploadsDir)

	// Test Login - Incorrect credentials
	loginBody := `{"username": "admin", "password": "wrongpassword"}`
	req := httptest.NewRequest("POST", "/api/admin/login", strings.NewReader(loginBody))
	w := httptest.NewRecorder()

	h.HandleAdminLogin(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401 Unauthorized, got %d", w.Code)
	}

	// Test Login - Correct credentials
	loginBodyCorrect := `{"username": "admin", "password": "chaka"}`
	reqCorrect := httptest.NewRequest("POST", "/api/admin/login", strings.NewReader(loginBodyCorrect))
	wCorrect := httptest.NewRecorder()

	h.HandleAdminLogin(wCorrect, reqCorrect)

	if wCorrect.Code != http.StatusOK {
		t.Errorf("expected status 200 OK, got %d", wCorrect.Code)
	}

	// Verify Cookie was set
	cookies := wCorrect.Result().Cookies()
	var sessionToken string
	for _, cookie := range cookies {
		if cookie.Name == "session_token" {
			sessionToken = cookie.Value
		}
	}
	if sessionToken == "" {
		t.Fatalf("session_token cookie was not set")
	}

	// Test Logout
	reqLogout := httptest.NewRequest("POST", "/api/admin/logout", nil)
	// Add session token cookie
	reqLogout.AddCookie(&http.Cookie{Name: "session_token", Value: sessionToken})
	wLogout := httptest.NewRecorder()

	h.HandleAdminLogout(wLogout, reqLogout)

	if wLogout.Code != http.StatusOK {
		t.Errorf("expected status 200 OK for logout, got %d", wLogout.Code)
	}
}

func TestHandlePostEstimateAndGetStatus(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "api_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "db.json")
	db, _ := NewDatabase(dbPath)
	uploadsDir := filepath.Join(tempDir, "uploads")
	h, _ := NewAPIHandler(db, uploadsDir)

	// Inject a pending request
	carReq := &CarRequest{
		ID:        "request-123",
		Phone:     "+375445555980",
		Region:    "Гродненская область",
		Status:    StatusPending,
		Currency:  "BYN",
	}
	db.Requests[carReq.ID] = carReq
	db.save()

	// 1. Check pending status via client API
	reqStatus := httptest.NewRequest("GET", "/api/requests/request-123/status", nil)
	wStatus := httptest.NewRecorder()
	h.HandleGetStatus(wStatus, reqStatus)

	if wStatus.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", wStatus.Code)
	}

	var fetchedReq CarRequest
	json.NewDecoder(wStatus.Body).Decode(&fetchedReq)

	if fetchedReq.Status != StatusPending || fetchedReq.Region != "Гродненская область" {
		t.Errorf("fetched request status/region mismatch: got status=%s, region=%s", fetchedReq.Status, fetchedReq.Region)
	}

	// 2. Submit valuation estimate (Admin)
	estimateBody := `{"min_price": 45000, "max_price": 50000, "currency": "$", "comment": "Отличное состояние"}`
	reqEstimate := httptest.NewRequest("POST", "/api/admin/requests/request-123/estimate", strings.NewReader(estimateBody))
	wEstimate := httptest.NewRecorder()
	h.HandlePostEstimate(wEstimate, reqEstimate)

	if wEstimate.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", wEstimate.Code)
	}

	// Verify DB state updated
	updatedReq := db.Requests["request-123"]
	if updatedReq.Status != StatusCompleted {
		t.Errorf("expected request status to be completed, got %s", updatedReq.Status)
	}
	if updatedReq.MinPrice != 45000 || updatedReq.MaxPrice != 50000 || updatedReq.Currency != "$" || updatedReq.Comment != "Отличное состояние" {
		t.Errorf("estimate values mismatch: got min=%f, max=%f, currency=%s, comment=%s", updatedReq.MinPrice, updatedReq.MaxPrice, updatedReq.Currency, updatedReq.Comment)
	}
}

func TestHandleDeleteRequest(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "api_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "db.json")
	db, _ := NewDatabase(dbPath)
	uploadsDir := filepath.Join(tempDir, "uploads")
	h, _ := NewAPIHandler(db, uploadsDir)

	// Inject a request
	carReq := &CarRequest{
		ID:        "request-delete-me",
		Phone:     "+375299876543",
		Region:    "Брестская область",
		Status:    StatusCompleted,
		Currency:  "BYN",
	}
	db.Requests[carReq.ID] = carReq
	db.save()

	// Mock file upload directory
	reqDir := filepath.Join(uploadsDir, carReq.ID)
	os.MkdirAll(reqDir, 0755)
	tempFile := filepath.Join(reqDir, "photo_1.jpg")
	os.WriteFile(tempFile, []byte("fake_image_bytes"), 0644)

	// Verify directory and file exists before delete
	if _, err := os.Stat(tempFile); os.IsNotExist(err) {
		t.Fatalf("test setup error: mock upload file does not exist")
	}

	// Call Delete request
	reqDelete := httptest.NewRequest("DELETE", "/api/admin/requests/request-delete-me", nil)
	wDelete := httptest.NewRecorder()
	h.HandlePostEstimate(wDelete, reqDelete) // Routed via HandlePostEstimate

	if wDelete.Code != http.StatusOK {
		t.Errorf("expected delete status 200, got %d", wDelete.Code)
	}

	// Verify request deleted from database map
	if _, exists := db.Requests[carReq.ID]; exists {
		t.Errorf("expected request to be removed from database")
	}

	// Verify files deleted from disk
	if _, err := os.Stat(reqDir); !os.IsNotExist(err) {
		t.Errorf("expected uploaded media directory to be deleted from disk")
	}
}
