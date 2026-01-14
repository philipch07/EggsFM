package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/philipch07/EggsFM/internal/hls"
	"github.com/philipch07/EggsFM/internal/webrtc"
)

const (
	envFileProd = ".env.production"
)

func logHTTPError(w http.ResponseWriter, err string, code int) {
	log.Println(err)
	http.Error(w, err, code)
}

// WHEP handler: listeners connect here to receive the server-published audio.
func whepHandler(res http.ResponseWriter, req *http.Request) {
	if req.Method != "POST" {
		return
	}

	offer, err := io.ReadAll(req.Body)
	if err != nil {
		logHTTPError(res, err.Error(), http.StatusBadRequest)
		return
	}

	answer, _, err := webrtc.WHEP(string(offer))
	if err != nil {
		logHTTPError(res, err.Error(), http.StatusBadRequest)
		return
	}

	res.Header().Add("Location", "/api/whep")
	res.Header().Add("Content-Type", "application/sdp")
	res.WriteHeader(http.StatusCreated)
	if _, err = fmt.Fprint(res, answer); err != nil {
		log.Println(err)
	}
}

// can be used for health checks and auto-restart if boom boom
func statusHandler(res http.ResponseWriter, req *http.Request) {
	if os.Getenv("DISABLE_STATUS") != "" {
		logHTTPError(res, "Status Service Unavailable", http.StatusServiceUnavailable)
		return
	}

	res.Header().Add("Content-Type", "application/json")

	if err := json.NewEncoder(res).Encode(webrtc.GetStreamStatus()); err != nil {
		logHTTPError(res, err.Error(), http.StatusBadRequest)
	}
}

func corsHandler(next func(w http.ResponseWriter, r *http.Request)) http.HandlerFunc {
	return func(res http.ResponseWriter, req *http.Request) {
		res.Header().Set("Access-Control-Allow-Origin", "*")
		res.Header().Set("Access-Control-Allow-Methods", "*")
		res.Header().Set("Access-Control-Allow-Headers", "*")
		res.Header().Set("Access-Control-Expose-Headers", "*")

		if req.Method != http.MethodOptions {
			next(res, req)
		}
	}
}

type cursorSource interface {
	Position() time.Duration
}

func parseDurationEnv(name string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	if d, err := time.ParseDuration(raw); err == nil {
		if d <= 0 {
			return 0
		}
		return d
	}
	if secs, err := strconv.ParseFloat(raw, 64); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs * float64(time.Second))
	}

	return fallback
}

func startCursorWatchdog(cursor cursorSource, stall time.Duration, hlsStreamer *hls.Streamer) {
	if cursor == nil || stall <= 0 {
		return
	}

	checkEvery := stall / 2
	if checkEvery < time.Second {
		checkEvery = time.Second
	}

	go func() {
		ticker := time.NewTicker(checkEvery)
		defer ticker.Stop()

		lastPos := cursor.Position()
		lastChange := time.Now()
		var lastRestart time.Time

		for range ticker.C {
			pos := cursor.Position()
			if pos != lastPos {
				lastPos = pos
				lastChange = time.Now()
				continue
			}

			stalledFor := time.Since(lastChange)
			if stalledFor < stall {
				continue
			}
			if !lastRestart.IsZero() && time.Since(lastRestart) < stall {
				continue
			}

			hlsDrops := uint64(0)
			if hlsStreamer != nil {
				hlsDrops = hlsStreamer.DropCount()
			}
			webrtcDrops := webrtc.AutoplayDropCount()

			log.Printf(
				"cursor stalled for %s (threshold=%s, pos=%s, hlsDrops=%d, webrtcDrops=%d); restarting stream",
				stalledFor.Round(time.Second),
				stall,
				pos,
				hlsDrops,
				webrtcDrops,
			)

			if err := webrtc.RestartAutoplay(); err != nil {
				log.Printf("autoplay restart failed: %v", err)
			}
			if hlsStreamer != nil {
				hlsStreamer.Restart()
			}

			lastRestart = time.Now()
			lastChange = time.Now()
		}
	}()
}

