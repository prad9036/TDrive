<script lang="ts">
    import { onDestroy } from 'svelte';
    import ModalShell from './ModalShell.svelte';
    import { isAndroidPlatform } from '../../api';
    import { canSaveToDownloads, saveToDownloads } from '../../modules/android-downloads';
    import { p2pTransferOpen } from './p2p-transfer-store';
    import {
        applyPeerAnswer, clearPeerDraft, createPeerAnswer, createPeerOffer, decodePeerInvite,
        encodePeerInvite, readPeerDraft, receivePeerBatches,
        sendPeerFiles, writePeerDraft, type PeerInvite,
    } from '../../modules/p2p-transfer';

    let mode = $state<'send' | 'receive'>('send');
    let files = $state<File[]>([]);
    let fileInput: HTMLInputElement | null = null;
    let offerCode = $state('');
    let answerCode = $state('');
    let answerShareCode = $state('');
    let status = $state('Connect to another device first, then choose files to exchange.');
    let error = $state('');
    let sent = $state(0);
    let total = $state(0);
    let connected = $state(false);
    let sending = $state(false);
    let pc = $state<RTCPeerConnection | null>(null);
    let channel: RTCDataChannel | null = null;
    let offer = $state<PeerInvite | null>(null);
    let stopped = false;
    let checkingDraft = false;
    let receivedFiles = $state<Array<{ name: string; path: string }>>([]);

    function resetConnection(): void {
        stopped = true;
        channel?.close();
        pc?.close();
        channel = null;
        pc = null;
        offer = null;
        connected = false;
        sending = false;
    }

    function close(): void {
        resetConnection();
        receivedFiles = [];
        p2pTransferOpen.set(false);
    }

    onDestroy(resetConnection);

    function selectMode(next: 'send' | 'receive'): void {
        mode = next;
        error = '';
        if (next === 'receive') {
            stopped = false;
            void watchForIncomingOffer();
        }
    }

    async function startSend(): Promise<void> {
        resetConnection(); stopped = false; error = ''; sent = 0; total = 0;
        status = 'Preparing a direct connection…';
        try {
            const created = await createPeerOffer();
            pc = created.connection; channel = created.channel; offer = created.invite;
            offerCode = encodePeerInvite(offer);
            try {
                await writePeerDraft(offer);
                status = created.hostOnly
                    ? 'Invite saved using LAN-only candidates. Waiting for the other device to connect…'
                    : 'Invite saved to Telegram Drafts. Waiting for the other device to connect…';
            } catch (e) {
                status = created.hostOnly
                    ? 'Offer ready with LAN-only candidates. Share the offer code manually with the receiving device.'
                    : 'Offer ready. Share the offer code manually with the receiving device.';
                error = `Could not save the offer to Telegram Drafts: ${message(e)}`;
            }
            handleIncoming(created.channel);
            created.channel.addEventListener('open', () => { void clearPeerDraft(created.invite.id).catch(() => undefined); }, { once: true });
            if (created.channel.readyState === 'open') void clearPeerDraft(created.invite.id).catch(() => undefined);
            created.channel.addEventListener('error', () => { error = 'The direct data channel failed.'; }, { once: true });
            pc.addEventListener('connectionstatechange', () => {
                if (pc?.connectionState === 'failed') { error = 'Direct connection failed. This network pair may require a relay, which this transfer does not use.'; }
            });
            void waitForAnswer();
        } catch (e) { error = message(e); status = 'Could not create the connection offer.'; resetConnection(); }
    }

    async function sendSelectedFiles(): Promise<void> {
        const target = channel;
        const selected = files;
        if (!target || target.readyState !== 'open' || selected.length === 0 || sending) return;
        sending = true;
        sent = 0;
        total = selected.reduce((sum, item) => sum + item.size, 0);
        status = `Sending ${selected.length} file${selected.length === 1 ? '' : 's'}…`;
        try {
            await sendPeerFiles(target, selected, (value, bytes, name) => {
                sent = value; total = bytes; status = `Sending ${name}…`;
            });
            status = `Sent ${selected.length} file${selected.length === 1 ? '' : 's'}. The connection is ready for another batch.`;
            files = [];
            if (fileInput) fileInput.value = '';
        } catch (e) { error = message(e); status = 'File transfer failed.'; }
        finally { sending = false; }
    }

    async function waitForAnswer(): Promise<void> {
        const id = offer?.id;
        if (!id || !pc) return;
        const target = pc;
        const started = Date.now();
        while (!stopped && Date.now() - started < 10 * 60_000) {
                await delay(3000);
            if (stopped) return;
            if (target.remoteDescription?.type === 'answer') return;
            try {
                const invite = await readPeerDraft();
                if (invite.role !== 'answer' || invite.id !== id) continue;
                await applyPeerAnswer(target, invite, id);
                waitForConnectionStatus(target);
                answerCode = '';
                return;
            } catch (e) {
                if (e instanceof Error && !e.message.includes('No TDrive transfer invite')) {
                    // Drafts can be temporarily unavailable while Telegram syncs; keep polling.
                    status = 'Waiting for the other device to save its answer…';
                }
            }
        }
        if (!stopped) { status = 'Invite expired. Start a new transfer.'; error = 'No answer arrived before the 10 minute invite expired.'; }
    }

    async function useAnswerCode(): Promise<void> {
        if (!pc || !offer) return;
        try {
            const answer = decodePeerInvite(answerCode);
            await applyPeerAnswer(pc, answer, offer.id);
            error = '';
            waitForConnectionStatus(pc);
            void clearPeerDraft(offer.id).catch(() => undefined);
        } catch (e) { error = message(e); }
    }

    async function receiveFromCode(): Promise<void> {
        try { await acceptOffer(decodePeerInvite(offerCode), false); }
        catch (e) { error = message(e); }
    }

    async function acceptOffer(invite: PeerInvite, useDraft: boolean): Promise<void> {
        if (invite.role !== 'offer') throw new Error('The invite is an answer. Ask the sender for the offer code.');
        resetConnection(); stopped = false; error = ''; status = 'Preparing your answer…';
        const result = await createPeerAnswer(invite);
        pc = result.connection;
        offer = invite;
        pc.addEventListener('datachannel', (event) => { handleIncoming(event.channel); }, { once: true });
        const answerCode = encodePeerInvite(result.answer);
        answerShareCode = answerCode;
        try {
            await writePeerDraft(result.answer);
            status = result.hostOnly
                ? 'Answer saved to Telegram Drafts using LAN-only candidates. Keep this screen open while the sender connects.'
                : 'Answer saved to Telegram Drafts. Keep this screen open while the sender connects.';
        } catch (e) {
            status = useDraft
                ? 'Could not save the answer to Telegram Drafts. Share the answer code manually and keep this screen open.'
                : 'Answer code ready. Share it manually and keep this screen open.';
            error = `Could not save the answer to Telegram Drafts: ${message(e)}`;
        }
    }

    async function watchForIncomingOffer(): Promise<void> {
        if (checkingDraft || stopped || mode !== 'receive' || pc) return;
        checkingDraft = true;
        status = 'Looking for a transfer offer in Telegram Drafts…';
        try {
            while (!stopped && mode === 'receive' && !pc) {
                let invite: PeerInvite;
                try {
                    invite = await readPeerDraft();
                } catch (e) {
                    if (!(e instanceof Error) || !e.message.includes('No TDrive transfer invite')) {
                        status = 'Waiting for Telegram Drafts…';
                    }
                    await delay(3000);
                    continue;
                }
                if (invite.role === 'offer') {
                    try { await acceptOffer(invite, true); }
                    catch (e) { error = message(e); status = 'Could not prepare the incoming transfer.'; }
                    return;
                }
                await delay(3000);
            }
        } finally {
            checkingDraft = false;
        }
    }

    const waitingConnections = new WeakSet<RTCPeerConnection>();

    function waitForConnectionStatus(target: RTCPeerConnection): void {
        if (waitingConnections.has(target)) return;
        waitingConnections.add(target);
        status = 'Answer accepted. Establishing the direct connection…';
        void waitForConnection(target).catch((e) => {
            if (!stopped) { error = message(e); status = 'Could not establish a direct peer connection.'; }
        });
    }

    function handleIncoming(incoming: RTCDataChannel): void {
        channel = incoming;
        const opened = (): void => {
            connected = true;
            status = 'Connected. Choose files to send or wait for files from the other device.';
            if (offer) void clearPeerDraft(offer.id).catch(() => undefined);
        };
        incoming.addEventListener('open', opened, { once: true });
        if (incoming.readyState === 'open') opened();
        void receivePeerBatches(incoming, (value, size, name) => {
            sent = value; total = size; status = `Receiving ${name}…`;
        }, async (result) => {
            let location = result.path;
            let exportError = '';
            if (isAndroidPlatform() && canSaveToDownloads()) {
                try {
                    location = await saveToDownloads(result.path);
                    if (!location) throw new Error('Android did not confirm a Downloads location.');
                } catch (e) {
                    exportError = `Could not move the received file to Downloads: ${message(e)}`;
                }
            }
            receivedFiles = [...receivedFiles, { ...result, path: location }];
            status = exportError ? 'File received, but it is still inside TDrive storage.' : `Received ${result.name} to ${location}.`;
            if (exportError) error = exportError;
        }, () => {
            status = 'Batch received. The connection is ready for more files.';
        }).catch((e) => {
            if (!stopped) { error = message(e); status = 'File transfer interrupted.'; }
        });
    }

    async function copyCode(value = offerCode): Promise<void> {
        try { await navigator.clipboard.writeText(value); status = 'Share code copied to clipboard.'; }
        catch { error = 'Clipboard access failed. Select and copy the share code manually.'; }
    }

    function message(error: unknown): string { return error instanceof Error ? error.message : String(error); }
    function delay(ms: number): Promise<void> { return new Promise((resolve) => window.setTimeout(resolve, ms)); }
    function waitForConnection(connection: RTCPeerConnection): Promise<void> {
        if (connection.connectionState === 'connected') return Promise.resolve();
        return new Promise((resolve, reject) => {
            const timeout = window.setTimeout(() => finish(new Error('This network pair could not establish a direct route. Try another network; this transfer has no relay server.')), 30_000);
            const changed = () => {
                if (connection.connectionState === 'connected') finish();
                else if (connection.connectionState === 'failed' || connection.connectionState === 'closed') finish(new Error('The direct peer connection failed.'));
            };
            const finish = (reason?: Error) => {
                window.clearTimeout(timeout);
                connection.removeEventListener('connectionstatechange', changed);
                if (reason) reject(reason); else resolve();
            };
            connection.addEventListener('connectionstatechange', changed);
            changed();
        });
    }
