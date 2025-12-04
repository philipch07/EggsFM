package webrtc

import (
	"errors"
	"io"
	"log"

	"github.com/google/uuid"
	"github.com/pion/webrtc/v4"
)

func audioWriter(remoteTrack *webrtc.TrackRemote, stream *stream) {
	rtpBuf := make([]byte, 1500)
	for {
		rtpRead, _, err := remoteTrack.Read(rtpBuf)
		switch {
		case errors.Is(err, io.EOF):
			return
		case err != nil:
			log.Println(err)
			return
		}

		stream.audioPacketsReceived.Add(1)
		if _, writeErr := stream.audioTrack.Write(rtpBuf[:rtpRead]); writeErr != nil && !errors.Is(writeErr, io.ErrClosedPipe) {
			log.Println(writeErr)
			return
		}
	}
}

// this needs to go off of an env variable for the streamKey.
func WHIP(offer, streamKey string) (string, error) {
	maybePrintOfferAnswer(offer, true)

	whipSessionId := uuid.New().String()

	peerConnection, err := newPeerConnection(apiWhip)
	if err != nil {
		return "", err
	}

	streamMapLock.Lock()
	defer streamMapLock.Unlock()
	stream, err := getStream(streamKey, whipSessionId)
	if err != nil {
		return "", err
	}

	peerConnection.OnTrack(func(remoteTrack *webrtc.TrackRemote, rtpReceiver *webrtc.RTPReceiver) {
		audioWriter(remoteTrack, stream)
	})

	peerConnection.OnICEConnectionStateChange(func(i webrtc.ICEConnectionState) {
		if i == webrtc.ICEConnectionStateFailed || i == webrtc.ICEConnectionStateClosed {
			if err := peerConnection.Close(); err != nil {
				log.Println(err)
			}
			peerConnectionDisconnected(true, streamKey, whipSessionId)
		}
	})

	if err := peerConnection.SetRemoteDescription(webrtc.SessionDescription{
		SDP:  string(offer),
		Type: webrtc.SDPTypeOffer,
	}); err != nil {
		return "", err
	}

	gatherComplete := webrtc.GatheringCompletePromise(peerConnection)
	answer, err := peerConnection.CreateAnswer(nil)

	if err != nil {
		return "", err
	} else if err = peerConnection.SetLocalDescription(answer); err != nil {
		return "", err
	}

	<-gatherComplete
	return maybePrintOfferAnswer(appendAnswer(peerConnection.LocalDescription().SDP), false), nil
}
