package events

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"google.golang.org/protobuf/proto"

	pb_remote_config_messages "github.com/VU-ASE/rovercom/packages/go"

	livestreamconfig "vu/ase/streamserver/src/config"
	"vu/ase/streamserver/src/peerconnection"
	"vu/ase/streamserver/src/state"

	rtc "github.com/VU-ASE/roverrtc/src"

	"github.com/pion/webrtc/v4"
)

// Called when a client sends an offer to the HTTP server
func OnClientSDPReceived(sdp rtc.RequestSDP, state *state.ServerState) ([]byte, error) {
	// Create a new RTCPeerConnection
	rtc, err := peerconnection.CreateFromOffer(sdp.Offer, sdp.Id, livestreamconfig.PeerConnectionConfig, state.RtcApi)
	if err != nil {
		return nil, err
	}

	log := rtc.Log()

	// Register connection state change handler
	rtc.Pc.OnConnectionStateChange(onClientConnectionChange(rtc, state))

	// Add rtc to list of client connections
	err = state.ConnectedPeers.Add(sdp.Id, rtc, false)
	if err != nil {
		return nil, err
	}

	log.Info().Msg("Received SDP offer from client")

	// Register data channel creation and other handlers
	OnClientSDPReturned(rtc, state)

	// Send answer back to client
	payload, err := json.Marshal(rtc.Pc.LocalDescription())
	if err != nil {
		return nil, err
	}

	return payload, nil
}

// Called when a client sends an ICE candidate to the HTTP server
func OnClientICEReceived(ice rtc.RequestICE, state *state.ServerState) ([]byte, error) {
	// Get connection from list of connections
	rtc := state.ConnectedPeers.Get(ice.Id)
	if rtc == nil {
		return nil, fmt.Errorf("Client connection with id %s does not exist", ice.Id)
	}

	log := rtc.Log()

	// Add to list of remote candidates
	if err := rtc.Pc.AddICECandidate(ice.Candidate); err != nil {
		return nil, err
	}

	log.Info().Msg("Received ICE candidates from client")

	// Return all candidates to client
	payload, err := json.Marshal(rtc.GetAllLocalCandidates())
	if err != nil {
		return nil, err
	}

	return payload, nil
}

// Create handlers for data channels
func OnClientSDPReturned(r *rtc.RTC, state *state.ServerState) {
	log := r.Log()

	// Register data channel creation
	r.Pc.OnDataChannel(func(d *webrtc.DataChannel) {
		log.Debug().Str("label", d.Label()).Msg("Client datachannel was created")

		// Register channel opening handling
		d.OnOpen(func() {
			log.Debug().Str("label", d.Label()).Msg("Client datachannel was opened for communication")

			switch d.Label() {
			case livestreamconfig.ControlChannelLabel:
				registerClientControlMessage(r, d, state)
			case livestreamconfig.MetaChannelLabel:
				registerClientMetaMessage(r, d, state)
			case livestreamconfig.FrameChannelLabel:
				registerClientFrameMessage(r, d, state)
			default:
				log.Warn().Str("label", d.Label()).Msg("Unknown client datachannel was opened for communication")
			}
		})
	})
}