func loadConfigs() error {
	log.Println("Loading " + envFileProd)
	if err := godotenv.Load(envFileProd); err != nil {
		return err
	}

	return nil
}

func main() {
	if err := loadConfigs(); err != nil {
		log.Println("Failed to find config in CWD, changing CWD to executable path")

		exePath, err := os.Executable()
		if err != nil {
			log.Fatal(err)
		}

		if err = os.Chdir(filepath.Dir(exePath)); err != nil {
			log.Fatal(err)
		}

		if err = loadConfigs(); err != nil {
			log.Fatal(err)
		}
	}

	webrtc.Configure()

	ffmpegBin := os.Getenv("FFMPEG_BIN")
	primaryCfg := hls.Config{
		OutputDir:           os.Getenv("HLS_OUTPUT_DIR"),
		FfmpegPath:          ffmpegBin,
		SegmentCacheControl: os.Getenv("HLS_SEGMENT_CACHE_CONTROL"),
		Cursor:              webrtc.AudioCursor(),
	}

	hlsStreamer, err := hls.Start(primaryCfg)
	if err != nil {
		log.Fatal(err)
	}
	webrtc.SetHLSTeeWriter(hlsStreamer.AudioWriter())

	mediaDir := os.Getenv("MEDIA_DIR")
	if err := webrtc.StartAutoplayFromMediaDir(mediaDir); err != nil {
		log.Fatal(err)
	}

	stallTimeout := parseDurationEnv("CURSOR_STALL_TIMEOUT", 10*time.Second)
	startCursorWatchdog(webrtc.AudioCursor(), stallTimeout, hlsStreamer)

	// we don't need this since we're using nginx as a reverse proxy but this is here if anyone isn't.
	httpsRedirectPort := "80"
	if val := os.Getenv("HTTPS_REDIRECT_PORT"); val != "" {
		httpsRedirectPort = val
	}

	if os.Getenv("HTTPS_REDIRECT_PORT") != "" || os.Getenv("ENABLE_HTTP_REDIRECT") != "" {
		go func() {
			redirectServer := &http.Server{
				Addr: ":" + httpsRedirectPort,
				Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					http.Redirect(w, r, "https://"+r.Host+r.URL.String(), http.StatusMovedPermanently)
				}),
			}

			log.Println("Running HTTP->HTTPS redirect Server at :" + httpsRedirectPort)
			log.Fatal(redirectServer.ListenAndServe())
		}()
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/api/whep", corsHandler(whepHandler))
	mux.HandleFunc("/api/status", corsHandler(statusHandler))

	hlsHandler := http.StripPrefix("/api/hls/", hlsStreamer.Handler())
	mux.HandleFunc("/api/hls/", corsHandler(func(w http.ResponseWriter, r *http.Request) {
		hlsHandler.ServeHTTP(w, r)
	}))

	frontendHandler, err := newFrontendHandler()
	if err != nil {
		log.Fatal(err)
	}

	log.Println("Serving frontend assets")

	mux.Handle("/", frontendHandler)

	server := &http.Server{
		Handler: mux,
		Addr:    os.Getenv("HTTP_ADDRESS"),
	}

	tlsKey := os.Getenv("SSL_KEY")
	tlsCert := os.Getenv("SSL_CERT")

	if tlsKey != "" && tlsCert != "" {
		server.TLSConfig = &tls.Config{
			Certificates: []tls.Certificate{},
		}

		cert, err := tls.LoadX509KeyPair(tlsCert, tlsKey)
		if err != nil {
			log.Fatal(err)
		}

		server.TLSConfig.Certificates = append(server.TLSConfig.Certificates, cert)

		log.Println("Running HTTPS Server at " + os.Getenv("HTTP_ADDRESS"))
		log.Fatal(server.ListenAndServeTLS("", ""))
	} else {
		log.Println("Running HTTP Server at " + os.Getenv("HTTP_ADDRESS"))
		log.Fatal(server.ListenAndServe())
	}
}
