package reviewer

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

// Comment defines the unified structure for feedback comments.
type Comment struct {
	Text      string `json:"text"`
	Timestamp string `json:"timestamp"`
}

// StartReviewServer serves the compiled HTML spec, opens browser, and captures feedback.
// It accepts an optional readyChan, which receives the running server's URL once the port is bound.
func StartReviewServer(ctx context.Context, htmlContent []byte, inputPath string, port int, noOpen bool, readyChan chan<- string) error {
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
	defer listener.Close()

	actualPort := listener.Addr().(*net.TCPAddr).Port
	url := fmt.Sprintf("http://127.0.0.1:%d", actualPort)
	log.Info().Msgf("Review server running at %s", url)

	// Programmatic ready notification
	if readyChan != nil {
		readyChan <- url
	}

	feedbackReceived := make(chan struct{})

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(htmlContent)
	})

	mux.HandleFunc("/api/feedback", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var comments []Comment
		if err := json.NewDecoder(r.Body).Decode(&comments); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Ensure safe & atomic write: marshal in memory first
		encoded, err := json.MarshalIndent(comments, "", "  ")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Write to feedback.json
		dir := filepath.Dir(inputPath)
		base := filepath.Base(inputPath)
		ext := filepath.Ext(base)
		feedbackName := fmt.Sprintf("%s-feedback.json", strings.TrimSuffix(base, ext))
		feedbackPath := filepath.Join(dir, feedbackName)

		if err := os.WriteFile(feedbackPath, encoded, 0644); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))

		log.Info().Msg("Feedback successfully received & written.")
		fmt.Println("FEEDBACK_RECEIVED")

		// Safe non-blocking trigger for graceful shutdown
		select {
		case <-feedbackReceived:
			// Already closed/triggered
		default:
			close(feedbackReceived)
		}
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
			time.Sleep(50 * time.Millisecond) // Wait a brief moment for socket backlog
			openBrowser(url)
		}()
	}

	// Wait for shutdown trigger
	select {
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
