<script lang="ts">
    import './layout.css';
    import '98.css';
    import favicon from '$lib/assets/favicon.svg';

    let { children } = $props();
</script>

<svelte:head>
    <link rel="icon" href={favicon} />
    <meta name="darkreader-lock" />
</svelte:head>

<div class="absolute top-1/2 left-1/2 -translate-1/2">
    {@render children()}

    <footer class="mx-auto px-2 pt-6 text-center text-white">
        <a href="https://github.com/philipch07/eggsfm" target="_blank"
            >Github</a>
    </footer>
</div>
