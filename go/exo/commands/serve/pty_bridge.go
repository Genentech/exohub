package serve

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty/v2"
	"github.com/gorilla/websocket"
)

const (
	// How often to send WebSocket pings.
	pingInterval = 30 * time.Second
	// How long to wait for a pong before considering the connection dead.
	pongTimeout = 10 * time.Second
)

// controlMessage is a JSON message sent over text WebSocket frames.
type controlMessage struct {
	Type     string `json:"type"`
	Rows     uint16 `json:"rows,omitempty"`
	Cols     uint16 `json:"cols,omitempty"`
	URL      string `json:"url,omitempty"`
	Code     string `json:"code,omitempty"`
	Username string `json:"username,omitempty"`
	Error    string `json:"error,omitempty"`
}

// PTYBridge manages a PTY-spawned exo browse process and bridges it to a WebSocket.
type PTYBridge struct {
	conn      *websocket.Conn
	ptmx      *os.File
	cmd       *exec.Cmd
	connID    string
	done      chan struct{}
	closeOnce sync.Once
}

// NewPTYBridge creates a bridge that will spawn exo atlas connected to the given WebSocket.
// connID is a unique per-connection identifier used for temp file isolation.
// initialDoc, if non-empty, opens that artifact directly in the pager.
func NewPTYBridge(conn *websocket.Conn, catalogURL, tokenJSON, connID, initialDoc, theme, themeMode string) (*PTYBridge, error) {
	exePath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("failed to find exo binary: %w", err)
	}

	// Per-connection search dir for search params isolation.
	// UI elements use the default shared config dir (not overridden).
	searchDir := connectionSearchDir(connID)
	if err := os.MkdirAll(searchDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create connection search dir: %w", err)
	}

	args := []string{"atlas", "--url", catalogURL}
	if initialDoc != "" {
		args = append(args, initialDoc)
	}
	cmd := exec.Command(exePath, args...)
	navDir := filepath.Join(os.TempDir(), "exo-serve", connID)
	env := append(os.Environ(),
		"EXO_TOKEN="+tokenJSON,
		"EXO_BROWSE_SEARCH_DIR="+searchDir,
		"EXO_NAV_DIR="+navDir,
		"EXO_SERVE_MODE=1",
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
	)
	if theme != "" {
		env = append(env, "EXO_THEME="+theme)
	}
	if themeMode != "" {
		env = append(env, "EXO_THEME_MODE="+themeMode)
	}
	cmd.Env = env

	return &PTYBridge{
		conn:   conn,
		cmd:    cmd,
		connID: connID,
		done:   make(chan struct{}),
	}, nil
}

// Start allocates the PTY, starts the process, and begins bridging.
// It blocks until the process exits or the WebSocket closes.
func (b *PTYBridge) Start(initialRows, initialCols uint16) error {
	size := &pty.Winsize{Rows: initialRows, Cols: initialCols}
	if size.Rows == 0 {
		size.Rows = 24
	}
	if size.Cols == 0 {
		size.Cols = 80
	}

	ptmx, err := pty.StartWithSize(b.cmd, size)
	if err != nil {
		return fmt.Errorf("failed to start PTY: %w", err)
	}
	b.ptmx = ptmx

	// Set up ping/pong to detect dead connections.
	// pongTimeout is the deadline — if no pong arrives in time, the next read fails.
	b.conn.SetReadDeadline(time.Now().Add(pingInterval + pongTimeout))
	b.conn.SetPongHandler(func(string) error {
		b.conn.SetReadDeadline(time.Now().Add(pingInterval + pongTimeout))
		return nil
	})

	// PTY → WebSocket (terminal output)
	go b.ptyToWS()

	// WebSocket → PTY (keyboard input + control messages)
	go b.wsToPTY()

	// Ping loop to detect dead connections
	go b.pingLoop()

	// Wait for the process to exit
	err = b.cmd.Wait()
	b.Close()
	return err
}

// Resize changes the PTY window size.
func (b *PTYBridge) Resize(rows, cols uint16) error {
	if b.ptmx == nil {
		return nil
	}
	return pty.Setsize(b.ptmx, &pty.Winsize{Rows: rows, Cols: cols})
}

// Close terminates the bridge, kills the process, and cleans up resources.
func (b *PTYBridge) Close() {
	b.closeOnce.Do(func() {
		close(b.done)
		if b.cmd.Process != nil {
			_ = b.cmd.Process.Kill()
		}
		if b.ptmx != nil {
			_ = b.ptmx.Close()
		}
		_ = b.conn.Close()
		CleanupConnectionFiles(b.connID)
	})
}

// oscPrefix is the OSC 777 escape sequence prefix for custom commands.
// Format: ESC ] 7 7 7 ; <command> [; <args>...] BEL
var oscPrefix = []byte("\x1b]777;")



