package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"os/user"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"us.figge.ferret/internal"
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
	verboseFlag bool
	configFile  string
	username    string
	statsPort   int
	config      *internal.Configuration
	cancel      func()
)

func main() {
	var ctx context.Context
	ctx, cancel = context.WithCancel(context.Background())
	defaultValues()
	parseCommandLine()
	loadConfiguration()
	monitorShutdown()
	stats := internal.NewStats(statsPort)
	if ok := stats.StartStatsTunnel(ctx); ok {
		startTunnels(ctx, stats)
	}
	if verboseFlag {
		fmt.Printf(" Status - All tunnels closed.  Stopped\n")
	}
}

func defaultValues() {
	statsPort = 2663
	currentUser, err := user.Current()
	if err != nil {
		fmt.Printf("  Error - failed to lookup current user: %v\n", err)
		terminate(1)
	}
	username = currentUser.Username
	switch runtime.GOOS {
	case GoosLinux:
		configFile = fmt.Sprintf("/home/%s/.ferret/config.yaml", currentUser.Username)
	case GoosDarwin:
		configFile = fmt.Sprintf("/Users/%s/.ferret/config.yaml", currentUser.Username)
	case GoosWindows:
		configFile = fmt.Sprintf("C:\\Users\\%s\\.ferret\\config.yaml", currentUser.Username)
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
			verboseFlag = true
		case "-p", "--stats-port":
			index++
			statsPort = parameterInt(index)
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

func parameterInt(index int) int {
	value := parameter(index)
	i, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		fmt.Printf("  Error - paramreter %s expected an int value\n", os.Args[index-1])
		terminate(1)
	}
	return int(i)
}

func loadConfiguration() {
	config = config.Load(configFile, verboseFlag)
	if config == nil {
		terminate(1)
	}
	if verboseFlag {
		fmt.Printf("  Info  - Using config file: %s\n", configFile)
	}

	if !config.Validate(username) {
		terminate(1)
	}
}

func monitorShutdown() {
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-shutdown
		cancel()
		fmt.Printf("%s terminated\n", os.Args[0])
		terminate(1)
	}()
}

func startTunnels(ctx context.Context, stats *internal.StatsManager) {
	wg := sync.WaitGroup{}
	for _, tunnel := range internal.Tunnels {
		wg.Add(1)
		tunnel.Init(stats.UpdateChannel())
		stats.AddTunnelStats(tunnel.Stats())
		go func(t *internal.Tunnel) {
			defer func() {
				wg.Done()
			}()
			listenerChan := make(chan bool)
			go monitorForFailureToConnect(listenerChan)
			t.Open(ctx, listenerChan)
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
	fmt.Printf("  -h, --help        Display this message.\n")
	fmt.Printf("  -c, --config      Specify the tunnel configuration file\n")
	fmt.Printf("  -p, --stats-port  Ferret stats port.  Default is 2663\n")
	fmt.Printf("  -v, --verbose     Verbose mode.  Prints progress debug messages.\n")
	fmt.Printf("  -V, --version     Display version information.\n")
	terminate(0)
}

func version() {
	if verboseFlag {
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
	}()
	<-time.NewTimer(time.Second).C
	fmt.Printf("Terminated\n")
	os.Exit(code)

}
