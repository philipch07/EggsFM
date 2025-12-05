<script lang="ts">
    import './layout.css';
    import '98.css';
    import favicon from '$lib/assets/favicon.svg';
    import Background from '$lib/Background.svelte';

    let { children } = $props();
</script>

<svelte:head>
    <link rel="icon" href={favicon} />
    <meta name="darkreader-lock" />
</svelte:head>

<Background metas={['Artist - Song', 'Album - Epic Album 2', 'Year - 1984']} />

<div
    class="absolute top-1/2 left-1/2 -translate-1/2 p-8 backdrop-blur-md bg-white/5 rounded-3xl border border-white/5">
    {@render children()}

    <footer class="mx-auto px-2 pt-6 text-center text-white">
        <p>Welcome to Eggs FM</p>
        <a href="https://github.com/philipch07/eggsfm" target="_blank"
            >Github</a>
    </footer>
</div>
