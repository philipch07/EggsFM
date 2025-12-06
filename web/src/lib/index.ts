import { dev } from '$app/environment';

// place files you want to import through the `$lib` alias in this folder.
export const API_BASE = dev ? 'http://localhost:8080/api' : '/api';
