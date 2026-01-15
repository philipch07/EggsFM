import { browser, dev } from '$app/environment';

function normalizeBase(base?: string | null) {
    if (!base) return null;
    const trimmed = base.trim();
    if (!trimmed) return null;

    const withoutTrailing = trimmed.replace(/\/+$/, '');
    if (/^https?:\/\//i.test(withoutTrailing) || withoutTrailing.startsWith('/')) {
        return withoutTrailing;
    }

    return `/${withoutTrailing}`;
}

const fallbackApiBase = normalizeBase(dev ? 'http://localhost:8080/api' : '/api')!;
const configuredApiBase =
    normalizeBase(import.meta.env.VITE_API_BASE ?? import.meta.env.VITE_API_PATH) ?? fallbackApiBase;

export const API_BASE = configuredApiBase;
export const STATION_NAME = (import.meta.env.VITE_STATION_NAME as string | undefined)?.trim() || 'EggsFM';
const pageTitle = (import.meta.env.VITE_PAGE_TITLE as string | undefined)?.trim();
export const PAGE_TITLE = pageTitle || STATION_NAME;
export const EMBED_DESCRIPTION = (import.meta.env.VITE_EMBED_DESCRIPTION as string | undefined)?.trim() || 'Jack in.';
export const LISTEN_URL =
    (import.meta.env.VITE_LISTEN_URL as string | undefined)?.trim() || (browser ? window.location.origin : '');
export const HLS_PLAYLIST = `${API_BASE}/hls/master.m3u8`;
export const HLS_MEDIA_PLAYLIST = `${API_BASE}/hls/live.m3u8`;
export const ICECAST_STREAM = `${API_BASE}/icecast.mp3`;
export const ICECAST_PLAYLIST = `${API_BASE}/icecast.m3u8`;
