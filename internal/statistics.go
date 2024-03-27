package internal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

var (
	tunnelStats []*TunnelStats
	zeros       = string([]byte{
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	})
	p        = message.NewPrinter(language.English)
	interval = time.Second * 5
)

type TunnelStats struct {
	Name        string `json:"name"`
	Connections int    `json:"connections"`
	Received    int64  `json:"received"`
	Transmitted int64  `json:"transmitted"`
	updateChan  chan struct{}
}

type StatsManager struct {
	statsPort     int
	statsAddress  string
	updateChan    chan struct{}
	connections   []net.Conn
	statsListener net.Listener
	lock          sync.Mutex
	updated       bool
	lastUpdate    []byte
}

func NewStats(statsPort int) *StatsManager {
	return &StatsManager{
		statsPort: statsPort,
	}
}

func (s *StatsManager) UpdateChannel() chan struct{} {
	return s.updateChan
}

func (s *StatsManager) StartStatsTunnel(ctx context.Context) bool {
	if s.statsPort != -1 {
		var err error
		s.statsAddress = fmt.Sprintf("127.0.0.1:%d", s.statsPort)
		s.updateChan = make(chan struct{})
		s.statsListener, err = net.Listen("tcp", s.statsAddress)
		if err != nil {
			s.receiveStats(ctx)
			return false
		}
		go s.transmitStats(ctx)
	}
	return true
}

func (s *StatsManager) transmitStats(ctx context.Context) {
	fmt.Printf("  Info  - ferret stats listening on %d\n", s.statsPort)
	go s.statsBroadcaster(ctx)

	for {
		conn, err := s.statsListener.Accept()
		if err != nil {
			var opErr *net.OpError
			if errors.As(err, &opErr) {
				if opErr.Op == "accept" && opErr.Err.Error() == "use of closed network connection" {
					// CLose quietly and we're likely shutting down
					return
				}
			}
			fmt.Printf("  Error - ferrent stats listener accept failed: %v\n", err)
			return
		}
		fmt.Printf("  Info  - Connected stats client\n")
		s.addConnection(conn)
	}
}

func (s *StatsManager) addConnection(conn net.Conn) {
	s.lock.Lock()
	defer s.lock.Unlock()
	if len(s.lastUpdate) > 0 {
		_, _ = conn.Write(s.lastUpdate)
	}
	s.connections = append(s.connections, conn)
}

func (s *StatsManager) closeAllConnections() {
	s.lock.Lock()
	defer s.lock.Unlock()
	for _, conn := range s.connections {
		_ = conn.Close()
	}
	_ = s.statsListener.Close()
}

func (s *StatsManager) statsBroadcaster(ctx context.Context) {
	lastBroadcast := time.Now().Add(-interval)
	for {
		select {
		case <-ctx.Done():
			fmt.Printf("  Info  - ferret stats closed\n")
			s.closeAllConnections()
			return
		case <-s.updateChan:
			if !s.updated {
				s.updated = true
				if len(s.connections) > 0 {
					go func() {
						// Don't repeat send data within 5 seconds, but always wait at least 1 second
						// for any pending data to be sent.

						diff := time.Until(lastBroadcast.Add(interval))
						if diff > 0 {
							<-time.NewTimer(diff).C
						} else {
							<-time.NewTimer(time.Second).C
						}
						bs, err := json.Marshal(tunnelStats)
						lastBroadcast = time.Now()
						if err == nil {
							s.writeUpdate(bs)
						}
						s.updated = false
					}()
				}
			}
		}
	}
}

func (s *StatsManager) writeUpdate(update []byte) {
	if !s.lock.TryLock() {
		return
	}
	defer s.lock.Unlock()

	x := 256 - (len(update) % 256)
	s.lastUpdate = append(update, zeros[256-x:]...)
	var alive []net.Conn
	for _, conn := range s.connections {
		if _, err := conn.Write(s.lastUpdate); err != nil {
			fmt.Printf("  Info  - Disconnected stats client\n")
		} else {
			alive = append(alive, conn)
		}
	}

	if len(alive) != len(s.connections) {
		s.connections = alive
	}
}

func (s *StatsManager) receiveStats(ctx context.Context) {
	fmt.Printf("ferret already running\n")
	conn, err := net.DialTimeout("tcp", s.statsAddress, time.Second*5)
	if err != nil {
		return
	}

	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	var n int
	bs := make([]byte, 256)
	str := ""
	for {
		n, err = conn.Read(bs)
		if err != nil {
			fmt.Printf("  Info  - ferret terminated or cannot be reached\n")
			_ = conn.Close()
			return
		} else if n > 0 {
			str = str + string(bs)
		}
		if bs[n-1] == 0 {
			index := strings.IndexByte(str, byte(0))
			str = str[:index]
			var ts []*TunnelStats
			if err = json.Unmarshal([]byte(str), &ts); err == nil {
				fmt.Printf("%-40s %-15s %-15s %-6s\n", "Name", "Rcvd", "Sent", "Cnct")
				for _, t := range ts {
					_, _ = p.Printf("%-40s %-15d %-15d %-6d\n", t.Name, t.Received, t.Transmitted, t.Connections)
				}
			}
			str = ""
		}
	}
}
