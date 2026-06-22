# DiscoCardServer

A Windows service that provides API endpoints for card activation and status checking via a MySQL database.

## Features

- Windows Service support with install/uninstall/start/stop commands
- Console mode for development and testing
- RESTful API endpoints for card operations
- MySQL database connectivity
- INI file configuration

## Configuration

The service reads configuration from `config.ini` (located in the same directory as the executable):

```ini
[DATABASE]
db_name = xd
db_host = localhost
db_port = 3306
db_user = root
db_pass = mydbpassword

```

## Building

```bash
go mod tidy
go build -o discocardserver.exe
```

## Usage

### Console Mode (for development/testing)

```bash
discocardserver.exe -console
```

### Install as Windows Service

```bash
discocardserver.exe -install
```

### Start the Service

```bash
discocardserver.exe -start
```

### Stop the Service

```bash
discocardserver.exe -stop
```

### Uninstall the Service

```bash
discocardserver.exe -uninstall
```

## API Endpoints

### Activate Endpoint

**URL:** `/activate`

**Method:** GET or POST

**Parameters:**
- `cardnum` (query parameter or form field): The card number to activate

**Response:**
- Status: 200 OK
- Body: `OK`

Example:
```
GET http://localhost:8080/activate?cardnum=1234567890
```

### Status Endpoint

**URL:** `/status`

**Method:** GET or POST

**Parameters:**
- `cardnum` (query parameter or form field): The card number to check status

**Response:**
- Status: 200 OK
- Body: `OK`

Example:
```
GET http://localhost:8080/status?cardnum=1234567890
```

### Health Check Endpoint

**URL:** `/health`

**Method:** GET

**Response:**
- Status: 200 OK
- Body: `OK`

## Development Notes

- The `/activate` and `/status` endpoints currently return 200 OK with "OK" as the response body
- Database logic for these endpoints will be implemented in the future
- The service runs on port 8080 by default
- Database connection is established on service startup

## Dependencies

- `github.com/gorilla/mux` - HTTP router
- `golang.org/x/sys` - Windows service support
- `gopkg.in/ini.v1` - INI file parsing
- `github.com/go-sql-driver/mysql` - MySQL driver

## Service Name

- Service Name: `DiscoCardServer`
- Display Name: `DiscoCardServer`
- Description: `Disco Card Server API Service`