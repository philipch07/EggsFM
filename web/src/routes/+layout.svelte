<script lang="ts">
    import './layout.css';
    import '98.css';
    import favicon from '$lib/assets/xfm.ico';
    import { PAGE_TITLE } from '$lib';

    let { children } = $props();
</script>

<svelte:head>
    <title>{PAGE_TITLE}</title>
    <link rel="icon" href={favicon} />
    <meta name="darkreader-lock" />
</svelte:head>

<div class="absolute top-1/2 left-0 w-full -translate-y-1/2">
    {@render children()}
</div>

<footer
    class="absolute bottom-0 left-1/2 mx-auto mb-8 -translate-x-1/2 text-center text-white">
    <a href="https://github.com/philipch07/eggsfm" target="_blank">RCS</a>
</footer>
