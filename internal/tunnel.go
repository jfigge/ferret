package internal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	Tunnels         = make(map[string]*Tunnel)
	errInvalidWrite = errors.New("invalid write result")
)

type HostName struct {
	Host string `yaml:"host" json:"host"`
}

type Tunnel struct {
	Name    string   `yaml:"name" json:"name"`
	Local   *Address `yaml:"local,omitempty" json:"local,omitempty"`
	Host    string   `yaml:"host" json:"host"`
	Forward *Address `yaml:"forward" json:"forward"`
	stats   *TunnelStats
}

var (
	connection  = atomic.Int32{}
	connections = atomic.Int32{}
)

func (t *Tunnel) Open(ctx context.Context, listeningChan chan<- bool, updateChan chan struct{}) {
	t.stats = &TunnelStats{Name: t.Name, updateChan: updateChan}
	tunnelStats = append(tunnelStats, t.stats)
	localListener, err := net.Listen("tcp", t.Local.address)
	if err != nil {
		fmt.Printf("  Error - tunnel (%s) entrance (%s) cannot be created: %v\n", t.Name, t.Local.address, err)
		listeningChan <- false
		return
	}
	fmt.Printf("  Info  - tunnel (%s) entrance opened at %s\n", t.Name, t.Local.address)
	listeningChan <- true

	// Wait indefinitely until the sigTerm channel closes
	go func() {
		<-ctx.Done()
		fmt.Printf("  Info  - tunnel (%s) stopped listening on %s\n", t.Name, t.Local.address)
		_ = localListener.Close()
	}()

	for {
		var localConn net.Conn
		localConn, err = localListener.Accept()
		updateChan <- struct{}{}
		if err != nil {
			var opErr *net.OpError
			if errors.As(err, &opErr) {
				if opErr.Op == "accept" && opErr.Err.Error() == "use of closed network connection" {
					// CLose quietly and we're likely shutting down
					return
				}
			}
			fmt.Printf("  Error - tunnel (%s) listener accept failed: %v\n", t.Name, err)
			return
		}
		fmt.Printf("  Info  - Connected tunnel: %v\n", t.Name)
		go t.forward(localConn)
	}
}

func (t *Tunnel) forward(localConn net.Conn) {
	t.stats.Connections++
	connection.Add(1)
	id := connection.Load()

	if verboseFlag {
		fmt.Printf("  Info  - tunnel (%s) id:%d conneting to forward server %s\n", t.Name, id, t.Forward.address)
	}

	host := Hosts[t.Host]
	if !host.Open() {
		// TODO Failed to connect
		return
	}
	sshConn, ok := host.Dial(t.Forward.address)
	if !ok {
		// TODO failed to connect
		return
	}

	wg := sync.WaitGroup{}
	wg.Add(2)
	ctx, cancel := context.WithCancel(context.Background())
	closer := func() {
		t.autoClose(ctx, sshConn, localConn, id)
	}

	connected1 := true
	connected2 := true
	// Copy localConn.Reader to sshConn.Writer
	go func() {
		connections.Add(1)
		defer wg.Done()
		err1 := t.copy(sshConn, localConn, true)
		connected1 = false
		connections.Add(-1)
		if verboseFlag {
			fmt.Printf("  Info  - tunnel (%s) id:%d c:%d transmit tunnel closed\n", t.Name, id, connections.Load())
		}
		if err1 != nil && verboseFlag {
			fmt.Printf("  Error - tunnel (%s) transmit encountered a closed tunnel: %v\n", t.Name, err1)
		}
		if connected2 {
			go closer()
		}
	}()

	// Copy sshConn.Reader to localConn.Writer
	go func() {
		connections.Add(1)
		defer wg.Done()
		err2 := t.copy(localConn, sshConn, false)
		connected2 = false
		connections.Add(-1)
		if verboseFlag {
			fmt.Printf("  Info  - tunnel (%s) id:%d c:%d receive tunnel closed\n", t.Name, id, connections.Load())
		}
		if err2 != nil && verboseFlag {
			fmt.Printf("  Info - tunnel (%s) receive encountered a closed tunnel: %v\n", t.Name, err2)
		}
		if connected1 {
			go closer()
		}
	}()

	wg.Wait()
	cancel()
	if verboseFlag {
		fmt.Printf("  Info  - id:%d c:%d closing connection %s\n", id, connections.Load(), localConn.RemoteAddr())
	}
}

