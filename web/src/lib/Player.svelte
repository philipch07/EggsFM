<script lang="ts">
    import { onMount } from 'svelte';
    import VolumeControl from './VolumeControl.svelte';
    import { parseLinkHeader } from '@web3-storage/parse-link-header';
    import { API_BASE } from '$lib';

    let audio: HTMLAudioElement | undefined = $state(undefined);
    let audioPaused = $state(false);
    let volume: number = $state(0.5);

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

<div class="window w-full">
    <div class="title-bar">
        <div class="title-bar-text">EggsFM - Player</div>
        <div class="title-bar-controls">
            <button aria-label="Minimize"></button>
            <button aria-label="Maximize"></button>
            <button aria-label="Close"></button>
        </div>
    </div>
    <div class="window-body">
        <div class="status-field-border m-2" style="padding: 8px;">
            <p class="text-lg">Now Playing: Penis Music (1000h loop)</p>
        </div>
        <hr class="my-2" />
        <div class="flex flex-row justify-between w-full">
            <button onclick={() => (audioPaused = !audioPaused)} class="">
                {audioPaused ? 'Play' : 'Pause'}
            </button>
            <VolumeControl bind:volume />
        </div>
    </div>
    <div class="status-bar">
        <p class="status-bar-field">Press Ctrl+W for help</p>
        <p class="status-bar-field">fart</p>
        <p class="status-bar-field">Viewers: idk the api is weird</p>
    </div>
</div>