// ptyToWS reads from the PTY, intercepts OSC 777 download sequences,
// and writes terminal output as binary frames to the WebSocket.
func (b *PTYBridge) ptyToWS() {
	defer b.Close()
	buf := make([]byte, 4096)
	var pending []byte // accumulates partial OSC sequences across reads
	for {
		n, err := b.ptmx.Read(buf)
		if err != nil {
			if err != io.EOF {
				select {
				case <-b.done:
				default:
					log.Printf("PTY read error: %v", err)
				}
			}
			return
		}

		data := buf[:n]
		if len(pending) > 0 {
			data = append(pending, data...)
			pending = nil
		}

		for len(data) > 0 {
			idx := bytes.Index(data, oscPrefix)
			if idx == -1 {
				// Check if data ends with a partial prefix
				for i := 1; i < len(oscPrefix) && i <= len(data); i++ {
					if bytes.HasSuffix(data, oscPrefix[:i]) {
						pending = make([]byte, i)
						copy(pending, data[len(data)-i:])
						data = data[:len(data)-i]
						break
					}
				}
				// Send remaining terminal output
				if len(data) > 0 {
					if err := b.conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
						return
					}
				}
				break
			}

			// Send everything before the OSC sequence
			if idx > 0 {
				if err := b.conn.WriteMessage(websocket.BinaryMessage, data[:idx]); err != nil {
					return
				}
			}

			// Find the BEL terminator
			rest := data[idx+len(oscPrefix):]
			belIdx := bytes.IndexByte(rest, '\x07')
			if belIdx == -1 {
				// Incomplete OSC — buffer it for next read
				pending = make([]byte, len(data)-idx)
				copy(pending, data[idx:])
				break
			}

			// Dispatch based on command
			payload := string(rest[:belIdx])
			b.handleOSC(payload)

			data = rest[belIdx+1:]
		}
	}
}

// wsToPTY reads from the WebSocket and dispatches to the PTY or handles control messages.
func (b *PTYBridge) wsToPTY() {
	defer b.Close()
	for {
		msgType, data, err := b.conn.ReadMessage()
		if err != nil {
			return
		}

		switch msgType {
		case websocket.BinaryMessage:
			// Keyboard input → PTY
			if _, err := b.ptmx.Write(data); err != nil {
				return
			}

		case websocket.TextMessage:
			// Control message (JSON)
			var msg controlMessage
			if err := json.Unmarshal(data, &msg); err != nil {
				continue
			}
			switch msg.Type {
			case "resize":
				if err := b.Resize(msg.Rows, msg.Cols); err != nil {
					log.Printf("Resize error: %v", err)
				}
			case "open-doc":
				// Browser forward button — write doc ID to nav file and signal TUI
				if msg.Code != "" && b.cmd.Process != nil {
					navFile := filepath.Join(os.TempDir(), "exo-serve", b.connID, "nav")
					if err := os.WriteFile(navFile, []byte(msg.Code), 0600); err == nil {
						_ = b.cmd.Process.Signal(syscall.SIGUSR1)
					}
				}
			}
		}
	}
}

// pingLoop sends WebSocket pings at regular intervals.
// If the client doesn't respond with a pong, the read deadline expires
// and wsToPTY exits, triggering Close().
func (b *PTYBridge) pingLoop() {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := b.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(pongTimeout)); err != nil {
				b.Close()
				return
			}
		case <-b.done:
			return
		}
	}
}

// handleOSC dispatches a custom OSC 777 command to the browser.
func (b *PTYBridge) handleOSC(payload string) {
	parts := strings.SplitN(payload, ";", 3)
	if len(parts) == 0 {
		return
	}
	switch parts[0] {
	case "download":
		if len(parts) == 3 {
			_ = sendControlMessage(b.conn, controlMessage{
				Type: "download",
				URL:  parts[2],
				Code: parts[1], // filename
			})
		}
	case "toggle-downloads":
		_ = sendControlMessage(b.conn, controlMessage{
			Type: "toggle-downloads",
		})
	case "toggle-select":
		_ = sendControlMessage(b.conn, controlMessage{
			Type: "toggle-select",
		})
	case "doc-link":
		if len(parts) >= 2 {
			_ = sendControlMessage(b.conn, controlMessage{
				Type: "doc-link",
				Code: parts[1],
			})
		}
	case "doc-link-hide":
		_ = sendControlMessage(b.conn, controlMessage{
			Type: "doc-link-hide",
		})
	case "clipboard":
		if len(parts) >= 2 {
			_ = sendControlMessage(b.conn, controlMessage{
				Type: "clipboard",
				Code: parts[1],
			})
		}
	case "copy-content":
		if len(parts) >= 2 {
			_ = sendControlMessage(b.conn, controlMessage{
				Type: "copy-content",
				Code: parts[1], // base64-encoded content
			})
		}
	case "search-params":
		log.Printf("OSC search-params: parts=%v", parts)
		if len(parts) >= 2 {
			_ = sendControlMessage(b.conn, controlMessage{
				Type: "search-params",
				Code: parts[1],
			})
		}
	}
}

// sendControlMessage sends a JSON control message over the WebSocket.
func sendControlMessage(conn *websocket.Conn, msg controlMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, data)
}
