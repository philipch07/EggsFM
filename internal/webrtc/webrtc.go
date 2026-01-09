package webrtc

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pion/dtls/v3/pkg/crypto/elliptic"
	"github.com/pion/ice/v3"
	"github.com/pion/interceptor"
	"github.com/pion/webrtc/v4"
)

type (
	stream struct {
		firstSeenEpoch uint64

		// Single shared Opus audio track for all listeners.
		audioTrack *webrtc.TrackLocalStaticSample

		whepSessionsLock sync.RWMutex
		whepSessions     map[string]struct{}

		// track metadata for /status endpoint
		nowPlayingLock    sync.RWMutex
		nowPlayingTitle   string
		nowPlayingArtists []string
	}
)

var (
	str     *stream
	apiWhep *webrtc.API
)

var errNotConfigured = errors.New("webrtc not configured")

// GetAudioTrack is what your server-side streamer should use to
// obtain the TrackLocalStaticRTP and write Opus RTP packets into it.
func GetAudioTrack() (*webrtc.TrackLocalStaticSample, error) {
	if str == nil || str.audioTrack == nil {
		return nil, errNotConfigured
	}
	return str.audioTrack, nil
}

// listenerDisconnected is called when a WHEP listener PeerConnection closes/fails.
func listenerDisconnected(sessionId string) {
	if str == nil {
		return
	}
	str.whepSessionsLock.Lock()
	delete(str.whepSessions, sessionId)
	str.whepSessionsLock.Unlock()
}

func getPublicIP() string {
	req, err := http.Get("http://ip-api.com/json/")
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if closeErr := req.Body.Close(); closeErr != nil {
			log.Fatal(err)
		}
	}()

	body, err := io.ReadAll(req.Body)
	if err != nil {
		log.Fatal(err)
	}

	ip := struct{ Query string }{}
	if err = json.Unmarshal(body, &ip); err != nil {
		log.Fatal(err)
	}

	if ip.Query == "" {
		log.Fatal("Query entry was not populated")
	}

	return ip.Query
}

func createSettingEngine(isWHIP bool, udpMuxCache map[int]*ice.MultiUDPMuxDefault, tcpMuxCache map[string]ice.TCPMux) (settingEngine webrtc.SettingEngine) {
	var (
		NAT1To1IPs   []string
		networkTypes []webrtc.NetworkType
		udpMuxPort   int
		udpMuxOpts   []ice.UDPMuxFromPortOption
		err          error
	)

	if os.Getenv("NETWORK_TYPES") != "" {
		for _, networkTypeStr := range strings.Split(os.Getenv("NETWORK_TYPES"), "|") {
			networkType, err := webrtc.NewNetworkType(networkTypeStr)
			if err != nil {
				log.Fatal(err)
			}
			networkTypes = append(networkTypes, networkType)
		}
	} else {
		networkTypes = append(networkTypes, webrtc.NetworkTypeUDP4, webrtc.NetworkTypeUDP6)
	}

	if os.Getenv("INCLUDE_PUBLIC_IP_IN_NAT_1_TO_1_IP") != "" {
		NAT1To1IPs = append(NAT1To1IPs, getPublicIP())
	}

	if os.Getenv("NAT_1_TO_1_IP") != "" {
		NAT1To1IPs = append(NAT1To1IPs, strings.Split(os.Getenv("NAT_1_TO_1_IP"), "|")...)
	}

	natICECandidateType := webrtc.ICECandidateTypeHost
	if os.Getenv("NAT_ICE_CANDIDATE_TYPE") == "srflx" {
		natICECandidateType = webrtc.ICECandidateTypeSrflx
	}

	if len(NAT1To1IPs) != 0 {
		mode := webrtc.ICEAddressRewriteReplace
		if natICECandidateType == webrtc.ICECandidateTypeSrflx {
			mode = webrtc.ICEAddressRewriteAppend
		}

		err := settingEngine.SetICEAddressRewriteRules(webrtc.ICEAddressRewriteRule{
			External:        NAT1To1IPs,
			AsCandidateType: natICECandidateType,
			Mode:            mode,
		})

		if err != nil {
			log.Fatal(err)
		}
	}

	if os.Getenv("INTERFACE_FILTER") != "" {
		interfaceFilter := func(i string) bool {
			return i == os.Getenv("INTERFACE_FILTER")
		}

		settingEngine.SetInterfaceFilter(interfaceFilter)
		udpMuxOpts = append(udpMuxOpts, ice.UDPMuxFromPortWithInterfaceFilter(interfaceFilter))
	}

	if isWHIP && os.Getenv("UDP_MUX_PORT_WHIP") != "" {
		if udpMuxPort, err = strconv.Atoi(os.Getenv("UDP_MUX_PORT_WHIP")); err != nil {
			log.Fatal(err)
		}
	} else if !isWHIP && os.Getenv("UDP_MUX_PORT_WHEP") != "" {
		if udpMuxPort, err = strconv.Atoi(os.Getenv("UDP_MUX_PORT_WHEP")); err != nil {
			log.Fatal(err)
		}
	} else if os.Getenv("UDP_MUX_PORT") != "" {
		if udpMuxPort, err = strconv.Atoi(os.Getenv("UDP_MUX_PORT")); err != nil {
			log.Fatal(err)
		}
	}

	if udpMuxPort != 0 {
		udpMux, ok := udpMuxCache[udpMuxPort]
		if !ok {
			if udpMux, err = ice.NewMultiUDPMuxFromPort(udpMuxPort, udpMuxOpts...); err != nil {
				log.Fatal(err)
			}
			udpMuxCache[udpMuxPort] = udpMux
		}

		settingEngine.SetICEUDPMux(udpMux)
	}

	if os.Getenv("TCP_MUX_ADDRESS") != "" {
		tcpMux, ok := tcpMuxCache[os.Getenv("TCP_MUX_ADDRESS")]
		if !ok {
			tcpAddr, err := net.ResolveTCPAddr("tcp", os.Getenv("TCP_MUX_ADDRESS"))
			if err != nil {
				log.Fatal(err)
			}

			tcpListener, err := net.ListenTCP("tcp", tcpAddr)
			if err != nil {
				log.Fatal(err)
			}

			tcpMux = webrtc.NewICETCPMux(nil, tcpListener, 8)
			tcpMuxCache[os.Getenv("TCP_MUX_ADDRESS")] = tcpMux
		}
		settingEngine.SetICETCPMux(tcpMux)

		if os.Getenv("TCP_MUX_FORCE") != "" {
			networkTypes = []webrtc.NetworkType{webrtc.NetworkTypeTCP4, webrtc.NetworkTypeTCP6}
		} else {
			networkTypes = append(networkTypes, webrtc.NetworkTypeTCP4, webrtc.NetworkTypeTCP6)
		}
	}

	settingEngine.SetDTLSEllipticCurves(elliptic.X25519, elliptic.P384, elliptic.P256)
	settingEngine.SetNetworkTypes(networkTypes)
	settingEngine.DisableSRTCPReplayProtection(true)
	settingEngine.DisableSRTPReplayProtection(true)
	settingEngine.SetIncludeLoopbackCandidate(os.Getenv("INCLUDE_LOOPBACK_CANDIDATE") != "")

	return
}

