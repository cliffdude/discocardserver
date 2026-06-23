package main

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
	"gopkg.in/ini.v1"
)

const (
	serviceName = "DiscoCardServer"
	serviceDesc = "Disco Card Server API Service"
)

var (
	configPath  string
	dbConfig    DatabaseConfig
	testMode    bool
	enableFoto  bool
	cameraIP    string
	cameraPort  string
	cameraUser  string
	cameraPass  string
	photoDir    string
	cameraDelay int // Delay in seconds before capturing photo after setting overlay
)

type DatabaseConfig struct {
	Name string
	Host string
	Port string
	User string
	Pass string
}

type myservice struct{}

func main() {
	flag.StringVar(&configPath, "config", "config.ini", "Path to configuration file")
	consoleMode := flag.Bool("console", false, "Run in console mode")
	installService := flag.Bool("install", false, "Install service")
	uninstallService := flag.Bool("uninstall", false, "Uninstall service")
	startService := flag.Bool("start", false, "Start service")
	stopService := flag.Bool("stop", false, "Stop service")
	flag.Parse()

	// Load configuration
	if err := loadConfig(); err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	switch {
	case *installService:
		install()
	case *uninstallService:
		uninstall()
	case *startService:
		start()
	case *stopService:
		stop()
	case *consoleMode:
		runConsole()
	default:
		// Run as Windows service
		runService()
	}
}

func loadConfig() error {
	// If configPath is relative, make it relative to executable
	if !filepath.IsAbs(configPath) {
		exePath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("failed to get executable path: %w", err)
		}
		exeDir := filepath.Dir(exePath)
		configPath = filepath.Join(exeDir, configPath)
	}

	cfg, err := ini.Load(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config file: %w", err)
	}

	section := cfg.Section("DATABASE")
	dbConfig = DatabaseConfig{
		Name: section.Key("db_name").String(),
		Host: section.Key("db_host").String(),
		Port: section.Key("db_port").String(),
		User: section.Key("db_user").String(),
		Pass: section.Key("db_pass").String(),
	}

	appSection := cfg.Section("APP")
	testMode = appSection.Key("test_mode").MustBool(false)
	enableFoto = appSection.Key("enablefoto").MustBool(false)

	cameraSection := cfg.Section("CAMERA")
	cameraIP = cameraSection.Key("camera_ip").String()
	cameraPort = cameraSection.Key("camera_port").String()
	cameraUser = cameraSection.Key("camera_user").String()
	cameraPass = cameraSection.Key("camera_pass").String()
	photoDir = cameraSection.Key("photo_dir").String()
	cameraDelay = cameraSection.Key("camera_delay").MustInt(1000) // Default to 1000ms (1 second)

	log.Printf("Configuration loaded from: %s", configPath)
	log.Printf("Test mode: %v", testMode)
	log.Printf("Enable foto: %v", enableFoto)
	return nil
}

func runService() {
	isIntSess, err := svc.IsWindowsService()
	if err != nil {
		log.Fatalf("failed to determine if we are running in service: %v", err)
	}

	if !isIntSess {
		log.Println("Not running as Windows service. Use -console flag for console mode.")
		return
	}

	log.Printf("Starting %s service...", serviceName)
	if err := svc.Run(serviceName, &myservice{}); err != nil {
		log.Fatalf("service failed: %v", err)
	}
}

func runConsole() {
	log.Printf("Running %s in console mode...", serviceName)
	startServer()
}

func (m *myservice) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (ssec bool, errno uint32) {
	const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown | svc.AcceptPauseAndContinue
	changes <- svc.Status{State: svc.StartPending}

	// Start the HTTP server in a goroutine
	go startServer()

	changes <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}

loop:
	for {
		select {
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				changes <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				break loop
			case svc.Pause:
				changes <- svc.Status{State: svc.Paused, Accepts: cmdsAccepted}
			case svc.Continue:
				changes <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}
			default:
				log.Printf("unexpected control request #%d", c)
			}
		}
	}

	changes <- svc.Status{State: svc.Stopped}
	return
}

