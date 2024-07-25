package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	livestreamconfig "vu/ase/streamserver/src/config"
	"vu/ase/streamserver/src/httpserver"
	"vu/ase/streamserver/src/state"

	rtc "github.com/VU-ASE/pkg-Rtc/src"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func run(serverAddress string) error {
	state, err := state.NewServerState()
	if err != nil {
		return fmt.Errorf("Could not create server state: %v", err)
	}

	// Create a map to hold all active connections
	connectedPeers := rtc.NewRTCMap()
	// Clean up connections when the server is shut down
	defer state.Destroy()

	// Global server state
	state.ConnectedPeers = connectedPeers

	// Strip http:// or https:// from the server address
	addr := strings.ReplaceAll(serverAddress, "http://", "")
	addr = strings.ReplaceAll(addr, "https://", "")

	// Quit on SIGINT
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go onAbort(sig, state)

	return httpserver.Serve(addr, state)
}

func onAbort(sig chan os.Signal, state *state.ServerState) {
	<-sig
	log.Info().Msg("Received SIGINT. Gracefully shutting down server...")

	go func() {
		<-sig
		log.Warn().Msg("Received SIGINT again. Forcing shutdown...")
		os.Exit(1)
	}()

	state.Destroy()
	os.Exit(0)
}

// Configures log level and output
func setupLogging(debug bool, outputPath string) {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnixMs

	// Set up custom caller prefix
	zerolog.CallerMarshalFunc = func(pc uintptr, file string, line int) string {
		path := strings.Split(file, "/")
		// only take the last three elements of the path
		filepath := strings.Join(path[len(path)-3:], "/")
		return fmt.Sprintf("[%s] %s:%d", "ForwardingServer", filepath, line)
	}
	outputWriter := zerolog.ConsoleWriter{Out: os.Stderr}
	log.Logger = log.Output(outputWriter).With().Caller().Logger()
	if outputPath != "" {
		file, err := os.OpenFile(
			outputPath,
			os.O_APPEND|os.O_CREATE|os.O_WRONLY,
			0664,
		)
		if err != nil {
			panic(err)
		}
		log.Logger = zerolog.New(file).With().Timestamp().Logger()
		fmt.Printf("Logging to file %s\n", outputPath)
	}

	// Set log level
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	if debug {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
		log.Debug().Msg("Debug logs enabled")
	}

	log.Info().Msg("Logger was set up")
}

// Used to start the program with the correct arguments
func main() {
	// Parse args
	debug := flag.Bool("debug", false, "show all logs (including debug)")
	output := flag.String("output", "", "path of the output file to log to")
	serverAddress := flag.String("server-address", livestreamconfig.ServerAddres, "address of the server to connect to")
	flag.Parse()

	setupLogging(*debug, *output)

	err := run(*serverAddress)
	if err != nil {
		log.Err(err).Msg("An unhandled error occurred. Quitting.")
		os.Exit(1)
	} else {
		log.Info().Msg("Program finished")
	}
}
