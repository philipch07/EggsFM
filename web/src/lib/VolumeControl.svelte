<script lang="ts">
    interface Props {
        volume: number;
    }

    const MAX_VOLUME = 19;

    let { volume = $bindable(0.5) }: Props = $props();

    let sliderValue = $state((volume * MAX_VOLUME).toString());

    $effect(() => {
        volume = parseInt(sliderValue) / MAX_VOLUME;
    });
</script>

<div class="field-row md:w-[300px]">
    <label for="range22">Volume:</label>
    <label for="range23">Low</label>
    <input
        id="range23"
        type="range"
        min="0"
        max={MAX_VOLUME}
        bind:value={sliderValue} />
    <label for="range24">High</label>
</div>
