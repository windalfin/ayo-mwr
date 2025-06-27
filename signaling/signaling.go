package signaling

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/tarm/serial"

	"ayo-mwr/config"
)

// SignalHandler interface defines methods for handling signals
type SignalHandler interface {
	HandleSignal(signal string) error
}

// ArduinoSignal handles signals from Arduino via COM port
// ArduinoStatus represents current connection state
 type ArduinoStatus struct {
     Port       string    `json:"port"`
     BaudRate   int       `json:"baud_rate"`
     Connected  bool      `json:"connected"`
     LastSignal time.Time `json:"last_signal"`
 }

 // ArduinoSignal handles signals from Arduino via COM port
 type ArduinoSignal struct {
	port     *serial.Port
	portName string
	baud     int
	mutex    sync.Mutex
	callback func(string) error
	config   *config.Config // Reference to application config
    connected bool
    lastSignal time.Time
}

// NewArduinoSignal creates a new Arduino signal handler
var (
    activeArduino *ArduinoSignal
    activeMutex   sync.Mutex
    defaultCfg    *config.Config
)

func NewArduinoSignal(portName string, baud int, callback func(string) error, cfg *config.Config) (*ArduinoSignal, error) {
	return &ArduinoSignal{
		portName: portName,
		baud:     baud,
		callback: callback,
		config:   cfg,
	}, nil
}

// Connect establishes connection to the Arduino COM port and starts listening
func (a *ArduinoSignal) Connect() error {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	log.Printf("Attempting to connect to Arduino on port %s with baud rate %d", a.portName, a.baud)

	if a.port != nil {
		log.Printf("Arduino connection already established on port %s", a.portName)
		return nil
	}

	config := &serial.Config{
		Name: a.portName,
		Baud: a.baud,
	}

	port, err := serial.OpenPort(config)
	if err != nil {
		log.Printf("ERROR: Failed to open Arduino serial port: %v", err)
		return fmt.Errorf("failed to open serial port: %v", err)
	}

	a.port = port
	a.connected = true
	a.lastSignal = time.Now()
	log.Printf("Successfully connected to Arduino on port %s, now listening for signals", a.portName)

	// Heartbeat watchdog
	go a.heartbeat()
	// Start listening for signals in a goroutine
	go a.listen()

	return nil
}

// listen continuously reads from the serial port
func (a *ArduinoSignal) heartbeat() {
	ticker := time.NewTicker(5 * time.Second)
	for range ticker.C {
		if !a.connected {
			continue
		}
		if time.Since(a.lastSignal) > 10*time.Second {
			// no recent signal, test write
			a.mutex.Lock()
			if a.port != nil {
				_, err := a.port.Write([]byte{0})
				if err != nil {
					log.Printf("[Arduino] Heartbeat failed, marking disconnected: %v", err)
					a.connected = false
				}
			}
			a.mutex.Unlock()
		}
	}
    for range ticker.C {
        if !a.connected {
            continue
        }
        if time.Since(a.lastSignal) > 10*time.Second {
            // no recent signal, test write
            a.mutex.Lock()
            if a.port != nil {
                _, err := a.port.Write([]byte{0})
                if err != nil {
                    log.Printf("[Arduino] Heartbeat failed, marking disconnected: %v", err)
                    a.connected = false
                }
            }
            a.mutex.Unlock()
        }
    }
}

func (a *ArduinoSignal) listen() {
	log.Printf("Arduino listener started on port %s with baud rate %d - waiting for signals...", a.portName, a.baud)

	// Use a buffer for reading data
	buf := make([]byte, 128)

	// Log a waiting message every minute to confirm the listener is still active
	ticker := time.NewTicker(1 * time.Minute)
	go func() {
		for range ticker.C {
			log.Printf("Still waiting for Arduino signals on port %s...", a.portName)
		}
	}()

	// Use a separate goroutine for reading to avoid blocking
	go func() {
		fmt.Printf("[ARDUINO] Starting to read from port %s\n", a.portName)

		for {
			// Read directly from the port
			n, err := a.port.Read(buf)
			if err != nil {
				log.Printf("Error reading from Arduino: %v", err)
				fmt.Printf("[ARDUINO] Error reading from port: %v\n", err)
				time.Sleep(500 * time.Millisecond) // Wait before retrying
				continue
			}

			if n > 0 {
                a.lastSignal = time.Now()
				// Print both hex and ASCII representation of received bytes
				log.Printf("Received %d bytes from Arduino", n)
				hexStr := ""
				asciiStr := ""
				for i := 0; i < n; i++ {
					hexStr += fmt.Sprintf("%02X ", buf[i])
					if buf[i] >= 32 && buf[i] <= 126 { // Printable ASCII
						asciiStr += string(buf[i])
					} else {
						asciiStr += "."
					}
				}
				log.Printf("HEX: %s", hexStr)
				log.Printf("ASCII: %s", asciiStr)
				fmt.Printf("[ARDUINO] Received: HEX=%s ASCII='%s'\n", hexStr, asciiStr)

				// Process the signal using the handler (falls back to internal logic when callback is nil)
				signal := string(buf[:n])
				if err := a.HandleSignal(signal); err != nil {
					log.Printf("Error handling signal: %v", err)
					fmt.Printf("[ARDUINO] Error handling signal: %v\n", err)
				} else {
					log.Printf("Successfully processed signal: '%s'", signal)
					fmt.Printf("[ARDUINO] Successfully processed signal: '%s'\n", signal)
				}
			}
		}
	}()

	// Block this goroutine to keep it running
	<-make(chan struct{})

	ticker.Stop()
	log.Printf("Arduino listener on port %s has stopped", a.portName)
}

