<script lang="ts">
    import { onMount } from 'svelte';
    import VolumeControl from './VolumeControl.svelte';
    import { parseLinkHeader } from '@web3-storage/parse-link-header';
    import { API_BASE } from '$lib';

    let audio: HTMLAudioElement | undefined = $state(undefined);
    let audioPaused = $state(false);
    let volume: number = $state(0.3);

    let peerConnection: RTCPeerConnection | null = null;

    async function setupRtc(peerConnection: RTCPeerConnection) {
        peerConnection.ontrack = (e) => {
            if (audio) audio.srcObject = e.streams[0];
        };

        peerConnection.addTransceiver('audio', { direction: 'recvonly' });

        let offer = await peerConnection.createOffer();

        peerConnection
            ?.setLocalDescription(offer)
            .catch((err) => console.error('SetLocalDescription', err));

        // TODO: define the stream key thingie used
        let resp = await fetch(`${API_BASE}/whep`, {
            method: 'POST',
            body: offer.sdp,
            headers: {
                Authorization: `Bearer eggsfm`,
                'Content-Type': 'application/sdp'
            }
        });
        if (resp.status !== 201) {
            throw new DOMException('WHEP endpoint did not return 201');
        }

        const parsedLinkHeader = parseLinkHeader(resp.headers.get('Link'));

        if (parsedLinkHeader === null || parsedLinkHeader === undefined) {
            throw new DOMException('Missing link header');
        }

        const evtSource = new EventSource(
            `${window.location.protocol}//${parsedLinkHeader['urn:ietf:params:whep:ext:core:server-sent-events']?.url}`
        );
        evtSource.onerror = (_) => evtSource.close();

        const answer = await resp.text();
        peerConnection
            ?.setRemoteDescription({
                sdp: answer,
                type: 'answer'
            })
            .catch((err) => console.error('RemoteDescription', err));
    }

    onMount(() => {
        peerConnection = new RTCPeerConnection();

        setupRtc(peerConnection);

        return () => {
            peerConnection?.close();
            peerConnection = null;
        };
    });
</script>

<audio
    bind:this={audio}
    bind:paused={audioPaused}
    {volume}
    autoplay
    playsinline
    class="hidden"></audio>

<div class="window w-full drop-shadow-[8px_8px_0_#000000b0]">
    <div class="title-bar">
        <div class="title-bar-text pl-1">EggsFM - Player</div>
        <div class="title-bar-controls">
            <button aria-label="Minimize" tabindex="-1" aria-hidden="true"
            ></button>
            <button aria-label="Maximize" tabindex="-1" aria-hidden="true"
            ></button>
            <button aria-label="Close" tabindex="-1" aria-hidden="true"
            ></button>
        </div>
    </div>
    <div class="window-body">
        <div class="status-field-border m-2" style="padding: 8px;">
            <p class="text-lg">Now Playing: Penis Music (1000h loop)</p>
        </div>
        <hr class="my-2" />
        <div
            class="flex md:flex-row flex-col md:gap-0 gap-2 justify-between w-full">
            <button onclick={() => (audioPaused = !audioPaused)}>
                <div class="md:py-0 py-2">
                    {audioPaused ? 'Play' : 'Pause'}
                </div>
            </button>
            <VolumeControl bind:volume />
        </div>
    </div>
    <div class="status-bar">
        <p class="status-bar-field">Connection: {'idk'}</p>
        <p class="status-bar-field">fart</p>
        <p class="status-bar-field">Viewers: {'idk'}</p>
    </div>
</div>
