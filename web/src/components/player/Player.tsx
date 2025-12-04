import React, { useEffect, useRef, useState } from 'react';
import { parseLinkHeader } from '@web3-storage/parse-link-header';
import VolumeComponent from './components/VolumeComponent';
import CurrentViewersComponent from './components/CurrentViewersComponent';

interface PlayerProps {
  streamKey: string;
  onCloseStream?: () => void;
}

const Player = (props: PlayerProps) => {
  const apiPath = import.meta.env.VITE_API_PATH;
  const { streamKey } = props;

  const [videoLayers, setVideoLayers] = useState([]);
  const [hasSignal, setHasSignal] = useState<boolean>(false);
  const [hasPacketLoss, setHasPacketLoss] = useState<boolean>(false);
  const [connectFailed, setConnectFailed] = useState<boolean>(false);
  const [isPlaying, setisPlaying] = useState<boolean>(false); // FIXME: show right status on load

  const audioRef = useRef<HTMLAudioElement>(null);
  const layerEndpointRef = useRef<string>('');
  const hasSignalRef = useRef<boolean>(false);
  const peerConnectionRef = useRef<RTCPeerConnection | null>(null);
  const streamVideoPlayerId = streamKey + '_videoPlayer';

  const setHasSignalHandler = (_: Event) => {
    setHasSignal(() => true);
  };

  useEffect(() => {
    const handleWindowBeforeUnload = () => {
      peerConnectionRef.current?.close();
      peerConnectionRef.current = null;
    };

    window.addEventListener('beforeunload', handleWindowBeforeUnload);

    peerConnectionRef.current = new RTCPeerConnection();

    return () => {
      peerConnectionRef.current?.close();
      peerConnectionRef.current = null;

      audioRef.current?.removeEventListener('playing', setHasSignalHandler);

      window.removeEventListener('beforeunload', handleWindowBeforeUnload);
    };
  }, []);

  useEffect(() => {
    hasSignalRef.current = hasSignal;

    const intervalHandler = () => {
      if (!peerConnectionRef.current) {
        return;
      }

      let receiversHasPacketLoss = false;
      peerConnectionRef.current.getReceivers().forEach((receiver) => {
        if (receiver) {
          receiver.getStats().then((stats) => {
            stats.forEach((report) => {
              if (report.type === 'inbound-rtp') {
                const lossRate =
                  report.packetsLost /
                  (report.packetsLost + report.packetsReceived);
                receiversHasPacketLoss = receiversHasPacketLoss
                  ? true
                  : lossRate > 5;
              }
            });
          });
        }
      });

      setHasPacketLoss(() => receiversHasPacketLoss);
    };

    const interval = setInterval(intervalHandler, hasSignal ? 15_000 : 2_500);

    return () => clearInterval(interval);
  }, [hasSignal]);

  useEffect(() => {
    if (!peerConnectionRef.current) {
      return;
    }

    peerConnectionRef.current.ontrack = (event: RTCTrackEvent) => {
      if (audioRef.current) {
        audioRef.current.srcObject = event.streams[0];
        audioRef.current.addEventListener('playing', setHasSignalHandler);
      }
    };

    peerConnectionRef.current.addTransceiver('audio', {
      direction: 'recvonly',
    });

    peerConnectionRef.current.createOffer().then((offer) => {
      offer['sdp'] = offer['sdp']!.replace(
        'useinbandfec=1',
        'useinbandfec=1;stereo=1',
      );

      peerConnectionRef
        .current!.setLocalDescription(offer)
        .catch((err) => console.error('SetLocalDescription', err));

      fetch(`${apiPath}/whep`, {
        method: 'POST',
        body: offer.sdp,
        headers: {
          Authorization: `Bearer ${streamKey}`,
          'Content-Type': 'application/sdp',
        },
      })
        .then((r) => {
          setConnectFailed(r.status !== 201);
          if (connectFailed) {
            throw new DOMException('WHEP endpoint did not return 201');
          }

          const parsedLinkHeader = parseLinkHeader(r.headers.get('Link'));

          if (parsedLinkHeader === null || parsedLinkHeader === undefined) {
            throw new DOMException('Missing link header');
          }

          layerEndpointRef.current = `${window.location.protocol}//${parsedLinkHeader['urn:ietf:params:whep:ext:core:layer'].url}`;

          const evtSource = new EventSource(
            `${window.location.protocol}//${parsedLinkHeader['urn:ietf:params:whep:ext:core:server-sent-events'].url}`,
          );
          evtSource.onerror = (_) => evtSource.close();

          evtSource.addEventListener('layers', (event) => {
            const parsed = JSON.parse(event.data);
            setVideoLayers(() =>
              parsed['1']['layers'].map((layer: any) => layer.encodingId),
            );
          });

          return r.text();
        })
        .then((answer) => {
          peerConnectionRef
            .current!.setRemoteDescription({
              sdp: answer,
              type: 'answer',
            })
            .catch((err) => console.error('RemoteDescription', err));
        })
        .catch((err) => console.error('PeerConnectionError', err));
    });
  }, [peerConnectionRef]);

  const playPause = () => {
    if (audioRef.current?.paused) {
      audioRef.current.play();
      setisPlaying(true);
    } else {
      audioRef.current?.pause();
      setisPlaying(false);
    }
  };

  return (
    <div id={streamVideoPlayerId} className='md:mx-auto px-2 md:px-0'>
      {connectFailed && (
        <p className='bg-red-700 text-white text-lg text-center p-4 rounded-t-lg whitespace-pre-wrap'>
          Failed to start EggsFM session 🥚{' '}
        </p>
      )}
      {audioRef.current !== null && (
        <div className='window md:w-[750px] w-full'>
          <div className='title-bar'>
            <div className='title-bar-text'>EggsFM - Player</div>
            <div className='title-bar-controls'>
              <button aria-label='Minimize'></button>
              <button aria-label='Maximize'></button>
              <button aria-label='Close'></button>
            </div>
          </div>
          <div className='window-body'>
            <div className='status-field-border m-2' style={{ padding: '8px' }}>
              <p className='text-lg'>Now Playing: Penis Music (1000h loop)</p>
            </div>
            <hr className='my-2' />
            <div className='flex flex-row justify-between w-full'>
              <button
                onClick={playPause}
                className=''
                disabled={!audioRef.current}
              >
                {isPlaying ? 'Pause' : 'Play'}
              </button>
              <VolumeComponent
                isMuted={audioRef.current?.muted ?? false}
                onVolumeChanged={(newValue) =>
                  (audioRef.current!.volume = newValue)
                }
                onStateChanged={(newState) =>
                  (audioRef.current!.muted = newState)
                }
              />
            </div>
          </div>
          <div className='status-bar'>
            <p className='status-bar-field'>Press Ctrl+W for help</p>
            <p className='status-bar-field'>fart</p>
            <CurrentViewersComponent streamKey={streamKey} />
          </div>
        </div>
      )}

      <audio
        ref={audioRef}
        autoPlay
        playsInline
        className='bg-transparent rounded-md w-full h-full relative'
      />
    </div>
  );
};

export default Player;
