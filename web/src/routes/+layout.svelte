<script lang="ts">
	import './layout.css';
	import favicon from '$lib/assets/favicon.svg';
	import NewVersionBar from '$lib/components/NewVersionBar.svelte';

	let { children } = $props();
</script>

<svelte:head>
	<title>Drawbridge</title>
	<link rel="icon" href={favicon} />
</svelte:head>

<div class="min-h-screen bg-white text-neutral-900 dark:bg-neutral-950 dark:text-neutral-100">
	<NewVersionBar />
	{@render children()}
</div>
