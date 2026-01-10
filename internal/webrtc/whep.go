package webrtc

import (
	"log"

	"github.com/google/uuid"
	"github.com/pion/webrtc/v4"
)

func WHEP(offer string) (string, string, error) {
	maybePrintOfferAnswer(offer, true)

	if str == nil {
		return "", "", webrtc.ErrConnectionClosed
	}

	whepSessionId := uuid.New().String()

	str.whepSessionsLock.Lock()
	str.whepSessions[whepSessionId] = struct{}{}
	str.whepSessionsLock.Unlock()
	cleanup := func() { listenerDisconnected(whepSessionId) }

	pc, err := newPeerConnection(apiWhep)
	if err != nil {
		cleanup()

		return "", "", err
	}

	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		if state == webrtc.ICEConnectionStateFailed || state == webrtc.ICEConnectionStateClosed {
			if err := pc.Close(); err != nil {
				log.Println(err)
			}

			cleanup()
		}
	})

	if _, err := pc.AddTrack(str.audioTrack); err != nil {
		cleanup()

		return "", "", err
	}

	if err := pc.SetRemoteDescription(webrtc.SessionDescription{
		SDP:  offer,
		Type: webrtc.SDPTypeOffer,
	}); err != nil {
		cleanup()

		return "", "", err
	}

	gatherComplete := webrtc.GatheringCompletePromise(pc)
	answer, err := pc.CreateAnswer(nil)

	if err != nil {
		cleanup()

		return "", "", err
	} else if err = pc.SetLocalDescription(answer); err != nil {
		cleanup()

		return "", "", err
	}

	<-gatherComplete

	return maybePrintOfferAnswer(appendAnswer(pc.LocalDescription().SDP), false), whepSessionId, nil
}
