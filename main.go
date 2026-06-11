package main

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"skolkozaavto/handlers"
	"syscall"
	"time"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Programmatically ensure PWA launcher icons exist
	if err := ensureIconsExist(); err != nil {
		log.Printf("Warning: failed to generate PWA icons: %v", err)
	}

	dbPath := "./data/db.sqlite"
	uploadsDir := "./uploads"

	// Initialize database
	db, err := handlers.NewDatabase(dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	// Initialize API Handler
	h, err := handlers.NewAPIHandler(db, uploadsDir)
	if err != nil {
		log.Fatalf("Failed to initialize API Handler: %v", err)
	}

	// APIs
	http.HandleFunc("/api/upload", h.EnableCORS(h.HandleUpload))
	http.HandleFunc("/api/requests/", h.EnableCORS(h.HandleGetStatus))
	
	// Admin APIs (Login/Logout & Authenticated endpoints)
	http.HandleFunc("/api/admin/login", h.EnableCORS(h.HandleAdminLogin))
	http.HandleFunc("/api/admin/logout", h.EnableCORS(h.HandleAdminLogout))
	http.HandleFunc("/api/admin/requests", h.EnableCORS(h.RequireAuth(h.HandleGetAdminRequests)))
	http.HandleFunc("/api/admin/requests/", h.EnableCORS(h.RequireAuth(h.HandlePostEstimate)))


	// Uploads static server
	http.Handle("/uploads/", http.StripPrefix("/uploads/", http.FileServer(http.Dir(uploadsDir))))

	// Custom route for Specialist/Admin interface
	http.HandleFunc("/admin", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./static/admin.html")
	})

	// Frontend static assets
	http.Handle("/", http.FileServer(http.Dir("./static")))

	server := &http.Server{
		Addr:    ":" + port,
		Handler: nil,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("==================================================")
		log.Printf("  SkolkoZaAvto Server started successfully!")
		log.Printf("  Local URL:   http://localhost:%s", port)
		log.Printf("  Admin Panel: http://localhost:%s/admin", port)
		log.Printf("==================================================")

		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	<-stop
	log.Printf("Shutting down server gracefully...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("Server forced to shutdown: %v", err)
	}

	// Close database connection cleanly
	if err := db.Close(); err != nil {
		log.Printf("Error closing database: %v", err)
	}

	log.Printf("Server stopped.")
}

// Generate PWA icons if they do not exist
func ensureIconsExist() error {
	iconDir := "./static/icons"
	if err := os.MkdirAll(iconDir, 0755); err != nil {
		return err
	}

	sizes := []int{192, 512}
	for _, size := range sizes {
		path := filepath.Join(iconDir, fmt.Sprintf("icon-%d.png", size))
		if _, err := os.Stat(path); err == nil {
			// Already exists
			continue
		}
		
		log.Printf("Generating launcher icon: %s (%dx%d)...", path, size, size)
		if err := generateIcon(size, path); err != nil {
			return err
		}
	}
	return nil
}

func generateIcon(size int, filename string) error {
	img := image.NewRGBA(image.Rect(0, 0, size, size))

	// 1. Dark background
	bg := color.RGBA{8, 12, 20, 255}
	draw.Draw(img, img.Bounds(), &image.Uniform{bg}, image.Point{}, draw.Src)

	// 2. Glowing circular gradient in center
	center := size / 2
	radius := int(float64(size) * 0.38)
	radiusSq := radius * radius

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx := x - center
			dy := y - center
			distSq := dx*dx + dy*dy
			if distSq < radiusSq {
				factor := float64(radiusSq-distSq) / float64(radiusSq)
				
				// Sky blue to electric blue gradient components
				r := uint8(0 * factor)
				g := uint8(198 * factor)
				b := uint8(255 * factor)
				
				orig := img.RGBAAt(x, y)
				blended := color.RGBA{
					R: uint8(float64(orig.R)*(1-factor) + float64(r)*factor),
					G: uint8(float64(orig.G)*(1-factor) + float64(g)*factor),
					B: uint8(float64(orig.B)*(1-factor) + float64(b)*factor),
					A: 255,
				}
				img.SetRGBA(x, y, blended)
			}
		}
	}

	// 3. Draw a modern, sleek minimalist car icon in the center
	carW := int(float64(size) * 0.45)
	carH := int(float64(size) * 0.18)
	carX := center - carW/2
	carY := center - carH/3

	// White/Silver car body
	carColor := color.RGBA{255, 255, 255, 255}
	for y := carY; y < carY+carH; y++ {
		for x := carX; x < carX+carW; x++ {
			img.SetRGBA(x, y, carColor)
		}
	}

	// Cabin (upper trapezoid)
	cabW := int(float64(carW) * 0.6)
	cabH := int(float64(size) * 0.08)
	cabX := center - cabW/2
	cabY := carY - cabH

	for y := cabY; y < cabY+cabH; y++ {
		// Slope cabin sides
		offset := (y - cabY) * 2 // slant ratio
		for x := cabX + offset; x < cabX+cabW-offset; x++ {
			img.SetRGBA(x, y, carColor)
		}
	}

	// Glowing cyan wheels
	wheelR := int(float64(size) * 0.065)
	wheelRSq := wheelR * wheelR
	wheelY := carY + carH
	wheelX1 := carX + carW/4
	wheelX2 := carX + 3*carW/4

	drawWheel := func(wx, wy int) {
		for y := wy - wheelR; y <= wy+wheelR; y++ {
			for x := wx - wheelR; x <= wx+wheelR; x++ {
				dx := x - wx
				dy := y - wy
				if dx*dx+dy*dy <= wheelRSq {
					// Draw tyre border
					if dx*dx+dy*dy > int(float64(wheelRSq)*0.4) {
						img.SetRGBA(x, y, color.RGBA{0, 198, 255, 255})
					} else {
						// Tyre hub
						img.SetRGBA(x, y, color.RGBA{255, 255, 255, 255})
					}
				}
			}
		}
	}

	drawWheel(wheelX1, wheelY)
	drawWheel(wheelX2, wheelY)

	// Save to disk
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	return png.Encode(f, img)
}