func startServer() {
	// Initialize database connection
	if err := InitDB(); err != nil {
		log.Printf("Warning: Failed to connect to database: %v", err)
		log.Println("Service will continue running but database operations will fail")
	}
	defer CloseDB()

	// Load config masks into memory for fast Mesa number lookup
	if err := LoadConfigMasks(); err != nil {
		log.Printf("Warning: Failed to load config masks: %v", err)
		log.Println("Card to Mesa number lookup may not work correctly")
	}

	r := mux.NewRouter()

	// Serve static files (HTML, CSS, JS)
	r.PathPrefix("/static/").Handler(http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	// Serve photos
	r.PathPrefix("/photos/").Handler(http.StripPrefix("/photos/", http.FileServer(http.Dir(photoDir))))

	// Serve the card status page
	r.HandleFunc("/cardstatus", cardStatusPageHandler).Methods("GET")

	// API endpoints
	r.HandleFunc("/activate", activateHandler).Methods("GET", "POST")
	r.HandleFunc("/status", statusHandler).Methods("GET", "POST")
	r.HandleFunc("/api/cardstatus", cardStatusAPIHandler).Methods("GET")

	// Health check endpoint
	r.HandleFunc("/health", healthHandler).Methods("GET")

	port := "8080"
	log.Printf("Starting HTTP server on port %s", port)
	if err := http.ListenAndServe(":"+port, r); err != nil {
		log.Printf("HTTP server error: %v", err)
	}
}

func activateHandler(w http.ResponseWriter, r *http.Request) {
	cardNum := r.URL.Query().Get("cardnum")
	if cardNum == "" {
		cardNum = r.FormValue("cardnum")
	}

	log.Printf("Activate request for card: %s", cardNum)

	if testMode {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "OK")
		return
	}

	// Find Mesa number from card number using the in-memory mask map
	mesaId, found := FindMesaNumber(cardNum)
	if !found {
		log.Printf("No Mesa number found for card: %s", cardNum)
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "ERROR")
		return
	}
	log.Printf("Found Mesa number %d for card: %s", mesaId, cardNum)

	// Validate card in database using the Mesa number
	valid, err := ValidateCardInDatabase(fmt.Sprintf("%d", mesaId))
	if err != nil {
		log.Printf("Error validating card in database: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "ERROR")
		return
	}

	if !valid {
		log.Printf("Card validation failed for Mesa: %d", mesaId)
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "ERROR")
		return
	}

	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "OK")

	// Take photo if enabled
	if enableFoto {
		go takefoto(cardNum)
	}
}

