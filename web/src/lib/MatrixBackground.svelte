<script lang="ts">
    import { onMount } from 'svelte';

    interface Props {
        metas: string[];
    }

    type Metadata = {
        text: string;
        x: number;
        y: number;
        alpha: number;
    };

    const stuff: Metadata[] = [];

    const { metas }: Props = $props();

    let wah: Metadata[] = $state([]);

    onMount(() => {
        let counter = 0;
        const interval = setInterval(() => {
            if (document.hidden) return;
            wah = wah.filter((w) => w.y < 2);
            if (counter % 5 == 0) {
                wah.push({
                    text: metas[Math.floor(Math.random() * metas.length)],
                    x: Math.random(),
                    y: -(Math.random() + 0.5),
                    alpha: Math.pow(Math.random() / 2 + 0.5, 2)
                });
            }
            wah.forEach((v) => (v.y += 0.005 * v.alpha));
            counter += 1;
        }, 33.3);

        () => {
            clearInterval(interval);
        };
    });
</script>

{#snippet info({ text, x, y, alpha }: Metadata)}
    {@const mid = `rgb(0, ${alpha * 255}, 0)`}
    {@const tip = `rgb(${alpha * 255}, 255.0, ${alpha * 255})`}
    <div
        style={`left: ${x * 100}vw; top: ${y * 100}vh; background-image: linear-gradient(#020, ${mid}, ${tip})`}
        class="vertical-text WAHHHH glow absolute overflow-hidden text-lg text-nowrap text-white [text-rendering:optimizeSpeed] text-shadow-green-500">
        {text}
    </div>
{/snippet}

<div
    class="fixed top-0 left-0 -z-50 h-screen w-screen bg-linear-to-t from-[#020] to-black select-none">
    {#each wah as meta}
        {@render info(meta)}
    {/each}
</div>

<style>
    .vertical-text {
        letter-spacing: 0.3rem;
        writing-mode: vertical-rl;
        text-orientation: upright;
    }

    @font-face {
        font-family: 'Sixtyfour';
        src: url('/Sixtyfour-Regular-VariableFont_BLED,SCAN.ttf');
    }

    .WAHHHH {
        font-family: 'Sixtyfour', sans-serif;
        font-optical-sizing: auto;
        font-weight: 400;
        font-style: normal;
        font-variation-settings:
            'BLED' 0,
            'SCAN' 0;
    }

    .glow {
        background-clip: text;
        /* background-image: linear-gradient(#000, #0f0, #fff); */
        color: transparent;
        /* text-shadow: 0 0 12px #0f0, 0 0 12px var(--tw-text-shadow-color), 0 0 8px var(--tw-text-shadow-color), 0 0 2px #dfd; */
    }
</style>