// HandleSignal processes signals received from Arduino
func (a *ArduinoSignal) HandleSignal(signal string) error {
	if a.callback != nil {
		return a.callback(signal)
	}

	fmt.Printf("Received signal: %s\n", signal)

	// Ignore semicolons as separate signals
	if signal == ";" || strings.TrimSpace(signal) == ";" {
		log.Printf("Ignoring semicolon as a separate signal")
		return nil
	}

	// Ignore carriage return and newline characters
	if strings.TrimSpace(signal) == "" || signal == "\r" || signal == "\n" || signal == "\r\n" {
		log.Printf("Ignoring whitespace/control characters")
		return nil
	}

	// Trim the trailing semicolon from the signal to get the button number
	buttonNo := strings.TrimSuffix(strings.TrimSpace(signal), ";")

    // Map button number to field ID and camera name using configuration
    fieldID := buttonNo // Default fallback
    cameraName := ""   // Empty means not provided

    if a.config != nil && a.config.CameraByButtonNo != nil {
        if cam, ok := a.config.CameraByButtonNo[buttonNo]; ok {
            if cam.Field != "" {
                fieldID = cam.Field
            }
            cameraName = cam.Name
            log.Printf("Mapped button %s -> fieldID=%s, cameraName=%s", buttonNo, fieldID, cameraName)
        } else {
            log.Printf("Warning: No camera mapping found for button %s", buttonNo)
        }
    } else {
        log.Printf("Warning: Camera configuration not available, using defaults")
    }

    // Call the API using the utility function, supplying both field ID and camera name (if available)
    err := CallProcessBookingVideoAPI(fieldID, cameraName)
	if err != nil {
		// The utility function already logs the specific error,
		// so we can just return a general error or the specific one.
		log.Printf("Error calling Process Booking Video API: %v", err) // Optional: log here as well
		return fmt.Errorf("failed to process booking video API call: %w", err)
	}

	// Log success
	log.Printf("Successfully initiated API call for button_no: %s, field_id: %s", buttonNo, fieldID)

	return nil
}

// GetStatus returns a snapshot of the current connection status
func GetArduinoStatus() ArduinoStatus {
    activeMutex.Lock()
    defer activeMutex.Unlock()
    if activeArduino == nil {
        if defaultCfg != nil {
            return ArduinoStatus{Port: defaultCfg.ArduinoCOMPort, BaudRate: defaultCfg.ArduinoBaudRate, Connected: false}
        }
        return ArduinoStatus{}
    }
    return ArduinoStatus{
        Port:       activeArduino.portName,
        BaudRate:   activeArduino.baud,
        Connected:  activeArduino.connected,
        LastSignal: activeArduino.lastSignal,
    }
}

// ReloadArduino closes existing connection (if any) and reinitializes using cfg.
func ReloadArduino(cfg *config.Config) error {
    activeMutex.Lock()
    if activeArduino != nil {
        _ = activeArduino.Close()
    }
    activeMutex.Unlock()
    _, err := InitArduino(cfg)
    return err
}

// Close closes the serial port connection
func (a *ArduinoSignal) Close() error {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	if a.port != nil {
		err := a.port.Close()
		a.port = nil
		return err
	}
	return nil
}

// FunctionSignal handles signals received via function calls
type FunctionSignal struct {
	callback func(string) error
}

// NewFunctionSignal creates a new function signal handler
func NewFunctionSignal(callback func(string) error) *FunctionSignal {
	return &FunctionSignal{
		callback: callback,
	}
}

// HandleSignal processes signals received via function calls
func (f *FunctionSignal) HandleSignal(signal string) error {
	if f.callback != nil {
		return f.callback(signal)
	}
	return nil
}

// InitArduino sets up an ArduinoSignal based on application configuration and starts listening.
func InitArduino(cfg *config.Config) (*ArduinoSignal, error) {
    // set default even if connection fails later
    defaultCfg = cfg
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}

	log.Printf("[SIGNALING] Initializing Arduino on port %s (baud %d)", cfg.ArduinoCOMPort, cfg.ArduinoBaudRate)

	arduino, err := NewArduinoSignal(cfg.ArduinoCOMPort, cfg.ArduinoBaudRate, nil, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create ArduinoSignal: %w", err)
	}

	if err := arduino.Connect(); err != nil {
		return nil, fmt.Errorf("failed to connect Arduino: %w", err)
	}

	log.Printf("[SIGNALING] Arduino connected and listening on %s", cfg.ArduinoCOMPort)
	// store as active
    defaultCfg = cfg
    activeMutex.Lock()
    activeArduino = arduino
    activeMutex.Unlock()
    return arduino, nil
}