func (t *Tunnel) Validate() bool {
	valid := true

	t.Name = strings.TrimSpace(t.Name)
	if t.Name == "" {
		fmt.Printf("  Error - tunnel name cannot be blank\n")
		valid = false
	}
	if _, ok := Tunnels[t.Name]; ok {
		fmt.Printf("  Error - tunnel name (%s) redfined\n", t.Name)
		valid = false
	}

	if t.Forward == nil || t.Forward.IsBlank() {
		fmt.Printf("  Error - tunnel (%s) requires a forward address\n", t.Name)
		valid = false
	} else if !t.Forward.Validate("tunnel", t.Name, "forward address", true, false) {
		valid = false
	}

	if (t.Local == nil || t.Local.IsBlank()) && t.Forward != nil && t.Forward.IsValid() {
		fmt.Printf("  Warn  - tunnel (%s) Local entrance undefined. Defaulting to 127.0.0.1:%d\n", t.Name, t.Forward.Port())
		t.Local = NewAddress(fmt.Sprintf("127.0.0.1:%d", t.Forward.Port()))
	}
	if t.Local == nil || t.Local.IsBlank() {
		fmt.Printf("  Error - tunnel (%s) missing a local address that cannot be derived\n", t.Name)
	} else if !t.Local.Validate("tunnel", t.Name, "local address", true, false) {
		valid = false
	}

	t.Host = strings.TrimSpace(t.Host)
	if t.Host == "" {
		fmt.Printf("  Error - tunnel (%s) missing remote host\n", t.Name)
		valid = false
	} else if host, ok := Hosts[t.Host]; !ok {
		fmt.Printf("  Error - tunnel (%s) remote host (%s) undefined\n", t.Name, t.Host)
		valid = false
	} else {
		host.isHost = true
	}

	if verboseFlag && valid {
		fmt.Printf("  Info  - tunnel (%s) validated\n", t.Name)
	}
	Tunnels[t.Name] = t
	return valid
}

func (t *Tunnel) autoClose(ctx context.Context, conn net.Conn, conn2 net.Conn, id int32) {
	status := "terminated"
	if verboseFlag {
		fmt.Printf("  Info  - tunnel (%s) id:%d c:%d auto-closer initiated\n", t.Name, id, connections.Load())
	}
	timer := time.NewTimer(30 * time.Second)
	select {
	case <-timer.C:
		status = "triggered"
	case <-ctx.Done():
	}
	if conn != nil {
		_ = conn.Close()
	}
	if conn2 != nil {
		_ = conn2.Close()
	}
	if verboseFlag {
		fmt.Printf("  Info  - tunnel (%s) id:%d c:%d auto-closer %s\n", t.Name, id, connections.Load(), status)
	}
}

func (t *Tunnel) copy(dst io.Writer, src io.Reader, read bool) (err error) {
	buf := make([]byte, 32*1024)
	for {
		nr, er := src.Read(buf)
		if nr > 0 {
			nw, ew := dst.Write(buf[0:nr])
			if nw < 0 || nr < nw {
				nw = 0
				if ew == nil {
					ew = errInvalidWrite
				}
			}
			if t.stats != nil {
				if read {
					t.stats.Received += int64(nw)
					t.stats.updateChan <- struct{}{}
				} else {
					t.stats.Transmitted += int64(nw)
					t.stats.updateChan <- struct{}{}
				}
			}
			if ew != nil {
				err = ew
				break
			}
			if nr != nw {
				err = io.ErrShortWrite
				break
			}
		}
		if er != nil {
			if er != io.EOF {
				err = er
			}
			break
		}
	}
	return err
}