// cameraLogin performs login to the camera and returns the auth cookie
func cameraLogin(client *http.Client, baseURL, username, password string) (string, error) {
	// First, get the login challenge
	loginURL := fmt.Sprintf("%s/cgi-bin/global.login?userName=%s", baseURL, username)

	req, err := http.NewRequest("GET", loginURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create login request: %w", err)
	}

	// Add browser headers
	req.Header.Add("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Add("Accept", "*/*")
	req.Header.Add("Connection", "keep-alive")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("login request failed: %w", err)
	}
	defer resp.Body.Close()

	// Check for 401 - this is expected
	if resp.StatusCode == http.StatusUnauthorized {
		authHeader := resp.Header.Get("WWW-Authenticate")
		if authHeader == "" {
			return "", fmt.Errorf("no WWW-Authenticate header in 401 response")
		}

		// Parse auth header to get realm
		params := make(map[string]string)
		parts := strings.Split(authHeader, ", ")
		for _, part := range parts {
			kv := strings.SplitN(part, "=", 2)
			if len(kv) == 2 {
				params[strings.TrimSpace(kv[0])] = strings.Trim(kv[1], `"`)
			}
		}

		realm := params["realm"]
		nonce := params["nonce"]
		qop := params["qop"]
		uri := "/cgi-bin/global.login?userName=" + username
		method := "GET"

		// Generate Digest auth response
		ha1 := fmt.Sprintf("%x", md5.Sum([]byte(username+":"+realm+":"+password)))
		ha2 := fmt.Sprintf("%x", md5.Sum([]byte(method+":"+uri)))

		cnonce := fmt.Sprintf("%x", md5.Sum([]byte(time.Now().String())))
		var response string
		if qop == "auth" || qop == "auth-int" {
			nc := "00000001"
			response = fmt.Sprintf("%x", md5.Sum([]byte(ha1+":"+nonce+":"+nc+":"+cnonce+":"+qop+":"+ha2)))
		} else {
			response = fmt.Sprintf("%x", md5.Sum([]byte(ha1+":"+nonce+":"+ha2)))
		}

		// Build Authorization header
		var authValue string
		if qop == "auth" || qop == "auth-int" {
			authValue = fmt.Sprintf(`Digest username="%s", realm="%s", nonce="%s", uri="%s", qop=%s, nc=00000001, cnonce="%s", response="%s"`,
				username, realm, nonce, uri, qop, cnonce, response)
		} else {
			authValue = fmt.Sprintf(`Digest username="%s", realm="%s", nonce="%s", uri="%s", response="%s"`,
				username, realm, nonce, uri, response)
		}

		// Retry login with Digest auth
		req.Header.Set("Authorization", authValue)
		resp2, err := client.Do(req)
		if err != nil {
			return "", fmt.Errorf("login with digest failed: %w", err)
		}
		defer resp2.Body.Close()

		// Check for session cookie
		for _, cookie := range resp2.Cookies() {
			if strings.Contains(cookie.Name, "session") || strings.Contains(cookie.Name, "DVRWeb") {
				return cookie.Value, nil
			}
		}

		// If no cookie, check response body for session info
		body, _ := io.ReadAll(resp2.Body)
		log.Printf("Login response: %s", string(body))

		if resp2.StatusCode != http.StatusOK {
			return "", fmt.Errorf("login failed with status %d: %s", resp2.StatusCode, string(body))
		}

		return "", nil
	}

	// Check for session cookie on success
	for _, cookie := range resp.Cookies() {
		if strings.Contains(cookie.Name, "session") || strings.Contains(cookie.Name, "DVRWeb") {
			return cookie.Value, nil
		}
	}

	return "", fmt.Errorf("unexpected login response status: %d", resp.StatusCode)
}

// cameraRequest makes an authenticated request to the camera
func cameraRequest(client *http.Client, req *http.Request, username, password string) (*http.Response, error) {
	// Try Basic auth first
	auth := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	req.Header.Set("Authorization", "Basic "+auth)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	// If Basic auth failed, try Digest auth
	if resp.StatusCode == http.StatusUnauthorized {
		authHeader := resp.Header.Get("WWW-Authenticate")
		if authHeader != "" && strings.Contains(authHeader, "Digest") {
			resp.Body.Close()

			// Parse WWW-Authenticate header
			authHeader = strings.TrimPrefix(authHeader, "Digest ")
			parts := strings.Split(authHeader, ", ")
			params := make(map[string]string)
			for _, part := range parts {
				kv := strings.SplitN(part, "=", 2)
				if len(kv) == 2 {
					params[strings.TrimSpace(kv[0])] = strings.Trim(kv[1], `"`)
				}
			}

			// Generate Digest auth response
			realm := params["realm"]
			nonce := params["nonce"]
			qop := params["qop"]
			uri := req.URL.RequestURI()
			method := req.Method
			ha1 := fmt.Sprintf("%x", md5.Sum([]byte(username+":"+realm+":"+password)))
			ha2 := fmt.Sprintf("%x", md5.Sum([]byte(method+":"+uri)))

			var response string
			cnonce := fmt.Sprintf("%x", md5.Sum([]byte(time.Now().String())))
			if qop == "auth" || qop == "auth-int" {
				nc := "00000001"
				response = fmt.Sprintf("%x", md5.Sum([]byte(ha1+":"+nonce+":"+nc+":"+cnonce+":"+qop+":"+ha2)))
			} else {
				response = fmt.Sprintf("%x", md5.Sum([]byte(ha1+":"+nonce+":"+ha2)))
			}

			// Build Authorization header
			var authValue string
			if qop == "auth" || qop == "auth-int" {
				authValue = fmt.Sprintf(`Digest username="%s", realm="%s", nonce="%s", uri="%s", qop=%s, nc=00000001, cnonce="%s", response="%s"`,
					username, realm, nonce, uri, qop, cnonce, response)
			} else {
				authValue = fmt.Sprintf(`Digest username="%s", realm="%s", nonce="%s", uri="%s", response="%s"`,
					username, realm, nonce, uri, response)
			}

			// Retry with Digest auth
			req.Header.Set("Authorization", authValue)
			return client.Do(req)
		}
	}

	return resp, nil
}

// setCameraOverlay sets the channel title overlay on the camera
func setCameraOverlay(client *http.Client, baseURL, username, password, overlayText string) error {
	// According to the PDF, we need to set multiple parameters for VideoWidget
	// Format: http://<ip>/cgi-bin/configManager.cgi?action=setConfig&<paramName>=<paramValue>[&<paramName>=<paramValue>...]

	// Build URL with all required parameters
	// We need to set: Name, EncodeBlend, and Rect (position)
	setURL := fmt.Sprintf("%s/cgi-bin/configManager.cgi?action=setConfig&ChannelTitle[0].Name=%s", baseURL, overlayText)
	//http://192.168.1.126/cgi-bin/configManager.cgi?action=setConfig&ChannelTitle[0].Name=blablabla
	req, err := http.NewRequest("GET", setURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create overlay request: %w", err)
	}

	// Add browser headers
	req.Header.Add("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Add("Accept", "*/*")

	resp, err := cameraRequest(client, req, username, password)
	if err != nil {
		return fmt.Errorf("failed to set overlay: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("camera returned status %d for overlay: %s", resp.StatusCode, string(body))
	}

	// Check response body for "OK"
	body, _ := io.ReadAll(resp.Body)
	responseStr := string(body)
	if !strings.Contains(responseStr, "OK") {
		return fmt.Errorf("overlay setting failed: %s", responseStr)
	}

	return nil
}

func takefoto(cardNum string) {
	// Create photos directory if it doesn't exist
	if err := os.MkdirAll(photoDir, 0755); err != nil {
		log.Printf("Failed to create photo directory: %v", err)
		return
	}

	// Generate filename with card number and timestamp
	timestamp := time.Now().Format("20060102_150405")
	filename := fmt.Sprintf("%s_%s.jpg", cardNum, timestamp)
	filepath := filepath.Join(photoDir, filename)

	// Build camera base URL
	cameraBaseURL := fmt.Sprintf("http://%s:%s", cameraIP, cameraPort)

	// Create HTTP client
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	// Set overlay text with card number
	overlayText := fmt.Sprintf("Card_%s", cardNum)
	if err := setCameraOverlay(client, cameraBaseURL, cameraUser, cameraPass, overlayText); err != nil {
		log.Printf("Failed to set camera overlay: %v", err)
		// Continue anyway, overlay failure shouldn't prevent photo capture
	}

	// Wait for camera to apply the overlay change (configurable delay in milliseconds)
	// This won't freeze the application since takefoto() runs in a goroutine
	time.Sleep(time.Duration(cameraDelay) * time.Millisecond)

	// Build camera snapshot URL
	cameraURL := fmt.Sprintf("%s/cgi-bin/snapshot.cgi", cameraBaseURL)
	log.Printf("Camera URL: %s", cameraURL)

	// Create HTTP request
	req, err := http.NewRequest("GET", cameraURL, nil)
	if err != nil {
		log.Printf("Failed to create camera request: %v", err)
		return
	}

	// Add common browser headers that cameras often require
	req.Header.Add("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
	req.Header.Add("Accept", "image/*")
	req.Header.Add("Accept-Language", "en-US,en;q=0.9")
	req.Header.Add("Connection", "keep-alive")

	// Send request with auth support
	resp, err := cameraRequest(client, req, cameraUser, cameraPass)
	if err != nil {
		log.Printf("Failed to capture photo from camera: %v", err)
		return
	}
	defer resp.Body.Close()

	log.Printf("Camera response status: %d", resp.StatusCode)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("Camera response body: %s", string(body))
		return
	}

	// Create output file
	outFile, err := os.Create(filepath)
	if err != nil {
		log.Printf("Failed to create photo file: %v", err)
		return
	}
	defer outFile.Close()

	// Copy image data to file
	if _, err := io.Copy(outFile, resp.Body); err != nil {
		log.Printf("Failed to save photo: %v", err)
		return
	}

	log.Printf("Photo saved successfully: %s", filepath)
}

func statusHandler(w http.ResponseWriter, r *http.Request) {
	cardNum := r.URL.Query().Get("cardnum")
	if cardNum == "" {
		cardNum = r.FormValue("cardnum")
	}

	log.Printf("Status request for card: %s", cardNum)

	// TODO: Implement database logic for status check

	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "OK")
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "OK")
}

