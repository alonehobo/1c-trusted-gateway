package main

import (
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
)

//go:embed Icon.ico
var embeddedIcon []byte

// generateToken creates a URL-safe base64 token.
func generateToken(nBytes int) string {
	b := make([]byte, nBytes)
	_, _ = rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)
}

// openBrowser opens the default browser on the given URL.
func openBrowser(url string) {
	if runtime.GOOS == "windows" {
		_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	} else {
		_ = exec.Command("xdg-open", url).Start()
	}
}

// waitForShutdown blocks until an OS signal or the shutdown channel fires.
func waitForShutdown(shutdownCh chan struct{}) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	select {
	case <-sigCh:
	case <-shutdownCh:
	}
}

// app is the global TrustedWebApp instance, referenced by the SSE handler.
var app *TrustedWebApp

func main() {
	configPath := "config.json"
	if len(os.Args) > 1 {
		configPath = os.Args[1]
	}

	// Suppress console window in Windows GUI mode
	fmt.Println("1C Trusted Gateway starting...")

	// Use LaunchWeb which handles everything
	savedToken := ""
	var config *AppConfig

	if HasSavedSettings() {
		settingsData, _ := LoadSettings()
		if settingsData != nil {
			config = ConfigFromDict(settingsData)
			if authMap, ok := settingsData["auth"].(map[string]any); ok {
				if tok, ok := authMap["token"].(string); ok {
					savedToken = tok
				}
			}
		}
	}

	if config == nil {
		var err error
		config, err = LoadConfig(configPath)
		if err != nil {
			config = DefaultAppConfig()
		}
	}

	app = NewTrustedWebApp(config, savedToken)
	app.AutoSendToAgent = config.Defaults.AutoSendToAgent
	app.SkipNumericValues = config.Defaults.SkipNumericValues

	// Start HTTP server
	host := DefaultWebHost
	port := config.WebPort
	if port <= 0 {
		port = DefaultWebPort
	}

	httpd := NewWebHTTPServer(host, port, app)

	webURL := fmt.Sprintf("http://%s:%d/?token=%s", host, port, app.SessionToken)
	fmt.Printf("Web UI: %s\n", webURL)

	// Open browser
	openBrowser(webURL)

	// Start system tray
	shutdownCh := make(chan struct{}, 1)
	startTrayIcon(webURL, func() {
		select {
		case shutdownCh <- struct{}{}:
		default:
		}
	})

	// Start HTTP server in goroutine
	go func() {
		if err := httpd.ListenAndServe(); err != nil {
			fmt.Printf("HTTP server error: %v\n", err)
		}
	}()

	// Wait for shutdown signal
	waitForShutdown(shutdownCh)

	fmt.Println("\nЗавершение...")
	stopTrayIcon()
	app.Shutdown()
	httpd.ShutdownServer()
}
