// Package mpvipc provides an interface for communicating with the mpv media
// player via it's JSON IPC interface
package mpvipc

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"sync"
)

// Connection represents a connection to a mpv IPC socket
type Connection struct {
	client     net.Conn
	socketName string

	lastRequest     uint
	waitingRequests map[uint]func(*CommandResult)

	lastListener   uint
	eventListeners map[uint]func(*Event)

	lock sync.Mutex
}

// Event represents an event received from mpv. For a list of all possible
// events, see https://mpv.io/manual/master/#list-of-events
type Event struct {
	// Name is the only obligatory field: the name of the event
	Name string `json:"event"`

	// Reason is the reason for the event: currently used for the "end-file"
	// event. When Name is "end-file", possible values of Reason are:
	// "eof", "stop", "quit", "error", "redirect", "unknown"
	Reason string `json:"reason"`

	// Prefix is the log-message prefix (only if Name is "log-message")
	Prefix string `json:"prefix"`

	// Level is the loglevel for a log-message (only if Name is "log-message")
	Level string `json:"level"`

	// Text is the text of a log-message (only if Name is "log-message")
	Text string `json:"text"`

	// ID is the user-set property ID (on events triggered by observed properties)
	ID uint `json:"id"`

	// Data is the property value (on events triggered by observed properties)
	Data interface{} `json:"data"`

	// Error is present if Reason is "error."
	Error string `json:"error"`
}

// NewConnection returns a Connection associated with the given unix socket
func NewConnection(socketName string) *Connection {
	return &Connection{
		socketName:      socketName,
		waitingRequests: make(map[uint]func(*CommandResult)),
		eventListeners:  make(map[uint]func(*Event)),
	}
}

// Open connects to the socket. Returns an error if already connected.
// It also starts listening to events, so ListenForEvents() can be called
// afterwards.
func (c *Connection) Open() error {
	c.lock.Lock()
	defer c.lock.Unlock()

	if c.client != nil {
		return fmt.Errorf("already open")
	}
	client, err := dial(c.socketName)
	if err != nil {
		return fmt.Errorf("can't connect to mpv's socket: %s", err)
	}
	c.client = client
	go c.listen()
	return nil
}

// ListenForEvents adds the given event callback into the listener set. The
// returned callback will return the channel when called. The given callback
// will be called in the main loop that listens for events, so any work should
// be distributed to another thread.
func (c *Connection) ListenForEvents(onEvent func(*Event)) func() {
	c.lock.Lock()
	c.lastListener++
	id := c.lastListener
	c.eventListeners[id] = onEvent
	c.lock.Unlock()

	return func() {
		c.lock.Lock()
		delete(c.eventListeners, id)
		c.lock.Unlock()
	}
}

// Call calls an arbitrary command and returns its result. For a list of
// possible functions, see https://mpv.io/manual/master/#commands and
// https://mpv.io/manual/master/#list-of-input-commands
func (c *Connection) Call(arguments ...interface{}) (data interface{}, err error) {
	finish := make(chan struct{})

	callErr := c.CallAsync(func(v interface{}, e error) {
		data, err = v, e
		finish <- struct{}{}
	}, arguments...)

	if callErr != nil {
		return nil, err
	}

	<-finish

	return
}

// CallAsync does what Call does, but it does not block until there's a reply.
// If f is nil, then no reply is waited. f is called in the main loop, so it
// shouldn't do intensive work.
func (c *Connection) CallAsync(f func(v interface{}, err error), args ...interface{}) error {
	c.lock.Lock()

	c.lastRequest++
	id := c.lastRequest

	if f != nil {
		c.waitingRequests[id] = func(r *CommandResult) {
			if r.Status == "success" {
				f(r.Data, nil)
			} else {
				f(nil, fmt.Errorf("mpv error: %s", r.Status))
			}

			c.lock.Lock()
			delete(c.waitingRequests, id)
			c.lock.Unlock()
		}
	}

	c.lock.Unlock()

	return c.SendCommand(id, args...)
}

// Set is a shortcut to Call("set_property", property, value)
func (c *Connection) Set(property string, value interface{}) error {
	_, err := c.Call("set_property", property, value)
	return err
}

// SetAsync sets the property asynchronously. The returned error will only cover
// sending. f is optional; if it is not nil, then it'll be called afterwards.
func (c *Connection) SetAsync(property string, value interface{}, f func(error)) error {
	var asyncFn func(interface{}, error)
	if f != nil {
		asyncFn = func(_ interface{}, err error) { f(err) }
	}

	return c.CallAsync(asyncFn, "set_property", property, value)
}

// Get is a shortcut to Call("get_property", property)
func (c *Connection) Get(property string) (interface{}, error) {
	value, err := c.Call("get_property", property)
	return value, err
}

// Close closes the socket, disconnecting from mpv. It is safe to call Close()
// on an already closed connection.
func (c *Connection) Close() error {
	c.lock.Lock()
	defer c.lock.Unlock()

	if c.client != nil {
		err := c.client.Close()
		c.client = nil
		return err
	}
	return nil
}

// IsClosed returns true if the connection is closed. There are several cases
// in which a connection is closed:
//
// 1. Close() has been called
//
// 2. The connection has been initialised but Open() hasn't been called yet
//
// 3. The connection terminated because of an error, mpv exiting or crashing
//
// It's ok to use IsClosed() to check if you need to reopen the connection
// before calling a command.
func (c *Connection) IsClosed() bool {
	c.lock.Lock()
	defer c.lock.Unlock()

	return c.client == nil
}

// SendCommand is the low level call to send a command.
func (c *Connection) SendCommand(id uint, arguments ...interface{}) error {
	if c.client == nil {
		return fmt.Errorf("trying to send command on closed mpv client")
	}
	message := &commandRequest{
		Arguments: arguments,
		ID:        id,
	}
	data, err := json.Marshal(&message)
	if err != nil {
		return fmt.Errorf("can't encode command: %s", err)
	}
	_, err = c.client.Write(data)
	if err != nil {
		return fmt.Errorf("can't write command: %s", err)
	}
	_, err = c.client.Write([]byte("\n"))
	if err != nil {
		return fmt.Errorf("can't terminate command: %s", err)
	}
	return err
}

type commandRequest struct {
	Arguments []interface{} `json:"command"`
	ID        uint          `json:"request_id"`
}

type CommandResult struct {
	Status string      `json:"error"`
	Data   interface{} `json:"data"`
	ID     uint        `json:"request_id"`
}

func (c *Connection) checkResult(data []byte) {
	result := CommandResult{}

	if err := json.Unmarshal(data, &result); err != nil || result.Status == "" {
		return // skip malformed data
	}

	c.lock.Lock()
	request := c.waitingRequests[result.ID]
	c.lock.Unlock()

	if request != nil {
		request(&result)
	}
}

func (c *Connection) checkEvent(data []byte) {
	event := &Event{}
	err := json.Unmarshal(data, &event)
	if err != nil {
		return // skip malformed data
	}
	if event.Name == "" {
		return // not an event
	}
	c.lock.Lock()
	for listenerID := range c.eventListeners {
		listener := c.eventListeners[listenerID]
		listener(event)
	}
	c.lock.Unlock()
}

func (c *Connection) listen() {
	scanner := bufio.NewScanner(c.client)
	for scanner.Scan() {
		data := scanner.Bytes()
		c.checkEvent(data)
		c.checkResult(data)
	}

	c.Close()
}