</script>

<ModalShell
    hostId="p2p-transfer-modal"
    open={$p2pTransferOpen}
    title="Direct file transfer"
    titleId="p2p-transfer-title"
    subtitle="Connect first, then send batches of files in either direction. Telegram Drafts carries only temporary connection details."
    cardClass="p2p-transfer-card"
    initialFocus="#p2p-transfer-send"
    restoreFocus="#open-p2p-transfer"
    onClose={close}
>
    <div class="p2p-tabs" role="tablist" aria-label="Transfer direction">
        <button id="p2p-transfer-send" class:active={mode === 'send'} role="tab" aria-selected={mode === 'send'} onclick={() => selectMode('send')}>Send a file</button>
        <button class:active={mode === 'receive'} role="tab" aria-selected={mode === 'receive'} onclick={() => selectMode('receive')}>Receive a file</button>
    </div>

    {#if mode === 'send'}
        {#if !connected}
            <button class="primary-btn" type="button" disabled={!!pc} onclick={() => void startSend()}>Connect to another device</button>
        {:else}
            <label class="p2p-label" for="p2p-send-files">Choose files to send</label>
            <input id="p2p-send-files" bind:this={fileInput} type="file" multiple onchange={(event) => { files = Array.from(event.currentTarget.files ?? []); }} />
            <button class="primary-btn" type="button" disabled={files.length === 0 || sending} onclick={() => void sendSelectedFiles()}>
                {sending ? 'Sending…' : files.length === 0 ? 'Choose files' : `Send ${files.length} file${files.length === 1 ? '' : 's'}`}
            </button>
        {/if}
        {#if offer && pc?.signalingState === 'have-local-offer' && !connected}
            <label class="p2p-label" for="p2p-answer-code">Answer code (for manual sharing)</label>
            <textarea id="p2p-answer-code" bind:value={answerCode} rows="3" placeholder="Paste the receiver's answer code here"></textarea>
            <button class="secondary-btn" type="button" onclick={() => void useAnswerCode()}>Apply answer code</button>
            <label class="p2p-label" for="p2p-share-code">Temporary offer code</label>
            <textarea id="p2p-share-code" readonly value={offerCode} rows="4"></textarea>
            <button class="secondary-btn" type="button" onclick={() => void copyCode()}>Copy offer code</button>
        {/if}
    {:else}
        {#if !connected}
            <div class="p2p-buttons">
                <button class="secondary-btn" type="button" onclick={() => void watchForIncomingOffer()}>Check Drafts now</button>
            </div>
            <label class="p2p-label" for="p2p-offer-code">Or paste offer code</label>
            <textarea id="p2p-offer-code" bind:value={offerCode} rows="4" placeholder="Paste the sender's offer code"></textarea>
            <button class="primary-btn" type="button" disabled={!!pc} onclick={() => void receiveFromCode()}>Accept offer code</button>
        {:else}
            <label class="p2p-label" for="p2p-send-files">Choose files to send</label>
            <input id="p2p-send-files" bind:this={fileInput} type="file" multiple onchange={(event) => { files = Array.from(event.currentTarget.files ?? []); }} />
            <button class="primary-btn" type="button" disabled={files.length === 0 || sending} onclick={() => void sendSelectedFiles()}>
                {sending ? 'Sending…' : files.length === 0 ? 'Choose files' : `Send ${files.length} file${files.length === 1 ? '' : 's'}`}
            </button>
        {/if}
        {#if offer && pc && !connected}
            <label class="p2p-label" for="p2p-answer-share-code">Answer code to send back</label>
            <textarea id="p2p-answer-share-code" readonly value={answerShareCode} rows="4"></textarea>
            <button class="secondary-btn" type="button" onclick={() => void copyCode(answerShareCode)}>Copy answer code</button>
        {/if}
    {/if}

    <p class="p2p-status" role="status">{status}</p>
    {#if total > 0}<progress max={total} value={sent}></progress><p class="p2p-status">{(sent / total * 100).toFixed(1)}%</p>{/if}
    {#if receivedFiles.length > 0}
        <div class="p2p-received" aria-label="Received files">
            {#each receivedFiles as receivedFile, index (`${receivedFile.name}-${index}`)}
                <p class="p2p-status">Saved {receivedFile.name}: {receivedFile.path}</p>
            {/each}
        </div>
    {/if}
    {#if error}<p class="p2p-error" role="alert">{error}</p>{/if}

    {#snippet actions()}
        <button class="secondary-btn" type="button" onclick={close}>Close</button>
    {/snippet}
</ModalShell>

<style>
    :global(.p2p-transfer-card) { width: min(560px, calc(100vw - 32px)); }
    .p2p-tabs, .p2p-buttons { display: flex; gap: 8px; margin-bottom: 14px; flex-wrap: wrap; }
    .p2p-tabs button, .p2p-buttons button { flex: 1; }
    .p2p-tabs button { border: 1px solid var(--border-subtle, #8886); border-radius: 8px; padding: 9px; background: transparent; color: inherit; }
    .p2p-tabs button.active { border-color: var(--accent, #4778ff); background: color-mix(in srgb, var(--accent, #4778ff) 12%, transparent); }
    .p2p-label { display: block; margin: 12px 0 6px; font-weight: 600; }
    input[type="file"] { display: block; max-width: 100%; margin-bottom: 12px; }
    textarea { box-sizing: border-box; width: 100%; resize: vertical; padding: 9px; border: 1px solid var(--border-subtle, #8886); border-radius: 8px; background: var(--surface-input, transparent); color: inherit; font: 12px/1.4 monospace; }
    .p2p-status { color: var(--text-secondary, #888); font-size: .9rem; line-height: 1.4; }
    .p2p-error { color: var(--color-danger, #d54); }
    progress { width: 100%; }
</style>
