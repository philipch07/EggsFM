<script lang="ts">
    interface Props {
        volume: number;
    }

    const MAX_VOLUME = 30;

    let { volume = $bindable(0.5) }: Props = $props();

    const clamp01 = (v: number) => (v < 0 ? 0 : v > 1 ? 1 : v);

    // Keep slider position as an integer tick (0..MAX_VOLUME)
    let sliderValue = $state(String(Math.round(clamp01(volume) * MAX_VOLUME)));

    // Slider -> volume
    $effect(() => {
        const n = Number.parseInt(sliderValue, 10);
        if (!Number.isFinite(n)) return;

        const next = clamp01(n / MAX_VOLUME);
        if (next !== volume) volume = next;
    });

    // volume -> slider (for reload / external updates)
    $effect(() => {
        const target = String(Math.round(clamp01(volume) * MAX_VOLUME));
        if (sliderValue !== target) sliderValue = target;
    });
</script>

<div class="field-row has-focus:outline-1 has-focus:outline-dotted md:w-75">
    <label for="volume-range">Volume:</label>
    <span>Low</span>
    <input id="volume-range" type="range" min="0" max={MAX_VOLUME} bind:value={sliderValue} />
    <span>High</span>
</div>
