package webrtc

import (
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
			_ = pc.Close()
			cleanup()
		}
	})

	rtpSender, err := pc.AddTrack(str.audioTrack)
	if err != nil {
		cleanup()
		return "", "", err
	}

	// i have no idea if we need to drain the RTCP so the sender doesn't stall.
	go func() {
		rtcpBuf := make([]byte, 1500)
		for {
			if _, _, rtcpErr := rtpSender.Read(rtcpBuf); rtcpErr != nil {
				return
			}
		}
	}()

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
	}
	if err = pc.SetLocalDescription(answer); err != nil {
		cleanup()

		return "", "", err
	}

	<-gatherComplete

	return maybePrintOfferAnswer(appendAnswer(pc.LocalDescription().SDP), false), whepSessionId, nil
}
