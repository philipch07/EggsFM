package webrtc

import (
	"log"

	"github.com/google/uuid"
	"github.com/pion/webrtc/v4"
)

// WHEP creates a listener PeerConnection that receives the shared audio track.
func WHEP(offer, streamKey string) (string, string, error) {
	maybePrintOfferAnswer(offer, true)

	// Get or create the audio stream for this key.
	streamMapLock.Lock()
	stream, err := getStream(streamKey)
	streamMapLock.Unlock()
	if err != nil {
		return "", "", err
	}

	whepSessionId := uuid.New().String()

	peerConnection, err := newPeerConnection(apiWhep)
	if err != nil {
		return "", "", err
	}

	peerConnection.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		if state == webrtc.ICEConnectionStateFailed || state == webrtc.ICEConnectionStateClosed {
			if err := peerConnection.Close(); err != nil {
				log.Println(err)
			}
			// listener disconnect
			listenerDisconnected(streamKey, whepSessionId)
		}
	})

	// Fan-out the single Opus TrackLocalStaticRTP to this listener.
	if _, err = peerConnection.AddTrack(stream.audioTrack); err != nil {
		return "", "", err
	}

	if err := peerConnection.SetRemoteDescription(webrtc.SessionDescription{
		SDP:  offer,
		Type: webrtc.SDPTypeOffer,
	}); err != nil {
		return "", "", err
	}

	gatherComplete := webrtc.GatheringCompletePromise(peerConnection)
	answer, err := peerConnection.CreateAnswer(nil)
	if err != nil {
		return "", "", err
	}
	if err = peerConnection.SetLocalDescription(answer); err != nil {
		return "", "", err
	}

	<-gatherComplete

	// Register this listener for counting & cleanup.
	stream.whepSessionsLock.Lock()
	stream.whepSessions[whepSessionId] = struct{}{}
	stream.whepSessionsLock.Unlock()

	return maybePrintOfferAnswer(appendAnswer(peerConnection.LocalDescription().SDP), false), whepSessionId, nil
}
