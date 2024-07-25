package livestreamconfig

import (
	"fmt"

	"github.com/pion/webrtc/v4"
)

const (
	// The server address to bind to
	ServerScheme = "http"
	ServerHost   = "0.0.0.0"
	ServerPort   = 7500

	// Used to identify the car connection
	CarId = "car"

	// Used to identify the different data channels
	MetaChannelLabel    = "meta"
	ControlChannelLabel = "control"
	FrameChannelLabel   = "frame"

	// The UDP port to use for ICE candidate multiplexing
	// Updating this value also requires updating your Dockerfile and docker-compose.yaml
	MuxUdpPort = 40000
)

var ServerAddres = fmt.Sprintf("%s://%s:%d", ServerScheme, ServerHost, ServerPort)

// By commenting out the ICE server, communication over LAN is possible
var PeerConnectionConfig = webrtc.Configuration{
	ICEServers: []webrtc.ICEServer{
		// {
		// 	URLs: []string{"stun:stun.l.google.com:19302"},
		// },
	},
}
