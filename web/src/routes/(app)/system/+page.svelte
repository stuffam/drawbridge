<script lang="ts">
	import BackupDownload from '$lib/components/BackupDownload.svelte';
	import Diagnostics from '$lib/components/Diagnostics.svelte';
	import Snapshots from '$lib/components/Snapshots.svelte';
	import WebCertificate from '$lib/components/WebCertificate.svelte';

	let { data } = $props();
</script>

<h1 class="text-2xl font-semibold tracking-tight">System</h1>

<Diagnostics />
<WebCertificate username={data.me.user.username} />
<BackupDownload username={data.me.user.username} />
<Snapshots />
