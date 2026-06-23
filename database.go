package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

var db *sql.DB

// ConfigMaskData represents the JSON structure in the Data field of config table
type ConfigMaskData struct {
	MaskId         int    `json:"MaskId"`
	MaskName       string `json:"MaskName"`
	MaskOrder      int    `json:"MaskOrder"`
	Mask           string `json:"Mask"`
	Active         bool   `json:"Active"`
	MaskType       int    `json:"MaskType"`
	ExplicitValue  bool   `json:"ExplicitValue"`
	Prefix         string `json:"Prefix"`
	VariableLength int    `json:"VariableLength"`
	UseCheckDigit  bool   `json:"UseCheckDigit"`
}

// Global in-memory map for Mask to MaskId lookup
var (
	maskToMesaMap = make(map[string]int)
	maskMapMutex  sync.RWMutex
)

// InitDB initializes the database connection using configuration from config.ini
func InitDB() error {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true",
		dbConfig.User,
		dbConfig.Pass,
		dbConfig.Host,
		dbConfig.Port,
		dbConfig.Name,
	)

	var err error
	db, err = sql.Open("mysql", dsn)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	if err = db.Ping(); err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	log.Printf("Connected to database: %s@%s:%s/%s", dbConfig.User, dbConfig.Host, dbConfig.Port, dbConfig.Name)
	return nil
}

// CloseDB closes the database connection
func CloseDB() error {
	if db != nil {
		return db.Close()
	}
	return nil
}

// GetDB returns the database instance
func GetDB() *sql.DB {
	return db
}

// TODO: Add database query methods for activate and status endpoints
// These will be implemented when the database schema is defined

