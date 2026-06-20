package main

import (
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
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
	configPath string
	dbConfig   DatabaseConfig
	testMode   bool
	enableFoto bool
	cameraIP   string
	cameraPort string
	cameraUser string
	cameraPass string
	photoDir   string
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

	// API endpoints
	r.HandleFunc("/activate", activateHandler).Methods("GET", "POST")
	r.HandleFunc("/status", statusHandler).Methods("GET", "POST")

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

	// Build camera snapshot URL
	cameraURL := fmt.Sprintf("http://%s:%s/cgi-bin/snapshot.cgi", cameraIP, cameraPort)

	// Create HTTP request
	req, err := http.NewRequest("GET", cameraURL, nil)
	if err != nil {
		log.Printf("Failed to create camera request: %v", err)
		return
	}

	// Add basic authentication
	auth := base64.StdEncoding.EncodeToString([]byte(cameraUser + ":" + cameraPass))
	req.Header.Add("Authorization", "Basic "+auth)

	// Send request
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Failed to capture photo from camera: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Camera returned status %d", resp.StatusCode)
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