// Used to debug connection state changes
func onClientConnectionChange(client *rtc.RTC, state *state.ServerState) func(webrtc.PeerConnectionState) {
	log := client.Log()

	return func(s webrtc.PeerConnectionState) {
		log.Info().Str("newState", s.String()).Msg("Client connection changed to new state")

		if s == webrtc.PeerConnectionStateDisconnected || s == webrtc.PeerConnectionStateClosed || s == webrtc.PeerConnectionStateFailed {
			// Remove the client from the list of connected clients
			_ = state.ConnectedPeers.Remove(client.Id)
			client.Destroy()

			// If this client was the active controller, remove the active controller and let everyone know
			state.Lock.RLock()
			activeController := state.ActiveController
			state.Lock.RUnlock()

			if activeController == client.Id {
				state.Lock.Lock()
				state.ActiveController = ""
				state.Lock.Unlock()

				notification := pb_remote_config_messages.ConfigMessage{
					Action: &pb_remote_config_messages.ConfigMessage_HumanControlState_{
						HumanControlState: &pb_remote_config_messages.ConfigMessage_HumanControlState{
							ActiveControllerId: "",
						},
					},
				}

				state.ConnectedPeers.ForEach(func(id string, r *rtc.RTC) {
					err := r.SendMetaMessage(&notification)
					if err != nil {
						log.Err(err).Str("clientId", id).Msg("Could not notify connected client of human control release")
					}
				})
			}
		} else if s == webrtc.PeerConnectionStateConnected {
			// Check if there already is a car connected, and send its car state if so
			state.Lock.RLock()
			car := state.ConnectedPeers.Get(livestreamconfig.CarId)
			state.Lock.RUnlock()
			if car == nil {
				return
			}

			// Create proto message to notify client that a car is connected before they were connected
			notification := pb_remote_config_messages.ConfigMessage{
				Action: &pb_remote_config_messages.ConfigMessage_CarState_{
					CarState: &pb_remote_config_messages.ConfigMessage_CarState{
						Connected:       car.Pc.ConnectionState() == webrtc.PeerConnectionStateConnected,
						TimestampOffset: car.TimestampOffset,
					},
				},
			}

			// Sleep for 2 seconds to let the webcontroller set up the correct data channel handlers to process our message
			time.Sleep(2 * time.Second)

			log.Info().Msg("Notifying client of connected car")

			// Notify the client of the car state
			err := client.SendMetaMessage(&notification)
			if err != nil {
				log.Err(err).Msg("Could not notify connected client of connected car")
			}
		}
	}
}

//
// Register data channel message handlers
//

func registerClientControlMessage(client *rtc.RTC, dc *webrtc.DataChannel, state *state.ServerState) {
	client.ControlChannel = dc

	// Register text message handling
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		//
		// ...
		// debug statements
		// can live here
		// ...
		//

		// Get the car connection
		car := state.ConnectedPeers.Get(livestreamconfig.CarId)

		if car != nil {
			log.Debug().Int("length", len(msg.Data)).Msg("Forwarding client --> car control data")

			// Car is connexcted, try forwarding the control data
			err := car.SendControlBytes(msg.Data)
			if err != nil {
				log.Err(err).Msg("Could not forward control data")

				// Report error to the client
				notification := pb_remote_config_messages.ConfigMessage{
					Action: &pb_remote_config_messages.ConfigMessage_Error_{
						Error: &pb_remote_config_messages.ConfigMessage_Error{
							Message: err.Error(),
						},
					},
				}
				_ = client.SendMetaMessage(&notification)
			}
		} else {
			// Car disconnected
			log.Warn().Msg("Could not forward control data, car disconnected")

			// Report to all clients that the car is not connected
			notification := pb_remote_config_messages.ConfigMessage{
				Action: &pb_remote_config_messages.ConfigMessage_CarState_{
					CarState: &pb_remote_config_messages.ConfigMessage_CarState{
						Connected: false,
					},
				},
			}

			// Send this error to all properly configured clients (best-effort)
			state.ConnectedPeers.ForEach(func(id string, r *rtc.RTC) {
				_ = r.SendMetaMessage(&notification)
			})
		}
	})
}

