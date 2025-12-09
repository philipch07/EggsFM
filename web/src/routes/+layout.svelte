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

<div class="absolute top-1/2 left-0 -translate-y-1/2 w-full">
    {@render children()}
</div>

<footer
    class="absolute bottom-0 left-1/2 -translate-x-1/2 mx-auto mb-8 text-center text-white">
    <a href="https://github.com/philipch07/eggsfm" target="_blank">RCS</a>
</footer>