// LoadConfigMasks loads all barcode mask configurations from the config table into memory
// This creates a fast lookup map from Mask to MaskId (Mesa number)
func LoadConfigMasks() error {
	if db == nil {
		return fmt.Errorf("database connection not initialized")
	}

	query := "SELECT Data FROM config WHERE Id = 'BARCODEMASKCONFIG'"
	rows, err := db.Query(query)
	if err != nil {
		return fmt.Errorf("failed to query config table: %w", err)
	}
	defer rows.Close()

	maskMapMutex.Lock()
	defer maskMapMutex.Unlock()

	// Clear existing map
	maskToMesaMap = make(map[string]int)

	count := 0
	for rows.Next() {
		var dataJSON string
		if err := rows.Scan(&dataJSON); err != nil {
			log.Printf("Warning: failed to scan config data: %v", err)
			continue
		}

		var maskData ConfigMaskData
		if err := json.Unmarshal([]byte(dataJSON), &maskData); err != nil {
			log.Printf("Warning: failed to unmarshal config data: %v", err)
			continue
		}

		// Only add active masks
		if maskData.Active {
			maskToMesaMap[maskData.Mask] = maskData.MaskId
			count++
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating config rows: %w", err)
	}

	log.Printf("Loaded %d active mask configurations into memory", count)
	return nil
}

// FindMesaNumber finds the Mesa number (MaskId) for a given card number
// It searches through all masks to find one that matches the card number
func FindMesaNumber(cardNum string) (int, bool) {
	maskMapMutex.RLock()
	defer maskMapMutex.RUnlock()

	// Try exact match first
	if mesaId, found := maskToMesaMap[cardNum]; found {
		return mesaId, true
	}

	// Try prefix matching - check if card number starts with any mask
	for mask, mesaId := range maskToMesaMap {
		if strings.HasPrefix(cardNum, mask) {
			return mesaId, true
		}
	}

	return 0, false
}

// ValidateCardInDatabase checks if a card exists in xconfigsaleszonesareaobjects table
// If it exists with status = 0, updates status to 4 and returns true. Otherwise, creates a new entry.
func ValidateCardInDatabase(cardNum string) (bool, error) {
	if db == nil {
		return false, fmt.Errorf("database connection not initialized")
	}

	// First, check if the card exists in the database
	var status int
	query := "SELECT Status FROM xconfigsalezonesareaobjects WHERE Id = ?"
	err := db.QueryRow(query, cardNum).Scan(&status)

	if err == nil {
		// Card found, check if status is 0
		if status == 0 {
			// Update status to 4
			updateQuery := "UPDATE xconfigsalezonesareaobjects SET Status = 4 WHERE Id = ?"
			_, err = db.Exec(updateQuery, cardNum)
			if err != nil {
				return false, fmt.Errorf("failed to update card status: %w", err)
			}
			log.Printf("Updated status to 4 for card: %s", cardNum)
			return true, nil
		}
		// Status is not 0, card is already activated
		return false, fmt.Errorf("Card already activated")
	} else if err != sql.ErrNoRows {
		// Some other error occurred
		return false, fmt.Errorf("failed to query database: %w", err)
	}

	// Card doesn't exist or status != 0, create new entry
	insertQuery := `INSERT INTO xconfigsalezonesareaobjects
		(Id, SaleZoneAreaId, Description, Status, Total, SubTotalReference,
		XPrinter1, XPrinter2, XPrinter3, XPrinter4, XPrinter5, XPrinter6, XPrinter7, XPrinter8,
		XPrinter9, XPrinter10, XPrinter11, XPrinter12, XPrinter13, XPrinter14, XPrinter15,
		XPrinter16, XPrinter17, XPrinter18, XPrinter19, XPrinter20,
		SContaPrinterId, FContaPrinterId, DefaultSerieId, DefaultDocumentTypeId,
		ServiceTxCanceled, Terminal, Discount, InvoiceObs, NumberPersons, CustomerKeyId,
		LoginDate, LogoutDate, ExclusiveTerminal, SyncStamp, FreeTable, CardSerieId,
		SaleZoneAreaObjectId1, SaleZoneAreaObjectId2, Inactive, BlockTransferTo,
		BlockTransferFrom, InitialUserOnly, InitialUser, PublicRelationsId, SchedulerResource,
		ServiceTxSuspended, PrintOrderOnCloseAccount, CloudSyncStamp)
		VALUES (?, 1, ?, 4, 0.000, '',
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
		42, 15, 0, 1, 0.000000, '', 0, '0', NOW(), '0001-01-01 00:00:00', 0, '2024-05-26 21:19:35',
		0, 0, 0, 0, 1, 0, 0, 0, 8, 0, '00000000-0000-0000-0000-000000000000', 0, 0, NULL)`

	_, err = db.Exec(insertQuery, cardNum, cardNum)
	if err != nil {
		return false, fmt.Errorf("failed to insert new card entry: %w", err)
	}

	log.Printf("Created new entry for card: %s", cardNum)
	return true, nil
}

// CardStatusData represents the complete card status information
type CardStatusData struct {
	CardNumber         string     `json:"cardNumber"`
	MesaNumber         string     `json:"mesaNumber"`
	Status             string     `json:"status"`
	PhotoUrl           string     `json:"photoUrl"`
	Items              []CardItem `json:"items"`
	TotalAmount        float64    `json:"totalAmount"`
	MinimumConsumption float64    `json:"minimumConsumption"`
}

// CardItem represents an item on the card
type CardItem struct {
	Description string  `json:"description"`
	Quantity    float64 `json:"quantity"`
	Total       float64 `json:"total"`
}

// GetCardStatus retrieves complete card status information from the database
func GetCardStatus(cardNum string) (*CardStatusData, error) {
	if db == nil {
		return nil, fmt.Errorf("database connection not initialized")
	}

	// Find Mesa number from card number
	mesaId, found := FindMesaNumber(cardNum)
	if !found {
		return nil, fmt.Errorf("card number not found in mask configuration")
	}

	mesaNum := fmt.Sprintf("%d", mesaId)

	// Get current status from xconfigsalezonesareaobjects
	var status int
	statusQuery := "SELECT Status FROM xconfigsalezonesareaobjects WHERE Id = ?"
	err := db.QueryRow(statusQuery, mesaNum).Scan(&status)
	if err != nil {
		if err == sql.ErrNoRows {
			// Card found in masks but not in saleszoneareaobjects table
			return nil, fmt.Errorf("Cartão não ativado")
		}
		return nil, fmt.Errorf("failed to query card status: %w", err)
	}

	// Get items from documentsbodystmp
	itemsQuery := "SELECT itemdescription, quantity, total FROM tmpdocumentstables WHERE SaleZoneAreaObjectid = ?"
	rows, err := db.Query(itemsQuery, mesaNum)
	if err != nil {
		return nil, fmt.Errorf("failed to query card items: %w", err)
	}
	defer rows.Close()

	var items []CardItem
	var totalAmount float64

	for rows.Next() {
		var item CardItem
		err := rows.Scan(&item.Description, &item.Quantity, &item.Total)
		if err != nil {
			log.Printf("Warning: failed to scan item: %v", err)
			continue
		}
		items = append(items, item)
		totalAmount += item.Total
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating items: %w", err)
	}

	// Get minimum consumption from seriesdiscount table
	var minConsumption float64
	minConsumptionQuery := "SELECT minconsumption FROM seriesdiscount WHERE ? BETWEEN startserie AND endserie"
	err = db.QueryRow(minConsumptionQuery, mesaId).Scan(&minConsumption)
	if err != nil {
		if err == sql.ErrNoRows {
			log.Printf("No minimum consumption found for mesaID %d", mesaId)
			minConsumption = 0.0
		} else {
			log.Printf("Warning: failed to query minimum consumption: %v", err)
			minConsumption = 0.0
		}
	}

	// Build card status data
	cardStatus := &CardStatusData{
		CardNumber:         cardNum,
		MesaNumber:         mesaNum,
		Status:             getStatusText(status),
		PhotoUrl:           getPhotoUrl(cardNum),
		Items:              items,
		TotalAmount:        totalAmount,
		MinimumConsumption: minConsumption,
	}

	return cardStatus, nil
}

// getStatusText converts status code to human-readable text
func getStatusText(status int) string {
	statusMap := map[int]string{
		0: "Inativo",
		1: "Consumo",
		2: "Pago",
		3: "Pendente",
		4: "Ativado",
	}

	if text, found := statusMap[status]; found {
		return text
	}
	return fmt.Sprintf("Unknown (%d)", status)
}

// getPhotoUrl constructs the URL for the card photo
func getPhotoUrl(cardNum string) string {
	// Check if photo directory is configured
	if photoDir == "" {
		return ""
	}

	// Look for photo files matching the card number pattern
	// Photos are named like: 0002830333_20260622_084956.jpg
	// We'll search for files starting with the card number
	pattern := filepath.Join(photoDir, cardNum+"*.jpg")

	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		return ""
	}

	// Return the most recent photo (last modified)
	var latestPhoto string
	var latestModTime time.Time

	for _, match := range matches {
		info, err := os.Stat(match)
		if err != nil {
			continue
		}
		if info.ModTime().After(latestModTime) {
			latestModTime = info.ModTime()
			latestPhoto = match
		}
	}

	if latestPhoto == "" {
		return ""
	}

	// Return relative path from static directory
	// Assuming photos are served from /photos/ endpoint
	return "/photos/" + filepath.Base(latestPhoto)
}
