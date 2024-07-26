package events

import (
	"encoding/json"
	"fmt"

	livestreamconfig "vu/ase/streamserver/src/config"
	"vu/ase/streamserver/src/peerconnection"
	"vu/ase/streamserver/src/state"

	pb_remote_config_messages "github.com/VU-ASE/rovercom/packages/go"

	rtc "github.com/VU-ASE/roverrtc/src"

	"github.com/pion/webrtc/v4"
	"github.com/rs/zerolog/log"
)

// Called when a car sends an offer to the HTTP server
func OnCarSDPReceived(sdp rtc.RequestSDP, receivedAt int64, state *state.ServerState) ([]byte, error) {
	// Create a new RTCPeerConnection
	rtc, err := peerconnection.CreateFromOffer(sdp.Offer, sdp.Id, livestreamconfig.PeerConnectionConfig, state.RtcApi)
	if err != nil {
		return nil, err
	}

	// Set the timestamp offset based on the registration time and the time this offer was received
	rtc.TimestampOffset = sdp.Timestamp - receivedAt

	log := rtc.Log()

	// Register event handlers from now on
	rtc.Pc.OnConnectionStateChange(onCarConnectionChange(rtc, state))

	log.Info().Msg("Received SDP offer from car")

	// Add rtc to list of car connections (there can be only one car connection)
	err = state.ConnectedPeers.Add(livestreamconfig.CarId, rtc, true)
	if err != nil {
		return nil, err
	}

	// Register data channel creation and other handlers
	OnCarSDPReturned(rtc, state)

	// Send answer back to car
	payload, err := json.Marshal(rtc.Pc.LocalDescription())
	if err != nil {
		return nil, err
	}

	return payload, nil
}

// Called when a car sends an ICE candidate to the HTTP server
func OnCarICEReceived(ice rtc.RequestICE, state *state.ServerState) ([]byte, error) {
	// Get connection from list of connections
	rtc := state.ConnectedPeers.Get(livestreamconfig.CarId)
	if rtc == nil {
		return nil, fmt.Errorf("Car connection with id %s does not exist", ice.Id)
	}

	log := rtc.Log()

	// Add to list of remote candidates
	if err := rtc.Pc.AddICECandidate(ice.Candidate); err != nil {
		return nil, err
	}

	log.Info().Msg("Received ICE candidates from car")

	// Return all candidates to client
	payload, err := json.Marshal(rtc.GetAllLocalCandidates())
	if err != nil {
		return nil, err
	}
	return payload, nil
}

// Register data channel creation and other handlers
func OnCarSDPReturned(r *rtc.RTC, state *state.ServerState) {
	log := r.Log()

	// Register data channel creation
	r.Pc.OnDataChannel(func(d *webrtc.DataChannel) {
		log.Debug().Str("label", d.Label()).Msg("Car datachannel was created")

		// Register channel opening handling
		d.OnOpen(func() {
			log.Info().Str("label", d.Label()).Msg("Car datachannel was opened for communication")

			switch d.Label() {
			case livestreamconfig.ControlChannelLabel:
				r.ControlChannel = d
				registerCarControlMessage(d, state)
			case livestreamconfig.MetaChannelLabel:
				registerCarMetaMessage(d, state)
			case livestreamconfig.FrameChannelLabel:
				registerCarFrameMessage(d, state)
			default:
				log.Warn().Str("label", d.Label()).Msg("Unknown car datachannel was opened for communication")
			}
		})
	})
}

// Used to debug connection state changes
func onCarConnectionChange(car *rtc.RTC, state *state.ServerState) func(webrtc.PeerConnectionState) {
	log := car.Log()

	return func(s webrtc.PeerConnectionState) {
		log.Debug().Msgf("Car connection changed to new state %s", s.String())

		// Create proto message
		notification := pb_remote_config_messages.ConfigMessage{
			Action: &pb_remote_config_messages.ConfigMessage_CarState_{
				CarState: &pb_remote_config_messages.ConfigMessage_CarState{
					Connected:       s == webrtc.PeerConnectionStateConnected,
					TimestampOffset: car.TimestampOffset,
				},
			},
		}

		// Notify all clients of the new connection state
		state.ConnectedPeers.ForEach(func(id string, r *rtc.RTC) {
			if r.Id != car.Id {
				err := r.SendMetaMessage(&notification)
				if err != nil {
					log.Err(err).Str("clientId", id).Msg("Could not notify connected client of connected car")
				}
			}
		})

		if s == webrtc.PeerConnectionStateConnected {
			// handle connect
		} else if s == webrtc.PeerConnectionStateDisconnected || s == webrtc.PeerConnectionStateClosed || s == webrtc.PeerConnectionStateFailed {
			// disconnected, remove from list of connected peers
			_ = state.ConnectedPeers.Remove(car.Id)
			car.Destroy()
		}
	}
}

//
// Events based on data channel messages
//

func registerCarControlMessage(dc *webrtc.DataChannel, state *state.ServerState) {
	// Register text message handling
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		// act based on control message
	})
}

func registerCarMetaMessage(dc *webrtc.DataChannel, state *state.ServerState) {
	// Register text message handling
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		// act based on control message
	})
}

func registerCarFrameMessage(dc *webrtc.DataChannel, state *state.ServerState) {
	// Register text message handling
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		log.Debug().Int("length", len(msg.Data)).Msg("Forwarding car --> client frame data")

		// Forward the message to all clients
		state.ConnectedPeers.ForEach(func(id string, r *rtc.RTC) {
			if id == livestreamconfig.CarId {
				return
			}

			err := r.SendFrameBytes(msg.Data)
			if err != nil {
				log.Err(err).Str("clientId", id).Msg("Could not forward frame data to client")
			}
		})
	})
}
