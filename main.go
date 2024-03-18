package main

import (
	"fmt"
	"os"
	"os/signal"
	"os/user"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"auto-ssh2/internal"
)

const (
	GoosLinux   = "linux"
	GoosDarwin  = "darwin"
	GoosWindows = "windows"
)

// Version information, populated by the build process
var (
	Version     string // this variable is defined in Makefile
	Commit      string // this variable is defined in Makefile
	Branch      string // this variable is defined in Makefile
	BuildNumber string //nolint:revive // this variable is defined in Makefile
)

// Default and operating variables
var (
	helpFlag    bool
	versionFlag bool
	verboseFlag int
	configFile  string
	username    string
	sigTerm     chan struct{}
	config      *internal.Configuration
)

func main() {
	sigTerm = make(chan struct{})
	defaultValues()
	parseCommandLine()
	LoadConfiguration()
	MonitorShutdown()
	StartTunnels()

	if verboseFlag > 0 {
		fmt.Printf(" Status - All tunnels closed.  Stopped\n")
	}
}

func defaultValues() {
	currentUser, err := user.Current()
	if err != nil {
		fmt.Printf("  Error - failed to lookup current user: %v\n", err)
		terminate(1)
	}
	username = currentUser.Username
	switch runtime.GOOS {
	case GoosLinux:
		configFile = fmt.Sprintf("/home/%s/.auto-ssh/config.yaml", currentUser.Username)
	case GoosDarwin:
		configFile = fmt.Sprintf("/Users/%s/.auto-ssh/config.yaml", currentUser.Username)
	case GoosWindows:
		configFile = fmt.Sprintf("C:\\Users\\%s\\.auto-ssh\\config.yaml", currentUser.Username)
	default:
		fmt.Printf("  Error - unsupported OS type: %s\n", runtime.GOOS)
		terminate(1)
	}
}

func parseCommandLine() {
	for index := 1; index < len(os.Args); index++ {
		switch os.Args[index] {
		case "-h", "--help":
			helpFlag = true
		case "-V", "--version":
			versionFlag = true
		case "-v", "--verbose":
			verboseFlag = 1
		case "-vv", "--very-verbose":
			verboseFlag = 2
		case "-vvv", "--very-very-verbose":
			verboseFlag = 3
		case "-c", "--config":
			index++
			configFile = parameter(index)

		default:
			if strings.HasPrefix(os.Args[index], "-") {
				fmt.Printf("  Error - unknown paramters (%s) at position %d\n", os.Args[index], index)
			} else {
				fmt.Printf("  Error - unexpected argument (%s) as position %d\n", os.Args[index], index)
			}
			helpFlag = true
		}
	}

	if helpFlag {
		help()
	}
	if versionFlag {
		version()
	}
}

func parameter(index int) string {
	if index < len(os.Args) && !strings.HasPrefix(os.Args[index], "-") {
		return os.Args[index]
	}
	fmt.Printf("  Error - paramreter %s requires a value\n", os.Args[index-1])
	terminate(1)
	return ""
}

func LoadConfiguration() {
	config = config.Load(configFile)
	if config == nil {
		terminate(1)
	}

	if !config.Validate(username) {
		terminate(1)
	}
}

func MonitorShutdown() {
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-shutdown
		fmt.Printf("%s terminated\n", os.Args[0])
		terminate(1)
	}()
}

func StartTunnels() {
	wg := sync.WaitGroup{}
	for _, tunnel := range internal.Tunnels {
		wg.Add(1)
		go func(t *internal.Tunnel) {
			defer func() {
				wg.Done()
				fmt.Printf("Closing tunnel: %s", t.Name)
			}()
			listenerChan := make(chan bool)
			go monitorForFailureToConnect(listenerChan)
			t.Open(sigTerm, listenerChan)
		}(tunnel)
	}
	wg.Wait()
}

func monitorForFailureToConnect(listener <-chan bool) {
	// listen to the successful starting of a channel, and call terminate
	// if any of them fail to start up.
	if !<-listener {
		terminate(1)
	}
}

func help() {
	fmt.Printf("Automatic tunneling on demand\n")
	fmt.Printf("Usage:\n")
	fmt.Printf("  -h, --help     Display this message.\n")
	fmt.Printf("  -c, --config   Specify the tunnel configuration file\n")
	fmt.Printf("  -v, --verbose  Verbose mode.  Prints progress debug messages.\n")
	fmt.Printf("  -V, --version  Display version information.\n")
	terminate(0)
}

func version() {
	if verboseFlag == 0 {
		fmt.Printf(
			"%s verison %s %s/%s, build %s, commit %s\n",
			os.Args[0], Version, runtime.GOOS, runtime.GOARCH, BuildNumber, Commit,
		)
	} else {
		fmt.Printf(
			"%s verison %s %s/%s, build %s, commit %s, branch %s\n",
			os.Args[0], Version, runtime.GOOS, runtime.GOARCH, BuildNumber, Commit, Branch,
		)
	}
	terminate(0)
}

func terminate(code int) {
	go func() {
		defer func() {
			_ = recover()
		}()
		close(sigTerm)
	}()
	<-time.NewTimer(time.Second).C
	fmt.Printf("Terminated\n")
	os.Exit(code)

}
