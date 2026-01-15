<script lang="ts">
    import { onMount } from 'svelte';
    import { browser } from '$app/environment';
    import VolumeControl from './VolumeControl.svelte';
    import {
        API_BASE,
        HLS_MEDIA_PLAYLIST,
        HLS_PLAYLIST,
        ICECAST_PLAYLIST,
        ICECAST_STREAM,
        LISTEN_URL,
        STATION_NAME
    } from '$lib';

    import qrPng from '$lib/assets/xfm-qr.png';

    const VOLUME_KEY = 'eggsfm_volume_v1';
    const DEFAULT_VOLUME = 0.2;

    type HlsConstructor = {
        new (opts?: Record<string, unknown>): HlsInstance;
        isSupported: () => boolean;
        Events: Record<string, string>;
    };

    type HlsInstance = {
        attachMedia(media: HTMLMediaElement): void;
        destroy(): void;
        loadSource(src: string): void;
        on(event: string, cb: (...args: any[]) => void): void;
        startLoad?: () => void;
        stopLoad?: () => void;
    };

    const HLS_SOURCES = [HLS_PLAYLIST, HLS_MEDIA_PLAYLIST].filter(Boolean);

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
    let playbackMode = $state<'webrtc' | 'hls' | 'icecast'>('webrtc');
    let hlsLib: HlsConstructor | null = null;
    let hlsLoader: Promise<HlsConstructor | null> | null = null;
    let hlsInstance: HlsInstance | null = null;
    let hlsErrored = $state(false);
    let hlsCurrentUrl: string | null = null;
    let playIntent = $state(true);
    let playAbort = new AbortController();
    let modeAbort = new AbortController();

    const canUseWebrtc = browser && typeof RTCPeerConnection !== 'undefined';

    // init from localStorage (or default)
    let volume: number = $state(loadSavedVolume());

    let peerConnection: RTCPeerConnection | null = null;

    let connectionState = $state<string>('new');
    let listeners = $state<number | null>(null);

    let nowPlaying = $state<string>('-');
    let artists = $state<string[]>([]);

    let bweKbps = $state<number | null>(null);

    let titleBarText = $derived(artists.length ? `${artists.join(', ')}` : STATION_NAME);
    let connectionLabel = $derived(
        playbackMode === 'hls'
            ? hlsErrored
                ? 'hls-error'
                : 'hls'
            : playbackMode === 'icecast'
              ? 'icecast'
              : connectionState
    );

    let tuneInUrl = $state<string>(LISTEN_URL);

    function nextPlaySignal() {
        playAbort.abort();
        playAbort = new AbortController();
        return playAbort.signal;
    }

    function nextModeSignal() {
        modeAbort.abort();
        modeAbort = new AbortController();
        return modeAbort.signal;
    }

    function getPlayErrorName(err: unknown) {
        if (!err || typeof err !== 'object') return null;
        return (err as { name?: string }).name ?? null;
    }

    async function applyPlayState(shouldPlay: boolean, signal: AbortSignal) {
        if (!audio || signal.aborted) return;
        const aud = audio;

        if (shouldPlay) {
            if (playbackMode === 'hls') {
                hlsInstance?.startLoad?.();

                if (!hlsInstance && hlsCurrentUrl && !aud.src) {
                    aud.src = hlsCurrentUrl;
                }
            }

            const hasMedia = Boolean(aud.srcObject) || Boolean(aud.src) || Boolean(hlsInstance);
            if (!hasMedia) {
                return;
            }

            try {
                await aud.play();
            } catch (err) {
                if (signal.aborted) return;
                const name = getPlayErrorName(err);
                switch (name) {
                    case 'AbortError':
                        return;
                    case 'NotAllowedError':
                        console.warn('playback start failed', err);
                        break;
                    default:
                        console.error('playback start failed', err);
                }

                playIntent = false;
            }
        } else {
            aud.pause();
        }
    }

    function requestPlayState(shouldPlay: boolean) {
        playIntent = shouldPlay;
        const signal = nextPlaySignal();
        void applyPlayState(shouldPlay, signal);
    }

    function destroyHls() {
        hlsInstance?.destroy();
        hlsInstance = null;
        hlsErrored = false;
        hlsCurrentUrl = null;
        if (audio) {
            audio.pause();
            audio.src = '';
        }
    }

    function stopWebrtc() {
        if (peerConnection) {
            peerConnection.onconnectionstatechange = null;
            peerConnection.ontrack = null;
            peerConnection.close();
        }
        peerConnection = null;
        bweKbps = null;
        if (audio) {
            audio.pause();
            audio.srcObject = null;
        }
    }

    async function loadHlsLibrary(): Promise<HlsConstructor | null> {
        if (!browser) return null;
        if (hlsLib) return hlsLib;

        if (!hlsLoader) {
            hlsLoader = import('hls.js')
                .then((mod) => {
                    const Hls = mod.default;
                    return Hls && typeof Hls.isSupported === 'function' ? (Hls as HlsConstructor) : null;
                })
                .catch(() => null);
        }

        hlsLib = await hlsLoader;
        return hlsLib;
    }

    async function startHlsPlayback(reason?: string) {
        if (!audio) return;
        const signal = nextModeSignal();

        playbackMode = 'hls';
        hlsErrored = false;
        bweKbps = null;
        connectionState = 'hls';

        destroyHls();
        stopWebrtc();

        const aud = audio;
        aud.srcObject = null;

        const supportsNativeHls = aud.canPlayType('application/vnd.apple.mpegurl') || aud.canPlayType('audio/mpegurl');

        const buildSources = () => [...HLS_SOURCES];

        const trySource = async (src: string) => {
            if (!src) return false;
            if (signal.aborted) return false;

            destroyHls();
            aud.srcObject = null;
            aud.src = '';

            if (supportsNativeHls) {
                aud.src = src;
                hlsCurrentUrl = src;
                return true;
            }

            const lib = await loadHlsLibrary();
            if (signal.aborted) return false;
            if (!lib || !lib.isSupported()) {
                return false;
            }

            hlsInstance = new lib({
                lowLatencyMode: false,
                enableWorker: true,
                backBufferLength: 60,
                liveSyncDurationCount: 3,
                liveMaxLatencyDurationCount: 6,
                maxLiveSyncPlaybackRate: 1.0
            });
            hlsInstance.on(lib.Events.ERROR, (_evt: string, data: any) => {
                if (signal.aborted || !data?.fatal) return;

                hlsErrored = true;
                connectionState = 'hls-error';
            });

            hlsInstance.loadSource(src);
            hlsInstance.attachMedia(aud);
            hlsInstance.startLoad?.();
            hlsCurrentUrl = src;

            return true;
        };

        for (const src of buildSources()) {
            const ok = await trySource(src);
            if (signal.aborted) return;

            if (ok) {
                connectionState = 'hls';
                hlsErrored = false;
                await applyPlayState(playIntent, playAbort.signal);

                return;
            }
        }

        if (signal.aborted) return;

        hlsErrored = true;
        connectionState = 'hls-error';
    }

    async function resolveIcecastStream(signal: AbortSignal): Promise<string> {
        const playlistUrl = ICECAST_PLAYLIST.startsWith('http')
            ? ICECAST_PLAYLIST
            : new URL(ICECAST_PLAYLIST, window.location.origin).toString();

        try {
            const resp = await fetch(playlistUrl, {
                method: 'GET',
                cache: 'no-store',
                signal
            });
            if (!resp.ok) {
                return ICECAST_STREAM;
            }
            const body = await resp.text();
            if (signal.aborted) return ICECAST_STREAM;

            const line = body
                .split(/\r?\n/)
                .map((entry) => entry.trim())
                .find((entry) => entry && !entry.startsWith('#'));

            if (!line) {
                return ICECAST_STREAM;
            }

            try {
                return new URL(line, playlistUrl).toString();
            } catch {
                return line;
            }
        } catch {
            return ICECAST_STREAM;
        }
    }

    async function startIcecastPlayback() {
        if (!audio) return;
        const signal = nextModeSignal();

        playbackMode = 'icecast';
        hlsErrored = false;
        bweKbps = null;
        connectionState = 'icecast';

        destroyHls();
        stopWebrtc();

        const aud = audio;
        aud.srcObject = null;

        const streamUrl = await resolveIcecastStream(signal);
        if (signal.aborted) return;
        aud.src = streamUrl;

        if (signal.aborted) return;
        await applyPlayState(playIntent, playAbort.signal);
    }

    function fallbackToHls(reason: string) {
        if (playbackMode === 'hls') return;
        console.info('Falling back to HLS:', reason);
        startHlsPlayback(reason);
    }

    async function startWebrtcPlayback() {
        if (!canUseWebrtc) {
            return startHlsPlayback('webrtc unsupported');
        }
        const signal = nextModeSignal();

        playbackMode = 'webrtc';
        hlsErrored = false;
        destroyHls();

        stopWebrtc();
        peerConnection = new RTCPeerConnection();

        connectionState = 'connecting';

        try {
            await setupRtc(peerConnection, signal);
            if (signal.aborted) return;
        } catch (err) {
            if (signal.aborted) return;
            if (getPlayErrorName(err) === 'AbortError') return;

            console.error('webrtc setup failed', err);
            connectionState = 'failed';
            await startHlsPlayback('webrtc negotiation failed');
            return false;
        }
    }

    async function handleModeChange(mode: 'webrtc' | 'hls' | 'icecast') {
        if (mode === 'webrtc') {
            await startWebrtcPlayback();
        } else if (mode === 'hls') {
            await startHlsPlayback('manual switch');
        } else {
            await startIcecastPlayback('manual switch');
        }
    }

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

    async function setupRtc(pc: RTCPeerConnection, signal: AbortSignal) {
        if (signal.aborted) return;
        pc.onconnectionstatechange = () => {
            if (signal.aborted) return;
            connectionState = pc.connectionState;
            if (
                playbackMode === 'webrtc' &&
                (pc.connectionState === 'failed' || pc.connectionState === 'disconnected')
            ) {
                fallbackToHls('peer connection lost');
            }
        };

        pc.ontrack = (e) => {
            if (signal.aborted || !audio) return;

            audio.src = '';
            audio.srcObject = e.streams[0];

            void applyPlayState(playIntent, playAbort.signal);
        };

        const transceiver = pc.addTransceiver('audio', {
            direction: 'recvonly'
        });
        if ('jitterBufferTarget' in transceiver.receiver) {
            transceiver.receiver.jitterBufferTarget = 300;
            console.info('Jitter buffer target set to 300');
        }

        const offer = await pc.createOffer();
        if (signal.aborted) return;

        pc?.setLocalDescription(offer).catch((err) => console.error('SetLocalDescription', err));

        const resp = await fetch(`${API_BASE}/whep`, {
            method: 'POST',
            body: offer.sdp,
            headers: { 'Content-Type': 'application/sdp' },
            signal
        });
        if (signal.aborted) return;

        if (resp.status !== 201) {
            throw new DOMException('WHEP endpoint did not return 201');
        }

        const answer = await resp.text();
        if (signal.aborted) return;
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
        if (playbackMode !== 'webrtc' || !peerConnection) {
            bweKbps = null;
            return;
        }

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
                    bps = anyR.availableIncomingBitrate ?? anyR.availableOutgoingBitrate ?? null;
                }
            });

            bweKbps = bps != null ? Math.round(bps / 1000) : null;
        } catch {
            // ignore
        }
    }

    function togglePlay() {
        requestPlayState(audioPaused);
    }

    function toggleMinimize() {
        minimized = !minimized;
        if (minimized) showQr = false;
    }

    function openQr() {
        showQr = true;
        minimized = false;

        const onKeyDown = (e: KeyboardEvent) => {
            const isSpace = e.key === ' ' || e.code === 'Space' || e.key === 'Spacebar';
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

        startWebrtcPlayback();

        refreshStatus();
        const statusTimer = setInterval(refreshStatus, 5000);

        refreshBwe();
        const bweTimer = setInterval(refreshBwe, 1000);

        return () => {
            clearInterval(statusTimer);
            clearInterval(bweTimer);
            modeAbort.abort();
            playAbort.abort();
            stopWebrtc();
            destroyHls();
        };
    });