// PopulateMediaEngine registers only Opus (48kHz, stereo).
func PopulateMediaEngine(m *webrtc.MediaEngine) error {
	return m.RegisterCodec(
		webrtc.RTPCodecParameters{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:    webrtc.MimeTypeOpus,
				ClockRate:   48000,
				Channels:    2,
				SDPFmtpLine: "minptime=10;useinbandfec=1;maxaveragebitrate=192000",
			},
			PayloadType: 111,
		},
		webrtc.RTPCodecTypeAudio,
	)
}

func newPeerConnection(api *webrtc.API) (*webrtc.PeerConnection, error) {
	cfg := webrtc.Configuration{}

	if stunServers := os.Getenv("STUN_SERVERS"); stunServers != "" {
		for _, stunServer := range strings.Split(stunServers, "|") {
			cfg.ICEServers = append(cfg.ICEServers, webrtc.ICEServer{
				URLs: []string{"stun:" + stunServer},
			})
		}
	}

	return api.NewPeerConnection(cfg)
}

func appendAnswer(in string) string {
	if extraCandidate := os.Getenv("APPEND_CANDIDATE"); extraCandidate != "" {
		index := strings.Index(in, "a=end-of-candidates")
		if index >= 0 {
			in = in[:index] + extraCandidate + in[index:]
		}
	}

	return in
}

func maybePrintOfferAnswer(sdp string, isOffer bool) string {
	if os.Getenv("DEBUG_PRINT_OFFER") != "" && isOffer {
		fmt.Println(sdp)
	}

	if os.Getenv("DEBUG_PRINT_ANSWER") != "" && !isOffer {
		fmt.Println(sdp)
	}

	return sdp
}

func Configure() {
	audioTrack, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{
			MimeType:  webrtc.MimeTypeOpus,
			ClockRate: 48000,
			Channels:  2,
		},
		"audio",
		"EggsFM",
	)

	if err != nil {
		panic(err)
	}

	str = &stream{
		audioTrack:     audioTrack,
		whepSessions:   map[string]struct{}{},
		firstSeenEpoch: uint64(time.Now().Unix()),

		// defaults so /status is never blank/null
		nowPlayingTitle:   "-",
		nowPlayingArtists: []string{},
	}

	mediaEngine := &webrtc.MediaEngine{}
	if err := PopulateMediaEngine(mediaEngine); err != nil {
		panic(err)
	}

	interceptorRegistry := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(mediaEngine, interceptorRegistry); err != nil {
		log.Fatal(err)
	}

	udpMuxCache := map[int]*ice.MultiUDPMuxDefault{}
	tcpMuxCache := map[string]ice.TCPMux{}

	apiWhep = webrtc.NewAPI(
		webrtc.WithMediaEngine(mediaEngine),
		webrtc.WithInterceptorRegistry(interceptorRegistry),
		webrtc.WithSettingEngine(createSettingEngine(false, udpMuxCache, tcpMuxCache)),
	)
}

// StreamStatus is the exposed status for each audio-only stream.
type StreamStatus struct {
	StreamKey      string   `json:"streamKey"`
	FirstSeenEpoch uint64   `json:"firstSeenEpoch"`
	ListenerCount  int      `json:"listenerCount"`
	NowPlaying     string   `json:"nowPlaying"`
	Artists        []string `json:"artists"`
}

func GetStreamStatus() []StreamStatus {
	str.whepSessionsLock.RLock()
	listenerCount := len(str.whepSessions)
	str.whepSessionsLock.RUnlock()

	title, artists := CurrentNowPlaying()
	if strings.TrimSpace(title) == "" {
		title = "-"
	}
	if artists == nil {
		artists = []string{}
	}

	return []StreamStatus{{
		StreamKey:      "default",
		FirstSeenEpoch: str.firstSeenEpoch,
		ListenerCount:  listenerCount,
		NowPlaying:     title,
		Artists:        artists,
	}}
}
