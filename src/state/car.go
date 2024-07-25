package state

import (
	"fmt"
	"os"
	"sync"
	livestreamconfig "vu/ase/streamserver/src/config"

	rtc "github.com/VU-ASE/pkg-Rtc/src"
	"github.com/pion/ice/v3"
	"github.com/pion/webrtc/v4"

	"github.com/rs/zerolog/log"
)

// This makes the server easier to test and mock and also allows us to
// add more fields to the server state in the future.
type ServerState struct {
	RtcApi           *webrtc.API
	ConnectedPeers   *rtc.RTCMap
	ActiveController string        // id of the controller that is currently controlling the car
	Lock             *sync.RWMutex // to make sure ICE candidates can be managed concurrently
}

func NewServerState() (*ServerState, error) {
	s := webrtc.SettingEngine{}

	// This is necessary when running through Docker, so that the ICE candidates can be resolved
	serverIp := os.Getenv("ASE_FWSERVER_IP")
	if serverIp == "" {
		return nil, fmt.Errorf("ASE_FWSERVER_IP environment variable not set. Please set it to your local IP address (192.168.0.XXX)")
	} else {
		log.Info().Msgf("ForwardingServer webRTC listener active on '%s:%d'", serverIp, livestreamconfig.MuxUdpPort)
	}
	s.SetNAT1To1IPs([]string{serverIp}, webrtc.ICECandidateTypeHost)

	mux, err := ice.NewMultiUDPMuxFromPort(livestreamconfig.MuxUdpPort)
	if err != nil {
		return nil, fmt.Errorf("Could not create UDP mux: %v", err)
	}
	s.SetICEUDPMux(mux)

	// Create a local PeerConnection
	api := webrtc.NewAPI(webrtc.WithSettingEngine(s))

	return &ServerState{
		RtcApi:           api,
		ConnectedPeers:   rtc.NewRTCMap(),
		ActiveController: "",
		Lock:             &sync.RWMutex{},
	}, nil
}

// Destroy all connections and the server state
func (s *ServerState) Destroy() {
	s.Lock.Lock()
	defer s.Lock.Unlock()

	for _, peer := range s.ConnectedPeers.UnsafeGetAll() {
		_ = s.ConnectedPeers.Remove(peer.Id)
		peer.Destroy()
	}

	log.Info().Msg("Destroyed server state")
}