// cardStatusPageHandler serves the card status HTML page
func cardStatusPageHandler(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "static/cardstatus.html")
}

// cardStatusAPIHandler handles API requests for card status information
func cardStatusAPIHandler(w http.ResponseWriter, r *http.Request) {
	cardNum := r.URL.Query().Get("cardnum")
	if cardNum == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Card number is required"})
		return
	}

	log.Printf("Card status API request for card: %s", cardNum)

	// Get card status from database
	cardStatus, err := GetCardStatus(cardNum)
	if err != nil {
		log.Printf("Error getting card status: %v", err)
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	// Return JSON response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(cardStatus)
}

func install() {
	m, err := mgr.Connect()
	if err != nil {
		log.Fatalf("failed to connect to service manager: %v", err)
	}
	defer m.Disconnect()

	exePath, err := os.Executable()
	if err != nil {
		log.Fatalf("failed to get executable path: %v", err)
	}

	s, err := m.OpenService(serviceName)
	if err == nil {
		s.Close()
		log.Printf("Service %s already exists", serviceName)
		return
	}

	s, err = m.CreateService(serviceName, exePath, mgr.Config{
		DisplayName: serviceName,
		Description: serviceDesc,
		StartType:   mgr.StartAutomatic,
	})
	if err != nil {
		log.Fatalf("failed to create service: %v", err)
	}
	defer s.Close()

	log.Printf("Service %s installed successfully", serviceName)
}

func uninstall() {
	m, err := mgr.Connect()
	if err != nil {
		log.Fatalf("failed to connect to service manager: %v", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err != nil {
		log.Fatalf("service %s not found: %v", serviceName, err)
	}
	defer s.Close()

	err = s.Delete()
	if err != nil {
		log.Fatalf("failed to delete service: %v", err)
	}

	log.Printf("Service %s uninstalled successfully", serviceName)
}

func start() {
	m, err := mgr.Connect()
	if err != nil {
		log.Fatalf("failed to connect to service manager: %v", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err != nil {
		log.Fatalf("service %s not found: %v", serviceName, err)
	}
	defer s.Close()

	err = s.Start()
	if err != nil {
		log.Fatalf("failed to start service: %v", err)
	}

	log.Printf("Service %s started successfully", serviceName)
}

func stop() {
	m, err := mgr.Connect()
	if err != nil {
		log.Fatalf("failed to connect to service manager: %v", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err != nil {
		log.Fatalf("service %s not found: %v", serviceName, err)
	}
	defer s.Close()

	status, err := s.Control(svc.Stop)
	if err != nil {
		log.Fatalf("failed to stop service: %v", err)
	}

	log.Printf("Service %s stopped successfully. Status: %d", serviceName, status)
}
