<script lang="ts">
    import { onMount } from 'svelte';
    import BotIcon from '@lucide/svelte/icons/bot';
    import Trash2Icon from '@lucide/svelte/icons/trash-2';
    import { addTransferBot, listTransferBots, removeTransferBot, type TransferBot } from '../../api/transfer-bots';

    let bots = $state<TransferBot[]>([]);
    let name = $state('');
    let token = $state('');
    let loading = $state(true);
    let busy = $state(false);
    let error = $state('');

    async function refresh(): Promise<void> {
        loading = true;
        error = '';
        try { bots = await listTransferBots(); }
        catch { error = 'Could not load transfer bots.'; }
        finally { loading = false; }
    }

    onMount(() => { void refresh(); });

    async function add(): Promise<void> {
        const label = name.trim();
        const secret = token.trim();
        if (!label || !secret || busy) return;
        busy = true;
        error = '';
        try {
            await addTransferBot(label, secret);
            name = '';
            token = '';
            bots = await listTransferBots();
        } catch (cause) {
            error = cause instanceof Error ? cause.message : 'Could not add this bot. Check the token and try again.';
        } finally { busy = false; }
    }

    async function remove(bot: TransferBot): Promise<void> {
        if (busy) return;
        busy = true;
        error = '';
        try {
            await removeTransferBot(bot.id);
            bots = bots.filter((item) => item.id !== bot.id);
        } catch {
            error = `Could not remove ${bot.name}.`;
        } finally { busy = false; }
    }
</script>

<section class="transfer-bots" aria-labelledby="transfer-bots-title">
    <h2 id="transfer-bots-title">Transfer bots</h2>
    <p class="transfer-bots-intro">Bots move file data only. Your Telegram account still owns TDrive files and handles drive changes. Add each bot as an administrator to the drive channels it should transfer files with.</p>

    {#if loading}
        <p role="status">Loading bots…</p>
    {:else if bots.length === 0}
        <p class="transfer-bots-empty">No transfer bots configured. File transfers will use your Telegram account.</p>
    {:else}
        <ul class="transfer-bots-list" aria-label="Configured transfer bots">
            {#each bots as bot (bot.id)}
                <li>
                    <span class="transfer-bots-identity"><BotIcon size={18} aria-hidden="true" /><span>{bot.name}{#if bot.username} <small>@{bot.username}</small>{/if}</span></span>
                    <button type="button" aria-label={`Remove ${bot.name}`} disabled={busy} onclick={() => void remove(bot)}><Trash2Icon size={17} aria-hidden="true" /></button>
                </li>
            {/each}
        </ul>
    {/if}

    <form onsubmit={(event) => { event.preventDefault(); void add(); }}>
        <label>
            <span>Bot name</span>
            <input bind:value={name} maxlength="80" autocomplete="off" required />
        </label>
        <label>
            <span>Bot token</span>
            <input bind:value={token} type="password" autocomplete="off" spellcheck="false" required />
        </label>
        <button type="submit" disabled={busy || !name.trim() || !token.trim()}>{busy ? 'Working…' : 'Add bot'}</button>
    </form>
    {#if error}<p class="transfer-bots-error" role="alert">{error}</p>{/if}
    <p class="transfer-bots-note">Tokens are stored in TDrive’s private local config. Bots need channel access to send or fetch files. Transfers stay with one bot for their duration and retries.</p>
</section>

<style>
    .transfer-bots { padding: 18px; color: var(--text-primary, inherit); }
    h2 { margin: 0 0 8px; font-size: 1.05rem; }
    p { line-height: 1.45; }
    .transfer-bots-intro, .transfer-bots-note, .transfer-bots-empty { opacity: .78; font-size: .9rem; }
    .transfer-bots-list { list-style: none; margin: 14px 0; padding: 0; }
    .transfer-bots-list li, .transfer-bots-identity { display: flex; align-items: center; gap: 10px; }
    .transfer-bots-list li { justify-content: space-between; padding: 9px 0; border-bottom: 1px solid var(--border-subtle, #8884); }
    .transfer-bots-identity small { opacity: .7; }
    button { min-height: 38px; }
    .transfer-bots-list button { border: 0; background: transparent; color: inherit; cursor: pointer; }
    form { display: grid; gap: 12px; margin-top: 16px; }
    label { display: grid; gap: 6px; font-size: .88rem; }
    input { min-width: 0; min-height: 40px; padding: 8px 10px; border: 1px solid var(--border-subtle, #8886); border-radius: 8px; background: var(--surface-input, transparent); color: inherit; }
    form > button { border: 0; border-radius: 8px; padding: 8px 14px; background: var(--accent, #4778ff); color: white; font-weight: 600; cursor: pointer; }
    button:disabled { opacity: .55; cursor: default; }
    .transfer-bots-error { color: var(--color-danger, #d54); }
    :global(html.mobile) .transfer-bots { padding: 16px 4px; }
</style>
