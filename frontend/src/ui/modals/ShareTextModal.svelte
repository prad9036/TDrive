<script lang="ts">
    import ModalShell from './ModalShell.svelte';
    import { textShareModal } from './text-share-modal-store';

    interface Props {
        onClose: () => void;
        onRetry: () => void;
        onSave: (text: string) => void | Promise<void>;
    }

    let { onClose, onRetry, onSave }: Props = $props();
    let draft = $state('');
    let wasOpen = false;
    const view = textShareModal.state;

    function save(): void {
        if ($view.busy || !$view.payload?.loaded) return;
        void onSave(draft);
    }

    $effect.pre(() => {
        if (!$view.open) {
            draft = '';
            wasOpen = false;
            return;
        }
        if (!wasOpen || $view.payload?.loaded) {
            draft = $view.payload?.draft ?? '';
        }
        wasOpen = true;
    });
</script>

<ModalShell
    hostId="share-text-modal"
    open={$view.open}
    title="Share text"
    titleId="share-text-title"
    subtitle="Save text to your Telegram Saved Messages draft. This does not send a message."
    cardClass="share-text-card"
    initialFocus="#share-text-input"
    restoreFocus="#open-share-text"
    onClose={() => { if (!$view.busy) onClose(); }}
>
    {#if $view.payload?.loading}
        <p class="share-text-status" role="status">Loading your Saved Messages draft…</p>
    {:else}
        <label class="share-text-label" for="share-text-input">Text</label>
        <textarea
            id="share-text-input"
            bind:value={draft}
            maxlength="4096"
            rows="8"
            placeholder="Write or edit a draft…"
            disabled={$view.busy || !$view.payload?.loaded}
            aria-describedby={$view.error ? 'share-text-error' : 'share-text-help'}
        ></textarea>
        <p id="share-text-help" class="share-text-help">Saving replaces the current Saved Messages draft. Telegram will not send it.</p>
    {/if}

    {#if $view.error}
        <p id="share-text-error" class="share-text-error" role="alert">{$view.error}</p>
        {#if !$view.payload?.loaded && !$view.payload?.loading}
            <button class="secondary-btn share-text-retry" type="button" onclick={onRetry}>Try again</button>
        {/if}
    {/if}

    {#snippet actions()}
        <button class="secondary-btn" type="button" disabled={$view.busy} onclick={onClose}>Cancel</button>
        <button class="primary-btn" type="button" disabled={$view.busy || !$view.payload?.loaded} onclick={save}>
            {$view.busy ? 'Saving…' : 'Save draft'}
        </button>
    {/snippet}
</ModalShell>

<style>
    :global(.share-text-card) { width: min(520px, calc(100vw - 32px)); }
    .share-text-label { display: block; margin: 0 0 7px; font-weight: 600; }
    textarea { box-sizing: border-box; width: 100%; min-height: 170px; resize: vertical; padding: 10px 12px; border: 1px solid var(--border-subtle, #8886); border-radius: 9px; background: var(--surface-input, transparent); color: inherit; font: inherit; line-height: 1.45; }
    textarea:focus-visible { outline: 2px solid var(--accent, #4778ff); outline-offset: 2px; }
    textarea:disabled { opacity: .65; }
    .share-text-help, .share-text-status { margin: 8px 0 0; color: var(--text-secondary, #888); font-size: .88rem; line-height: 1.4; }
    .share-text-error { color: var(--color-danger, #d54); }
    .share-text-retry { margin-top: 8px; }
</style>
