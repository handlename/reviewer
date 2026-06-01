package reviewer

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"
)

// StartReviewServer serves the compiled HTML spec, opens browser, and captures feedback
func StartReviewServer(ctx context.Context, htmlContent []byte, inputPath string, port int, noOpen bool) error {
	// Probing port
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Warn().Err(err).Msgf("port %d busy, probing for auto-assigned port", port)
		listener, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return fmt.Errorf("failed to bind any local port: %w", err)
		}
	}
	actualPort := listener.Addr().(*net.TCPAddr).Port
	url := fmt.Sprintf("http://127.0.0.1:%d", actualPort)
	log.Info().Msgf("Review server running at %s", url)

	feedbackReceived := make(chan bool, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(htmlContent)
	})

	mux.HandleFunc("/api/feedback", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var comments []interface{}
		if err := json.NewDecoder(r.Body).Decode(&comments); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Write to feedback.json
		dir := filepath.Dir(inputPath)
		base := filepath.Base(inputPath)
		ext := filepath.Ext(base)
		feedbackName := fmt.Sprintf("%s-feedback.json", strings.TrimSuffix(base, ext))
		feedbackPath := filepath.Join(dir, feedbackName)

		file, err := os.Create(feedbackPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer file.Close()

		encoder := json.NewEncoder(file)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(comments); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))

		log.Info().Msg("Feedback successfully received & written.")
		fmt.Println("FEEDBACK_RECEIVED")
		
		// Trigger graceful shutdown signal
		feedbackReceived <- true
	})

	server := &http.Server{
		Handler: mux,
	}

	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Error().Err(err).Msg("Server error")
		}
	}()

	// Launch browser if permitted
	if !noOpen {
		go func() {
			time.Sleep(100 * time.Millisecond) // Wait a brief moment for listen
			openBrowser(url)
		}()
	}

	// Listen to system signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	select {
	case <-sigChan:
		log.Info().Msg("Termination signal received. Shutting down server...")
	case <-feedbackReceived:
		log.Info().Msg("Feedback received. Shutting down server...")
	case <-ctx.Done():
		log.Info().Msg("Context cancelled. Shutting down server...")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	
	return server.Shutdown(shutdownCtx)
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default: // linux, etc.
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Run(); err != nil {
		log.Warn().Err(err).Msg("Could not automatically open browser")
	}
}