</script>

<audio
    bind:this={audio}
    autoplay
    playsinline
    class="hidden"
    onplay={() => (audioPaused = false)}
    onpause={() => (audioPaused = true)}></audio>

{#if showQr}
    <div class="qr-overlay" role="button" tabindex="0" aria-label="Close QR code">
        <div class="qr-box" role="dialog" aria-modal="true" aria-label="QR code">
            <div class="qr-png">
                <img class="qr-img" src={qrPng} alt="QR code" />
            </div>
            {#if tuneInUrl}
                <div class="qr-link">
                    <a href={tuneInUrl} target="_blank" rel="noreferrer">{tuneInUrl}</a>
                </div>
            {/if}
        </div>
    </div>
{/if}

<div class="window w-full drop-shadow-[8px_8px_0_#000000b0]">
    <div class="title-bar">
        <div class="title-bar-text pl-1">{titleBarText}</div>
        <div class="title-bar-controls">
            <button aria-label="Minimize" type="button" onclick={toggleMinimize}></button>
            <button
                aria-label="Maximize"
                type="button"
                onclick={(e) => {
                    e.stopPropagation();
                    openQr();
                }}></button>
            <button aria-label="Close" type="button" onclick={closeTab}></button>
        </div>
    </div>

    {#if !minimized}
        <div class="window-body">
            <div class="status-field-border m-2" style="padding: 8px;">
                <p class="text-lg">Now Playing: {nowPlaying}</p>
                <div class="mt-2 flex flex-col gap-1 md:flex-row md:items-center md:gap-2">
                    <label for="playback-mode" class="text-sm font-bold tracking-tight uppercase"> Stream Mode </label>
                    <select
                        id="playback-mode"
                        bind:value={playbackMode}
                        onchange={() => handleModeChange(playbackMode)}>
                        <option value="webrtc">WebRTC (live)</option>
                        <option value="hls">HLS</option>
                        <option value="icecast">Icecast (MP3)</option>
                    </select>
                    {#if playbackMode === 'hls'}
                        <span class="text-xs text-gray-600"> HLS (AAC) </span>
                    {:else if playbackMode === 'icecast'}
                        <span class="text-xs text-gray-600"> Icecast (MP3) </span>
                    {/if}
                </div>
                {#if hlsErrored}
                    <p class="mt-1 text-xs font-semibold text-red-700">HLS unavailable; retry WebRTC or refresh.</p>
                {/if}
            </div>

            <hr class="my-2" />

            <div class="flex w-full flex-col justify-between gap-2 md:flex-row md:gap-0">
                <button type="button" onclick={togglePlay}>
                    <div class="py-2 md:py-0">
                        {audioPaused ? 'Play' : 'Pause'}
                    </div>
                </button>
                <VolumeControl bind:volume />
            </div>
        </div>

        <div class="status-bar">
            <p class="status-bar-field">Connection: {connectionLabel}</p>
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
