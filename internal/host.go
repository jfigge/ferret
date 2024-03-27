package internal

import (
	"fmt"
	"net"
	"os"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

var (
	Hosts       = make(map[string]*Host)
	identityMap = make(map[string]ssh.Signer)
	hostKeysMap = map[string]ssh.HostKeyCallback{
		"": func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			return nil
		},
	}
)

type Host struct {
	Name       string   `yaml:"name" json:"name"`
	Address    *Address `yaml:"address" json:"address"`
	Username   string   `yaml:"username" json:"username"`
	Identity   string   `yaml:"identity" json:"identity"`
	Passphrase string   `yaml:"passphrase,omitempty" json:"passphrase,omitempty"`
	KnownHosts string   `yaml:"known_hosts,omitempty" json:"known_hosts,omitempty"`
	JumpHost   string   `yaml:"jump_host,omitempty" json:"jump_host,omitempty"`
	isHost     bool
	isJumpHost bool
	lock       sync.Mutex
	client     *ssh.Client
	config     *ssh.ClientConfig
}

func (h *Host) Open() bool {
	h.lock.Lock()
	defer h.lock.Unlock()

	if h.client == nil {
		var err error
		h.client, err = ssh.Dial("tcp", h.Address.address, h.config)
		if err != nil {
			fmt.Printf("  Error - failed to connect to remote address: %v\n", err)
			return false
		}
	}
	return true
}

func (h *Host) Dial(address string) (net.Conn, bool) {
	h.lock.Lock()
	defer h.lock.Unlock()
	conn, err := h.client.Dial("tcp", address)
	// TODO Redial (Open) as necessary
	if err != nil {
		fmt.Printf("  Error - Host (%s) failed to call remote address: %v\n", h.Name, err)
		return nil, false
	}
	return conn, true
}

