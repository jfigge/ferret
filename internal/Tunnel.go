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
	Tunnels = make(map[string]*Tunnel)
)

type HostName struct {
	Host string `yaml:"host" json:"host"`
}

type Tunnel struct {
	Name    string   `yaml:"name" json:"name"`
	Local   *Address `yaml:"local,omitempty" json:"local,omitempty"`
	Host    string   `yaml:"host" json:"host"`
	Forward *Address `yaml:"forward" json:"forward"`
}

var (
	connection  = atomic.Int32{}
	connections = atomic.Int32{}
	nextPort    = atomic.Int32{}
)

func (t *Tunnel) Open(sigTerm chan struct{}, listeningChan chan<- bool) {
	localListener, err := net.Listen("tcp", t.Local.address)
	if err != nil {
		fmt.Printf("  Error - Tunnel (%s) entrance (%s) cannot be created: %v\n", t.Name, t.Local.address, err)
		listeningChan <- false
		return
	}
	fmt.Printf("  Info  - Tunnel (%s) entrance opened at %s\n", t.Name, t.Local.address)
	listeningChan <- true

	// Wait indefinitely until the sigTerm channel closes
	go func() {
		<-sigTerm
		fmt.Printf(" Status - Tunnel (%s) stopped listening on %s\n", t.Name, t.Local.address)
		_ = localListener.Close()
	}()

	for {
		var localConn net.Conn
		localConn, err = localListener.Accept()
		if err != nil {
			var opErr *net.OpError
			if errors.As(err, &opErr) {
				if opErr.Op == "accept" && opErr.Err.Error() == "use of closed network connection" {
					// CLose quietly and we're likely shutting down
					return
				}
			}
			fmt.Printf("  Error - Tunnel (%s) listener accept failed: %v\n", t.Name, err)
			return
		}
		fmt.Printf("Connect tunnel: %+v\n", localConn)
		go t.forward(localConn)
	}
}

func (t *Tunnel) forward(localConn net.Conn) {
	connection.Add(1)
	id := connection.Load()

	//if verboseFlag > 0 {
	fmt.Printf(" Status - Tunnel (%s) id:%d conneting to forward server %s\n", t.Name, id, t.Forward.address)
	//}

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
		_, err1 := io.Copy(sshConn, localConn)
		connected1 = false
		connections.Add(-1)
		//if verboseFlag > 1 {
		fmt.Printf(" Status - Tunnel (%s) id:%d c:%d transmit tunnel closed\n", t.Name, id, connections.Load())
		//}
		//if err1 != nil && verboseFlag > 1 {
		fmt.Printf("   Info - Tunnel (%s) transmit encountered a closed tunnel: %v\n", t.Name, err1)
		//}
		if connected2 {
			go closer()
		}
	}()

	// Copy sshConn.Reader to localConn.Writer
	go func() {
		connections.Add(1)
		defer wg.Done()
		_, err2 := io.Copy(localConn, sshConn)
		connected2 = false
		connections.Add(-1)
		//if verboseFlag > 1 {
		fmt.Printf(" Status - id:%d c:%d receive tunnel closed\n", id, connections.Load())
		//}
		//if err2 != nil && verboseFlag > 1 {
		fmt.Printf("   Info - receive encountered a closed tunnel: %v\n", err2)
		//}
		if connected1 {
			go closer()
		}
	}()

	wg.Wait()
	cancel()
	//if verboseFlag == 1 {
	fmt.Printf(" Status - id:%d closing connection %s\n", id, localConn.RemoteAddr())
	//} else if verboseFlag > 1 {
	fmt.Printf(" Status - id:%d c:%d closing connection %s\n", id, connections.Load(), localConn.RemoteAddr())
	//}
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

	if t.Forward.IsBlank() {
		fmt.Printf("  Error - tunnel(%s) requires a forward address\n", t.Name)
		valid = false
	} else if !t.Forward.ValidateAddress("tunnel", t.Name, "forward address", true) {
		valid = false
	}

	if t.Local.IsBlank() && t.Forward.IsValid() {
		t.Local = NewAddress(fmt.Sprintf("0.0.0.0:%d", t.Forward.Port()))
	}
	if t.Local.IsBlank() {
		fmt.Printf("  Error - tunnel(%s) missing a local address that cannot be derived\n", t.Name)
	} else if !t.Local.ValidateAddress("tunnel", t.Name, "local address", true) {
		valid = false
	}

	t.Host = strings.TrimSpace(t.Host)
	if t.Host == "" {
		fmt.Printf("  Error - tunnel (%s) missing remote host\n", t.Name)
		valid = false
	} else if host, ok := Hosts[t.Host]; !ok {
		fmt.Printf("  Error - tunnel (%s) remote host (%s) not defined\n", t.Name, t.Host)
		valid = false
	} else {
		host.isHost = true
	}

	Tunnels[t.Name] = t
	return valid
}

func (t *Tunnel) autoClose(ctx context.Context, conn net.Conn, conn2 net.Conn, id int32) {
	status := "terminated"
	//if verboseFlag > 1 {
	fmt.Printf(" Status - Tunnel (%s) id:%d c:%d auto-closer initiated\n", t.Name, id, connections.Load())
	//}
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
	//if verboseFlag > 1 {
	fmt.Printf(" Status - Tunnel (%s) id:%d c:%d auto-closer %s\n", t.Name, id, connections.Load(), status)
	//}
}
