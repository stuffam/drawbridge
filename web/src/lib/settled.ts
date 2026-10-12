import { api, type SettingsResult } from './api';
import { setPending } from './pending.svelte';

/** What a page says when the answer to a settings change came after the time to keep it ran out. */
export const lateNote =
	'The answer came after the time to keep the change ran out, so this is what the server has now.';

/**
 * The answer to a settings change that arrived after the change's time was up. The change in it was
 * probably undone before it got here, so its settings are out of date: this asks the server for the
 * settings and for what is waiting, and records that.
 */
export async function settledResult(late: SettingsResult): Promise<SettingsResult> {
	const [settings, state] = await Promise.all([api.server(), api.applyState()]);
	setPending(state.pending_change);
	return { ...late, settings, pending_change: state.pending_change };
}