func (h *Host) Validate(defaultUsername string) bool {
	valid := true

	h.Name = strings.TrimSpace(h.Name)
	if h.Name == "" {
		fmt.Printf("  Error - host name cannot be blank\n")
		valid = false
	}
	if _, ok := Hosts[h.Name]; ok {
		fmt.Printf("  Error - host name (%s) redfined\n", h.Name)
		valid = false
	}

	h.Username = strings.TrimSpace(h.Username)
	if strings.TrimSpace(h.Username) == "" && verboseFlag {
		fmt.Printf("  Info  - host (%s) will use default username: %s\n", h.Name, defaultUsername)
		h.Username = defaultUsername
	}

	h.KnownHosts = strings.TrimSpace(h.KnownHosts)
	if _, ok := hostKeysMap[h.KnownHosts]; !ok {
		if fi, err := os.Stat(h.KnownHosts); os.IsNotExist(err) {
			fmt.Printf("  Error - host (%s) known_hosts file (%s) cannot be read: file not found\n", h.Name, h.KnownHosts)
			valid = false
		} else if fi.IsDir() {
			fmt.Printf("  Error - host (%s) known_hosts file (%s) cannot be read: file is a directory\n", h.Name, h.KnownHosts)
			valid = false
		} else {
			var hostKeyCallback ssh.HostKeyCallback
			if hostKeyCallback, err = knownhosts.New(h.KnownHosts); os.IsPermission(err) {
				fmt.Printf("  Error - host (%s) known_hosts file (%s) cannot be read: permission denied\n", h.Name, h.KnownHosts)
				valid = false
			} else if err != nil {
				fmt.Printf("  Error - host (%s) known_hosts file (%s) cannot be read: %v\n", h.Name, h.KnownHosts, err)
				valid = false
			} else {
				hostKeysMap[h.KnownHosts] = hostKeyCallback
			}
		}
	}

	h.Identity = strings.TrimSpace(h.Identity)
	if h.Identity == "" {
		fmt.Printf("  Error - host (%s) missing identity file\n", h.Name)
		valid = false
	}
	if _, ok := identityMap[h.Identity]; !ok {
		if fi, err := os.Stat(h.Identity); os.IsNotExist(err) {
			fmt.Printf("  Error - host (%s) identity file (%s) cannot be read: file not found\n", h.Name, h.Identity)
			valid = false
		} else if fi.IsDir() {
			fmt.Printf("  Error - host (%s) identity file (%s) cannot be read: file is a directory\n", h.Name, h.Identity)
			valid = false
		} else {
			var key []byte
			key, err = os.ReadFile(h.Identity)
			if os.IsPermission(err) {
				fmt.Printf("  Error - host (%s) identity file (%s) cannot be read: permission denied\n", h.Name, h.Identity)
				valid = false
			} else if err != nil {
				fmt.Printf("  Error - host (%s) identity file (%s) cannot be read: %v\n", h.Name, h.Identity, err)
				valid = false
			} else {
				var signer ssh.Signer
				h.Passphrase = strings.TrimSpace(h.Passphrase)
				if h.Passphrase != "" {
					signer, err = ssh.ParsePrivateKeyWithPassphrase(key, []byte(h.Passphrase))
				} else {
					signer, err = ssh.ParsePrivateKey(key)
				}
				if err != nil {
					fmt.Printf("  Error - host (%s) identity file (%s) cannot be decode: %v\n", h.Name, h.Identity, err)
					valid = false
				} else {
					identityMap[h.Identity] = signer
				}
			}
		}
	}

	if h.Address == nil || h.Address.IsBlank() {
		fmt.Printf("  Error - host (%s) requires an address\n", h.Name)
		valid = false
	} else if !h.Address.Validate("host", h.Name, "address", h.JumpHost != "", true) {
		valid = false
	}

	if h.JumpHost != "" {
		if h.JumpHost == h.Name {
			fmt.Printf("  Error - host (%s) jump_host cannot reference itself\n", h.Name)
			valid = false
		} else {
			h.KnownHosts = ""
		}
	}
	h.config = &ssh.ClientConfig{
		User: h.Username,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(identityMap[h.Identity]),
		},
		HostKeyCallback: hostKeysMap[h.KnownHosts],
	}

	if verboseFlag && valid {
		fmt.Printf("  Info - host (%s) validated\n", h.Name)
	}
	Hosts[h.Name] = h
	return valid
}

func (h *Host) IsJumpHost() bool {
	return h.isJumpHost
}

func (h *Host) IsHost() bool {
	return h.isHost
}

func freePort() (net.Listener, int32, bool) {
	if address, err := net.ResolveTCPAddr("tcp", "localhost:0"); err == nil {
		var listener net.Listener
		listener, err = net.ListenTCP("tcp", address)
		if err == nil {
			return listener, int32(listener.Addr().(*net.TCPAddr).Port), true
		}
		_ = listener.Close()
	}
	return nil, -1, false
}

func validateJumpHosts() bool {
	valid := true
	var freePortListeners []net.Listener
	defer func() {
		for _, listener := range freePortListeners {
			_ = listener.Close()
		}
	}()
	for _, h := range Hosts {
		if h.JumpHost != "" && h.isHost {
			if jumpHost, ok := Hosts[h.JumpHost]; !ok {
				fmt.Printf("  Error - host (%s) jump_host (%s) is not defined\n", h.Name, h.JumpHost)
				valid = false
			} else if jumpHost.JumpHost != "" {
				fmt.Printf("  Error - host (%s) requires multi-host jumps and is not supported", h.Name)
				valid = false
			} else {
				listener, port, found := freePort()
				if !found {
					valid = false
					break
				} else {
					jumpTunnel := &Tunnel{
						Name:    fmt.Sprintf("%s jumphost", jumpHost.Name),
						Local:   NewAddress(fmt.Sprintf("127.0.0.1:%d", port)),
						Host:    h.JumpHost,
						Forward: h.Address,
					}
					jumpTunnel.Validate()
					h.Address = jumpTunnel.Local
					freePortListeners = append(freePortListeners, listener)
				}
			}
		}
	}
	return valid
}
