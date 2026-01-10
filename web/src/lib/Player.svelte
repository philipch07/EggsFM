<script lang="ts">
    import { onMount } from 'svelte';
    import { browser } from '$app/environment';
    import VolumeControl from './VolumeControl.svelte';
    import { API_BASE, LISTEN_URL, STATION_NAME } from '$lib';

    import qrPng from '$lib/assets/xfm-qr.png';

    const VOLUME_KEY = 'eggsfm_volume_v1';
    const DEFAULT_VOLUME = 0.2;

    function clamp01(v: number) {
        if (!Number.isFinite(v)) return DEFAULT_VOLUME;
        if (v < 0) return 0;
        if (v > 1) return DEFAULT_VOLUME;
        return v;
    }

    function loadSavedVolume(): number {
        if (!browser) return DEFAULT_VOLUME;
        try {
            const raw = localStorage.getItem(VOLUME_KEY);
            if (raw == null) return DEFAULT_VOLUME;
            return clamp01(parseFloat(raw));
        } catch {
            return DEFAULT_VOLUME;
        }
    }

    let audio: HTMLAudioElement | undefined = $state(undefined);
    let audioPaused = $state(true);

    // init from localStorage (or default)
    let volume: number = $state(loadSavedVolume());

    let peerConnection: RTCPeerConnection | null = null;

    let connectionState = $state<string>('new');
    let listeners = $state<number | null>(null);

    let nowPlaying = $state<string>('-');
    let artists = $state<string[]>([]);

    let bweKbps = $state<number | null>(null);

    let titleBarText = $derived(
        artists.length ? `${artists.join(', ')}` : STATION_NAME
    );

    let tuneInUrl = $state<string>(LISTEN_URL);

    // UI state
    let minimized = $state(false);
    let showQr = $state(false);

    // persist volume
    $effect(() => {
        if (!browser) return;
        try {
            localStorage.setItem(VOLUME_KEY, String(clamp01(volume)));
        } catch {
            // ignore
        }
    });

    // keep audio element in sync
    $effect(() => {
        if (!audio) return;
        audio.volume = clamp01(volume);
    });

    async function setupRtc(pc: RTCPeerConnection) {
        pc.onconnectionstatechange = () => {
            connectionState = pc.connectionState;
        };

        pc.ontrack = (e) => {
            if (!audio) return;

            audio.srcObject = e.streams[0];

            audio
                .play()
                .then(() => (audioPaused = false))
                .catch(() => (audioPaused = true));
        };

        const transceiver = pc.addTransceiver('audio', { direction: 'recvonly' });
        if ('jitterBufferTarget' in transceiver.receiver) {
            transceiver.receiver.jitterBufferTarget = 300;
            console.info('Jitter buffer target set to 300');
        }

        const offer = await pc.createOffer();

        pc?.setLocalDescription(offer).catch((err) =>
            console.error('SetLocalDescription', err)
        );

        const resp = await fetch(`${API_BASE}/whep`, {
            method: 'POST',
            body: offer.sdp,
            headers: { 'Content-Type': 'application/sdp' }
        });

        if (resp.status !== 201) {
            throw new DOMException('WHEP endpoint did not return 201');
        }

        const answer = await resp.text();
        pc?.setRemoteDescription({
            sdp: answer,
            type: 'answer'
        }).catch((err) => console.error('RemoteDescription', err));
    }

    async function refreshStatus() {
        try {
            const resp = await fetch(`${API_BASE}/status`, { method: 'GET' });
            if (!resp.ok) return;

            const data: Array<{
                listenerCount: number;
                nowPlaying?: string;
                artists?: string[] | null;
            }> = await resp.json();

            const row = data?.[0];
            listeners = row?.listenerCount ?? 0;
            nowPlaying = row?.nowPlaying ?? '-';
            artists = row?.artists ?? [];
        } catch (e) {
            console.log('status fetch error:', e);
        }
    }

    async function refreshBwe() {
        if (!peerConnection) return;

        try {
            const stats = await peerConnection.getStats();
            let bps: number | null = null;

            stats.forEach((r) => {
                const anyR: any = r;
                if (
                    r.type === 'candidate-pair' &&
                    anyR.state === 'succeeded' &&
                    (anyR.selected === true || anyR.nominated === true)
                ) {
                    bps =
                        anyR.availableIncomingBitrate ??
                        anyR.availableOutgoingBitrate ??
                        null;
                }
            });

            bweKbps = bps != null ? Math.round(bps / 1000) : null;
        } catch {
            // ignore
        }
    }

    async function togglePlay() {
        if (!audio) return;

        if (audio.paused) {
            try {
                await audio.play();
                audioPaused = false;
            } catch {
                audioPaused = true;
            }
        } else {
            audio.pause();
            audioPaused = true;
        }
    }

    function toggleMinimize() {
        minimized = !minimized;
        if (minimized) showQr = false;
    }

    function openQr() {
        showQr = true;
        minimized = false;

        const onKeyDown = (e: KeyboardEvent) => {
            const isSpace =
                e.key === ' ' || e.code === 'Space' || e.key === 'Spacebar';
            if (isSpace || e.key === 'Escape') {
                e.preventDefault();
                showQr = false;
            }
            window.removeEventListener('keydown', onKeyDown);
        };

        const onClick = (e: MouseEvent) => {
            showQr = false;
            window.removeEventListener('click', onClick);
        };

        window.addEventListener('keydown', onKeyDown);
        window.addEventListener('click', onClick);
    }

    function closeTab() {
        try {
            window.close();
            setTimeout(() => {
                try {
                    window.close();
                } catch {
                    // ignore
                }
            }, 50);
        } catch {
            // ignore
        }
    }

    onMount(() => {
        if (!tuneInUrl && browser) {
            tuneInUrl = window.location.origin;
        }

        peerConnection = new RTCPeerConnection();
        setupRtc(peerConnection);

        refreshStatus();
        const statusTimer = setInterval(refreshStatus, 5000);

        refreshBwe();
        const bweTimer = setInterval(refreshBwe, 1000);

        return () => {
            clearInterval(statusTimer);
            clearInterval(bweTimer);
            peerConnection?.close();
            peerConnection = null;
        };
    });
