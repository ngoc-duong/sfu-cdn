package pull

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	_ "net/http/pprof"
	"net/url"

	"github.com/go-logr/logr"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/pion/ion-sfu/cmd/signal/json-rpc/server"
	"github.com/pion/ion-sfu/pkg/sfu"
	"github.com/pion/webrtc/v3"
	"github.com/sourcegraph/jsonrpc2"
)

type Candidate struct {
	Target    int                  `json:"target"`
	Candidate *webrtc.ICECandidate `json:"candidate"`
}

type Response struct {
	Params *webrtc.SessionDescription `json:"params"`
	Result *webrtc.SessionDescription `json:"result"`
	Method string                     `json:"method"`
	Id     uint64                     `json:"id"`
}

type TrickleResponse struct {
	Params server.Trickle `json:"params"`
	Method string         `json:"method"`
}

func readMessage(c *websocket.Conn, p *server.JSONSignal, logger logr.Logger, done chan struct{}) {
	defer close(done)
	for {
		_, mess, errRead := c.ReadMessage()
		if errRead != nil {
			break
		}
		var response Response
		json.Unmarshal(mess, &response)
		if response.Result != nil {
			fmt.Println("got answer")
			result := *response.Result
			if err := p.SetRemoteDescription(result); err != nil {
				logger.Error(err, "Err set remote answer")
			}
		} else if response.Method == "offer" {
			fmt.Println("got offer")
			answer, err := p.Answer(*response.Params)
			if err != nil {
				logger.Error(err, "Err create ans")
			}

			connectionUUID := uuid.New()
			connectionID := uint64(connectionUUID.ID())

			offerJSON, _ := json.Marshal(&server.Negotiation{
				Desc: *answer,
			})

			params := (*json.RawMessage)(&offerJSON)

			answerMessage := &jsonrpc2.Request{
				Method: "answer",
				Params: params,
				ID: jsonrpc2.ID{
					IsString: false,
					Str:      "",
					Num:      connectionID,
				},
			}

			reqBodyBytes := new(bytes.Buffer)
			json.NewEncoder(reqBodyBytes).Encode(answerMessage)
			messageBytes := reqBodyBytes.Bytes()
			c.WriteMessage(websocket.TextMessage, messageBytes)
		} else if response.Method == "trickle" {
			fmt.Println("got trickle")
			var trickleResponse TrickleResponse
			if err := json.Unmarshal(mess, &trickleResponse); err != nil {
				logger.Error(err, "Err read trickle")
			}
			err := p.Trickle(trickleResponse.Params.Candidate, trickleResponse.Params.Target)
			if err != nil {
				logger.Error(err, "Err add candidate")
			}
		}
	}
}

func ConnectOrigin(s *sfu.SFU, logger logr.Logger) {
	var addrConn string
	flag.StringVar(&addrConn, "add", "localhost:7070", "address to use")
	flag.Parse()
	u := url.URL{Scheme: "ws", Host: addrConn, Path: "/ws"}
	logger.Info("connecting to", u.String())
	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)

	p := server.NewJSONSignal(sfu.NewPeer(s), logger)

	// p.OnOffer = func(sd *webrtc.SessionDescription) {
	// 	offerJSON, err := json.Marshal(sd)
	// 	if err != nil {
	// 		logger.Error(err, "Err marshal")
	// 	}
	// 	params := (*json.RawMessage)(&offerJSON)
	// 	connectionUUID := uuid.New()
	// 	connectionID := uint64(connectionUUID.ID())
	// 	offerMessage := &jsonrpc2.Request{
	// 		Method: "offer",
	// 		Params: params,
	// 		ID: jsonrpc2.ID{
	// 			IsString: false,
	// 			Str:      "",
	// 			Num:      connectionID,
	// 		},
	// 	}
	// 	reqBodyBytes := new(bytes.Buffer)
	// 	json.NewEncoder(reqBodyBytes).Encode(offerMessage)

	// 	messageBytes := reqBodyBytes.Bytes()
	// 	c.WriteMessage(websocket.TextMessage, messageBytes)
	// }

	p.OnIceCandidate = func(candidate *webrtc.ICECandidateInit, i int) {
		if candidate != nil {
			candidateJSON, err := json.Marshal(&server.Trickle{
				Candidate: *candidate,
				Target:    i,
			})

			params := (*json.RawMessage)(&candidateJSON)

			if err != nil {
				logger.Error(err, "Err candidate")
			}

			message := &jsonrpc2.Request{
				Method: "trickle",
				Params: params,
			}

			reqBodyBytes := new(bytes.Buffer)
			json.NewEncoder(reqBodyBytes).Encode(message)
			messageBytes := reqBodyBytes.Bytes()
			c.WriteMessage(websocket.TextMessage, messageBytes)
		}
	}

	p.Join("test room", "", sfu.JoinConfig{
		NoPublish:       false,
		NoSubscribe:     false,
		NoAutoSubscribe: false,
	})

	pc := p.Subscriber().GetPeerConnection()

	if err != nil {
		logger.Error(err, "Error connect origin")
	}
	defer c.Close()

	done := make(chan struct{})

	go readMessage(c, p, logger, done)

	offer, _ := pc.CreateOffer(nil)

	errSetDps := pc.SetLocalDescription(offer)
	if errSetDps != nil {
		logger.Error(errSetDps, "Err set dps")
	}
	offerJSON, err := json.Marshal(&server.Join{
		Offer: offer,
		SID:   "test room",
		UID:   "",
		Config: sfu.JoinConfig{
			NoPublish:       false,
			NoSubscribe:     false,
			NoAutoSubscribe: false,
		},
	})
	params := (*json.RawMessage)(&offerJSON)
	connectionUUID := uuid.New()
	connectionID := uint64(connectionUUID.ID())
	offerMessage := &jsonrpc2.Request{
		Method: "join",
		Params: params,
		ID: jsonrpc2.ID{
			IsString: false,
			Str:      "",
			Num:      connectionID,
		},
	}
	reqBodyBytes := new(bytes.Buffer)
	json.NewEncoder(reqBodyBytes).Encode(offerMessage)

	messageBytes := reqBodyBytes.Bytes()
	c.WriteMessage(websocket.TextMessage, messageBytes)

	<-done
}
