package signaling

import (
	"bufio"
	"fmt"
	"strings"
	"sync"

	"github.com/tarm/serial"
)

// SignalHandler interface defines methods for handling signals
type SignalHandler interface {
	HandleSignal(signal string) error
}

// ArduinoSignal handles signals from Arduino via COM port
type ArduinoSignal struct {
	port     *serial.Port
	portName string
	baud     int
	mutex    sync.Mutex
	callback func(string) error
}

// NewArduinoSignal creates a new Arduino signal handler
func NewArduinoSignal(portName string, baud int, callback func(string) error) (*ArduinoSignal, error) {
	return &ArduinoSignal{
		portName: portName,
		baud:     baud,
		callback: callback,
	}, nil
}

// Connect establishes connection to the Arduino COM port and starts listening
func (a *ArduinoSignal) Connect() error {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	if a.port != nil {
		return nil
	}

	config := &serial.Config{
		Name: a.portName,
		Baud: a.baud,
	}

	port, err := serial.OpenPort(config)
	if err != nil {
		return fmt.Errorf("failed to open serial port: %v", err)
	}

	a.port = port

	// Start listening for signals in a goroutine
	go a.listen()

	return nil
}

// listen continuously reads from the serial port
func (a *ArduinoSignal) listen() {
	reader := bufio.NewReader(a.port)
	var buffer strings.Builder
	
	for {
		b, err := reader.ReadByte()
		if err != nil {
			fmt.Printf("Error reading from serial port: %v\n", err)
			break
		}
		
		// The Arduino code sends each character followed by a semicolon
		if b == ';' {
			// End of character marker, process what we have if not empty
			if buffer.Len() > 0 {
				signal := buffer.String()
				if a.callback != nil {
					if err := a.callback(signal); err != nil {
						fmt.Printf("Error handling signal: %v\n", err)
					}
				}
				buffer.Reset()
			}
		} else {
			// Add character to our buffer
			buffer.WriteByte(b)
		}
	}
}

// HandleSignal processes signals received from Arduino
func (a *ArduinoSignal) HandleSignal(signal string) error {
	if a.callback != nil {
		return a.callback(signal)
	}
	return nil
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