</script>

<audio bind:this={audio} autoplay playsinline class="hidden"></audio>

{#if showQr}
    <div
        class="qr-overlay"
        role="button"
        tabindex="0"
        aria-label="Close QR code">
        <div
            class="qr-box"
            role="dialog"
            aria-modal="true"
            aria-label="QR code">
            <div class="qr-png">
                <img class="qr-img" src={qrPng} alt="QR code" />
            </div>
            {#if tuneInUrl}
                <div class="qr-link">
                    <a href={tuneInUrl} target="_blank" rel="noreferrer"
                        >{tuneInUrl}</a>
                </div>
            {/if}
        </div>
    </div>
{/if}

<div class="window w-full drop-shadow-[8px_8px_0_#000000b0]">
    <div class="title-bar">
        <div class="title-bar-text pl-1">{titleBarText}</div>
        <div class="title-bar-controls">
            <button aria-label="Minimize" type="button" onclick={toggleMinimize}
            ></button>
            <button
                aria-label="Maximize"
                type="button"
                onclick={(e) => {
                    e.stopPropagation();
                    openQr();
                }}></button>
            <button aria-label="Close" type="button" onclick={closeTab}
            ></button>
        </div>
    </div>

    {#if !minimized}
        <div class="window-body">
            <div class="status-field-border m-2" style="padding: 8px;">
                <p class="text-lg">Now Playing: {nowPlaying}</p>
            </div>

            <hr class="my-2" />

            <div
                class="flex w-full flex-col justify-between gap-2 md:flex-row md:gap-0">
                <button type="button" onclick={togglePlay}>
                    <div class="py-2 md:py-0">
                        {audioPaused ? 'Play' : 'Pause'}
                    </div>
                </button>
                <VolumeControl bind:volume />
            </div>
        </div>

        <div class="status-bar">
            <p class="status-bar-field">Connection: {connectionState}</p>
            <p class="status-bar-field">Listeners: {listeners ?? '-'}</p>
            <p class="status-bar-field">
                {bweKbps === null ? '- kb/s' : `${bweKbps} kb/s`}
            </p>
        </div>
    {/if}
</div>

<style>
    .qr-overlay {
        position: fixed;
        inset: 0;
        z-index: 99999;

        display: flex;
        align-items: center;
        justify-content: center;

        /* no selection box / highlight */
        user-select: none;
        outline: none;
        -webkit-tap-highlight-color: transparent;
    }

    .qr-overlay:focus {
        outline: none;
    }

    .qr-box {
        width: min(90vmin, 640px);
        height: min(90vmin, 640px);
        background: white;
        padding: 12px;
        overflow: hidden;
        box-shadow: 0 12px 35px rgba(0, 0, 0, 0.35);
    }

    .qr-png {
        width: 100%;
        height: 100%;
    }

    .qr-img {
        width: 100%;
        height: 100%;
        display: block;
        object-fit: contain;
        max-width: 100%;
        max-height: 100%;
    }

    .qr-link {
        text-align: center;
        margin-top: 10px;
        font-weight: 700;
        word-break: break-all;
    }
</style>
