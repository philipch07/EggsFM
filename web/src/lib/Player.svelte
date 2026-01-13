<script lang="ts">
    import { onMount } from 'svelte';
    import { browser } from '$app/environment';
    import VolumeControl from './VolumeControl.svelte';
    import { API_BASE, HLS_MEDIA_PLAYLIST, HLS_PLAYLIST, LISTEN_URL, STATION_NAME } from '$lib';

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
    let playbackMode = $state<'webrtc' | 'hls'>('webrtc');
    let hlsLib: HlsConstructor | null = null;
    let hlsLoader: Promise<HlsConstructor | null> | null = null;
    let hlsInstance: HlsInstance | null = null;
    let hlsErrored = $state(false);
    let hlsCurrentUrl: string | null = null;

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
    let connectionLabel = $derived(playbackMode === 'hls' ? (hlsErrored ? 'hls-error' : 'hls') : connectionState);

    let tuneInUrl = $state<string>(LISTEN_URL);

    function destroyHls() {
        hlsInstance?.destroy();
        hlsInstance = null;
        hlsErrored = false;
        hlsCurrentUrl = null;
        if (audio) {
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
            audio.srcObject = null;
        }
    }

    async function loadHlsLibrary(): Promise<HlsConstructor | null> {
        if (!browser) return null;
        if (hlsLib) return hlsLib;

        const globalHls = (window as any).Hls as HlsConstructor | undefined;
        if (globalHls && typeof globalHls.isSupported === 'function') {
            hlsLib = globalHls;
            return hlsLib;
        }

        if (!hlsLoader) {
            hlsLoader = new Promise((resolve) => {
                const script = document.createElement('script');
                script.src = 'https://cdn.jsdelivr.net/npm/hls.js@1.5.17/dist/hls.min.js';
                script.async = true;
                script.onload = () => {
                    const loaded = (window as any).Hls as HlsConstructor | undefined;
                    resolve(loaded && typeof loaded.isSupported === 'function' ? loaded : null);
                };
                script.onerror = () => resolve(null);
                document.body.appendChild(script);
            });
        }

        hlsLib = await hlsLoader;
        return hlsLib;
    }

    async function startHlsPlayback(reason?: string) {
        if (!audio) return;

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

            destroyHls();
            aud.srcObject = null;
            aud.src = '';

            if (supportsNativeHls) {
                aud.src = src;
                try {
                    await aud.play();
                    audioPaused = false;
                    hlsCurrentUrl = src;
                    return true;
                } catch {
                    audioPaused = true;
                    return false;
                }
            }

            const lib = await loadHlsLibrary();
            if (!lib || !lib.isSupported()) {
                return false;
            }

            let fatalError = false;
            hlsInstance = new lib({
                lowLatencyMode: false,
                enableWorker: true,
                backBufferLength: 60,
                liveSyncDurationCount: 3,
                liveMaxLatencyDurationCount: 6,
                maxLiveSyncPlaybackRate: 1.0
            });
            hlsInstance.on(lib.Events.ERROR, (_evt: string, data: any) => {
                if (data?.fatal) {
                    fatalError = true;
                }
            });

            hlsInstance.loadSource(src);
            hlsInstance.attachMedia(aud);
            hlsInstance.startLoad?.();

            try {
                await aud.play();
                audioPaused = false;
                hlsCurrentUrl = src;
                return !fatalError;
            } catch {
                audioPaused = true;
                destroyHls();
                return false;
            }
        };

        for (const src of buildSources()) {
            const ok = await trySource(src);
            if (ok) {
                hlsCurrentUrl = src;
                connectionState = 'hls';
                hlsErrored = false;
                return;
            }
        }

        hlsErrored = true;
        connectionState = 'hls-error';
        audioPaused = true;
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

        playbackMode = 'webrtc';
        hlsErrored = false;
        destroyHls();

        stopWebrtc();
        peerConnection = new RTCPeerConnection();

        connectionState = 'connecting';

        try {
            await setupRtc(peerConnection);
            return true;
        } catch (err) {
            console.error('webrtc setup failed', err);
            connectionState = 'failed';
            await startHlsPlayback('webrtc negotiation failed');
            return false;
        }
    }

    async function handleModeChange(mode: 'webrtc' | 'hls') {
        const prev = playbackMode;
        playbackMode = mode;
        if (mode === 'webrtc') {
            await startWebrtcPlayback();
        } else {
            await startHlsPlayback(prev === 'hls' ? 'manual switch' : 'mode change');
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

    async function setupRtc(pc: RTCPeerConnection) {
        pc.onconnectionstatechange = () => {
            connectionState = pc.connectionState;
            if (
                playbackMode === 'webrtc' &&
                (pc.connectionState === 'failed' || pc.connectionState === 'disconnected')
            ) {
                fallbackToHls('peer connection lost');
            }
        };

        pc.ontrack = (e) => {
            if (!audio) return;

            audio.src = '';
            audio.srcObject = e.streams[0];

            audio
                .play()
                .then(() => (audioPaused = false))
                .catch(() => (audioPaused = true));
        };

        const transceiver = pc.addTransceiver('audio', {
            direction: 'recvonly'
        });
        if ('jitterBufferTarget' in transceiver.receiver) {
            transceiver.receiver.jitterBufferTarget = 300;
            console.info('Jitter buffer target set to 300');
        }

        const offer = await pc.createOffer();

        pc?.setLocalDescription(offer).catch((err) => console.error('SetLocalDescription', err));

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

    async function togglePlay() {
        if (!audio) return;

        if (audio.paused) {
            if (playbackMode === 'hls' && hlsInstance && hlsInstance.startLoad) {
                hlsInstance.startLoad();
            } else if (playbackMode === 'hls' && !hlsInstance && hlsCurrentUrl && !audio.src) {
                audio.src = hlsCurrentUrl;
            }

            try {
                await audio.play();
                audioPaused = false;
            } catch {
                audioPaused = true;
            }
        } else {
            if (playbackMode === 'hls') {
                hlsInstance?.stopLoad?.();
                if (!hlsInstance && audio.src) {
                    hlsCurrentUrl = audio.src;
                    audio.src = '';
                }
            }
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
                        onchange={(event) =>
                            handleModeChange((event.currentTarget as HTMLSelectElement).value as 'webrtc' | 'hls')}>
                        <option value="webrtc">WebRTC (live)</option>
                        <option value="hls">HLS</option>
                    </select>
                    {#if playbackMode === 'hls'}
                        <span class="text-xs text-gray-600"> HLS (AAC) </span>
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