func registerClientMetaMessage(client *rtc.RTC, dc *webrtc.DataChannel, state *state.ServerState) {
	client.MetaChannel = dc

	// Register text message handling
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		// Parse the meta message
		parsedMsg := pb_remote_config_messages.ConfigMessage{}
		err := proto.Unmarshal(msg.Data, &parsedMsg)
		if err != nil {
			log.Err(err).Msg("Could not parse incoming client meta message")
			// Send this error to the client
			notification := pb_remote_config_messages.ConfigMessage{
				Action: &pb_remote_config_messages.ConfigMessage_Error_{
					Error: &pb_remote_config_messages.ConfigMessage_Error{
						Message: err.Error(),
					},
				},
			}
			_ = client.SendMetaMessage(&notification)
			return
		}

		log.Debug().Msg("Received meta message")

		//
		// ...
		// add debug statements here
		// ...
		//

		err = nil
		switch parsedMsg.Action.(type) {
		case *pb_remote_config_messages.ConfigMessage_HumanControlRequest_:
			request := parsedMsg.GetHumanControlRequest()
			if request == nil {
				err = fmt.Errorf("Human control request is nil")
				break
			}
			if request.Type == pb_remote_config_messages.ConfigMessage_HUMAN_CONTROL_RELEASE {
				log.Debug().Msg("Received human control release request")
				err = onClientRequestControlRelease(client, state)
			} else if request.Type == pb_remote_config_messages.ConfigMessage_HUMAN_CONTROL_TAKEOVER {
				log.Debug().Msg("Received human control takeover request")
				err = onClientRequestControlTakeover(client, state)
			} else {
				err = fmt.Errorf("Unknown human control request type")
			}

		default:

			err = fmt.Errorf("Meta message action is not supported")
		}

		// Log errors
		if err != nil {
			log.Err(err).Msg("Client meta message handler returned error")
		}
	})
}

func registerClientFrameMessage(client *rtc.RTC, dc *webrtc.DataChannel, state *state.ServerState) {
	client.FrameChannel = dc

	// Register text message handling
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		// act based on frame message
	})
}

//
// Actions based on meta messages by the client
//

func onClientRequestControlTakeover(client *rtc.RTC, state *state.ServerState) error {
	state.Lock.Lock()
	defer state.Lock.Unlock()

	currentController := state.ConnectedPeers.Get(state.ActiveController)
	if currentController != nil && currentController.IsConnected() && currentController.Id != client.Id {
		err := fmt.Errorf("Cannot request control takeover: there is already an active controller")

		notification := pb_remote_config_messages.ConfigMessage{
			Action: &pb_remote_config_messages.ConfigMessage_Error_{
				Error: &pb_remote_config_messages.ConfigMessage_Error{
					Message: err.Error(),
				},
			},
		}
		sendErr := client.SendMetaMessage(&notification)

		if sendErr != nil {
			err = errors.Join(sendErr, err)
		}
		return err
	}

	// update the active controller
	// todo: make this a function
	state.ActiveController = client.Id

	notification := pb_remote_config_messages.ConfigMessage{
		Action: &pb_remote_config_messages.ConfigMessage_HumanControlState_{
			HumanControlState: &pb_remote_config_messages.ConfigMessage_HumanControlState{
				ActiveControllerId: client.Id,
			},
		},
	}

	state.ConnectedPeers.ForEach(func(id string, r *rtc.RTC) {
		err := r.SendMetaMessage(&notification)
		if err != nil {
			log.Err(err).Msg("Could not broadcast controller state")
		}
	})

	return nil
}

func onClientRequestControlRelease(client *rtc.RTC, state *state.ServerState) error {
	state.Lock.Lock()
	defer state.Lock.Unlock()

	// Check if the client is the current controller
	currentController := state.ActiveController
	if currentController != client.Id {
		err := fmt.Errorf("Cannot release control: you are not the active controller")

		// Send this error to the client
		notification := pb_remote_config_messages.ConfigMessage{
			Action: &pb_remote_config_messages.ConfigMessage_Error_{
				Error: &pb_remote_config_messages.ConfigMessage_Error{
					Message: err.Error(),
				},
			},
		}
		sendErr := client.SendMetaMessage(&notification)

		if sendErr != nil {
			err = errors.Join(sendErr, err)
		}

		return err
	}

	// Update the active controller
	// todo: make this a function
	state.ActiveController = ""

	// Send a message to all clients that the controller has changed
	notification := pb_remote_config_messages.ConfigMessage{
		Action: &pb_remote_config_messages.ConfigMessage_HumanControlState_{
			HumanControlState: &pb_remote_config_messages.ConfigMessage_HumanControlState{
				ActiveControllerId: "",
			},
		},
	}

	state.ConnectedPeers.ForEach(func(id string, r *rtc.RTC) {
		err := r.SendMetaMessage(&notification)
		if err != nil {
			log.Err(err).Msg("Could not broadcast controller state")
		}
	})

	return nil
}
