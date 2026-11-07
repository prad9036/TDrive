import { getSavedMessagesDraft, saveSavedMessagesDraft } from '../api/text-share';
import { beginP2PReceive, cancelP2PReceive, finishP2PReceive, writeP2PReceiveChunk } from '../api/p2p-transfer';

/** Signaling envelope stored temporarily in the Telegram Saved Messages draft. */
export interface PeerInvite {
    protocol: 'tdrive-p2p-v1';
    id: string;
    role: 'offer' | 'answer';
    sdp: string;
    expiresAt: number;
}

const PREFIX = 'TDRIVE-P2P:';
const MAX_AGE_MS = 10 * 60 * 1000;

export function encodePeerInvite(invite: PeerInvite): string {
    const bytes = new TextEncoder().encode(JSON.stringify(invite));
    let binary = '';
    for (const byte of bytes) binary += String.fromCharCode(byte);
    return PREFIX + btoa(binary);
}

export function decodePeerInvite(text: string): PeerInvite {
    const line = text.split(/\r?\n/).find((item) => item.startsWith(PREFIX));
    if (!line) throw new Error('No TDrive transfer invite was found in the draft.');
    let invite: unknown;
    try {
        const binary = atob(line.slice(PREFIX.length));
        const bytes = Uint8Array.from(binary, (char) => char.charCodeAt(0));
        invite = JSON.parse(new TextDecoder().decode(bytes));
    } catch {
        throw new Error('The TDrive transfer invite is invalid.');
    }
    if (!invite || typeof invite !== 'object') throw new Error('The TDrive transfer invite is invalid.');
    const value = invite as Partial<PeerInvite>;
    if (value.protocol !== 'tdrive-p2p-v1' || typeof value.id !== 'string'
        || (value.role !== 'offer' && value.role !== 'answer') || typeof value.sdp !== 'string'
        || typeof value.expiresAt !== 'number' || value.expiresAt < Date.now()
        || value.expiresAt > Date.now() + MAX_AGE_MS + 60_000) {
        throw new Error('The TDrive transfer invite is expired or invalid.');
    }
    return value as PeerInvite;
}

export async function createPeerOffer(): Promise<{ invite: PeerInvite; connection: RTCPeerConnection; channel: RTCDataChannel; hostOnly: boolean }> {
    try {
        return await createPeerOfferAttempt([{ urls: 'stun:stun.l.google.com:19302' }], false);
    } catch (error) {
        if (!isICEGatheringTimeout(error)) throw error;
        // Some WebView2 builds reject setLocalDescription when their STUN
        // gathering deadline expires. Retry with host candidates so the user
        // can still create an offer for peers that have a directly reachable
        // route (most commonly devices on the same LAN).
        return createPeerOfferAttempt([], true);
    }
}

async function createPeerOfferAttempt(iceServers: RTCIceServer[], hostOnly: boolean): Promise<{ invite: PeerInvite; connection: RTCPeerConnection; channel: RTCDataChannel; hostOnly: boolean }> {
    const pc = newPeerConnection(iceServers);
    const channel = pc.createDataChannel('tdrive-file', { ordered: true });
    try {
        const offer = await pc.createOffer();
        try {
            await pc.setLocalDescription(offer);
        } catch (error) {
            // A few WebRTC runtimes reject at the gather deadline even though
            // they have already installed a usable local description.
            if (!isICEGatheringTimeout(error) || !pc.localDescription?.sdp) throw error;
        }
        await waitForICE(pc);
        const sdp = pc.localDescription?.sdp;
        if (!sdp) throw new Error('The WebRTC engine did not produce a local offer.');
        const invite = { protocol: 'tdrive-p2p-v1' as const, id: newTransferID(), role: 'offer' as const, sdp, expiresAt: Date.now() + MAX_AGE_MS };
        return { invite, connection: pc, channel, hostOnly };
    } catch (error) { channel.close(); pc.close(); throw error; }
}

/** Send a batch of files sequentially, acknowledging every bounded chunk. */
export async function sendPeerFiles(
    channel: RTCDataChannel,
    files: File[],
    progress: (sent: number, total: number, name: string) => void,
): Promise<void> {
    if (files.length === 0) throw new Error('Choose at least one file to send.');
    if (files.length > 500) throw new Error('A direct transfer can contain at most 500 files.');
    const chunkBytes = 64 * 1024;
    const totalBytes = files.reduce((sum, file) => sum + file.size, 0);
    let sentTotal = 0;
    const waitForControl = (type: string, expectedOffset?: number) => new Promise<void>((resolve, reject) => {
        const timeout = window.setTimeout(() => { cleanup(); reject(new Error('Peer transfer stalled.')); }, 120_000);
        const cleanup = () => { clearTimeout(timeout); channel.removeEventListener('message', message); channel.removeEventListener('close', closed); };
        const closed = () => { cleanup(); reject(new Error('The peer connection closed during transfer.')); };
        const message = (event: MessageEvent) => {
            if (typeof event.data !== 'string') return;
            let packet: { type?: string; offset?: number; message?: string };
            try { packet = JSON.parse(event.data) as typeof packet; } catch { return; }
            if (packet.type === 'error') { cleanup(); reject(new Error(packet.message || 'The receiver could not save the file.')); return; }
            if (packet.type !== type) return;
            cleanup();
            if (expectedOffset !== undefined && packet.offset !== expectedOffset) reject(new Error('Peer acknowledged an unexpected file offset.'));
            else resolve();
        };
        channel.addEventListener('message', message);
        channel.addEventListener('close', closed, { once: true });
    });

    channel.send(JSON.stringify({ type: 'batch', count: files.length }));
    await waitForControl('ready');
    for (const file of files) {
        channel.send(JSON.stringify({ type: 'file', name: file.name, mime: file.type || 'application/octet-stream', size: file.size }));
        await waitForControl('ready');
        let sent = 0;
        while (sent < file.size) {
            if (channel.readyState !== 'open') throw new Error('The peer connection closed during transfer.');
            const end = Math.min(sent + chunkBytes, file.size);
            channel.send(await file.slice(sent, end).arrayBuffer());
            await waitForControl('ack', end);
            sent += end - sent;
            progress(sentTotal + sent, totalBytes, file.name);
        }
        channel.send(JSON.stringify({ type: 'done' }));
        await waitForControl('file-complete');
        sentTotal += file.size;
    }
    channel.send(JSON.stringify({ type: 'batch-done' }));
    await waitForControl('batch-complete');
}

/** Keep one receiver attached for the lifetime of the connection. */
export function receivePeerBatches(
    channel: RTCDataChannel,
    onProgress: (received: number, total: number, name: string) => void,
    onFileSaved: (file: { name: string; path: string }) => void | Promise<void>,
    onBatchComplete: () => void,
): Promise<void> {
    channel.binaryType = 'arraybuffer';
    return new Promise((_, reject) => {
        let metadata: { name: string; mime: string; size: number } | undefined;
        let receiveToken = '';
        let batchCount = 0;
        let completedCount = 0;
        let writeQueue = Promise.resolve();
        let received = 0;
        let settled = false;
        const closed = () => fail(new Error(metadata ? 'The peer disconnected before the current file finished.' : 'The peer disconnected.'));
        const cleanup = () => { channel.removeEventListener('message', message); channel.removeEventListener('close', closed); };
        const fail = (error: unknown) => {
            if (settled) return;
            settled = true; cleanup();
            if (receiveToken) void cancelP2PReceive(receiveToken).catch(() => undefined);
            if (channel.readyState === 'open') {
                const detail = error instanceof Error ? error.message : String(error);
                try { channel.send(JSON.stringify({ type: 'error', message: detail })); } catch { /* connection already closing */ }
            }
            reject(error);
        };
        const process = async (event: MessageEvent): Promise<void> => {
            if (typeof event.data === 'string') {
                const packet = JSON.parse(event.data) as { type?: string; name?: string; mime?: string; size?: number; count?: number };
                // The data channel is duplex. These control packets belong to
                // this peer's outgoing batch; the sender's waiter consumes them.
                if (packet.type === 'ready' || packet.type === 'ack' || packet.type === 'file-complete' || packet.type === 'batch-complete') return;
                if (packet.type === 'batch') {
                    if (batchCount || typeof packet.count !== 'number' || !Number.isSafeInteger(packet.count) || packet.count < 1 || packet.count > 500) {
                        throw new Error('Invalid file batch header.');
                    }
                    batchCount = packet.count;
                    completedCount = 0;
                    channel.send(JSON.stringify({ type: 'ready' }));
                    return;
                }
                if (packet.type === 'file') {
                    const size = packet.size;
                    if (!batchCount || metadata || typeof packet.name !== 'string' || typeof size !== 'number' || !Number.isSafeInteger(size) || size < 0) throw new Error('Invalid file header.');
                    metadata = { name: packet.name, mime: packet.mime || 'application/octet-stream', size };
                    receiveToken = await beginP2PReceive(metadata.name, metadata.size);
                    channel.send(JSON.stringify({ type: 'ready' }));
                    return;
                }
                if (packet.type === 'done') {
                    if (!metadata || received !== metadata.size) throw new Error('The sender ended the transfer before all file bytes arrived.');
                    const path = await finishP2PReceive(receiveToken);
                    receiveToken = '';
                    await onFileSaved({ name: metadata.name, path });
                    metadata = undefined;
                    received = 0;
                    completedCount++;
                    channel.send(JSON.stringify({ type: 'file-complete' }));
                    return;
                }
                if (packet.type === 'batch-done') {
                    if (!batchCount || metadata || completedCount !== batchCount) throw new Error('The sender ended the batch before all files were saved.');
                    batchCount = 0;
                    completedCount = 0;
                    channel.send(JSON.stringify({ type: 'batch-complete' }));
                    onBatchComplete();
                    return;
                }
                throw new Error('Unexpected control message from peer.');
            }
            if (!metadata || !(event.data instanceof ArrayBuffer)) throw new Error('Unexpected data received from peer.');
            const next = received + event.data.byteLength;
            if (next > metadata.size || event.data.byteLength > 64 * 1024) throw new Error('Received data exceeds the announced file size or chunk limit.');
            let binary = '';
            for (const byte of new Uint8Array(event.data)) binary += String.fromCharCode(byte);
            await writeP2PReceiveChunk(receiveToken, btoa(binary));
            received = next;
            channel.send(JSON.stringify({ type: 'ack', offset: received }));
            onProgress(received, metadata.size, metadata.name);
        };
        const message = (event: MessageEvent) => {
            writeQueue = writeQueue.then(() => process(event)).catch(fail);
        };
        channel.addEventListener('message', message);
        channel.addEventListener('close', closed);
    });
}

export async function createPeerAnswer(offer: PeerInvite): Promise<{ answer: PeerInvite; connection: RTCPeerConnection; hostOnly: boolean }> {
    if (offer.role !== 'offer') throw new Error('This invite is not a connection offer.');
    try {
        return await createPeerAnswerAttempt(offer, [{ urls: 'stun:stun.l.google.com:19302' }], false);
    } catch (error) {
        if (!isICEGatheringTimeout(error)) throw error;
        return createPeerAnswerAttempt(offer, [], true);
    }
}

async function createPeerAnswerAttempt(offer: PeerInvite, iceServers: RTCIceServer[], hostOnly: boolean): Promise<{ answer: PeerInvite; connection: RTCPeerConnection; hostOnly: boolean }> {
    const pc = newPeerConnection(iceServers);
    try {
        await pc.setRemoteDescription({ type: 'offer', sdp: offer.sdp });
        const answer = await pc.createAnswer();
        await pc.setLocalDescription(answer);
        await waitForICE(pc);
        const sdp = pc.localDescription?.sdp;
        if (!sdp) throw new Error('The WebRTC engine did not produce a local answer.');
        return {
            answer: { protocol: 'tdrive-p2p-v1', id: offer.id, role: 'answer', sdp, expiresAt: offer.expiresAt },
            connection: pc,
            hostOnly,
        };
    } catch (error) { pc.close(); throw error; }
}

const pendingPeerAnswers = new WeakMap<RTCPeerConnection, { sdp: string; promise: Promise<void> }>();

export async function applyPeerAnswer(pc: RTCPeerConnection, answer: PeerInvite, id: string): Promise<void> {
    if (answer.role !== 'answer' || answer.id !== id) throw new Error('The answer does not match this transfer.');
    if (pc.remoteDescription?.type === 'answer') {
        if (pc.remoteDescription.sdp === answer.sdp) return;
        throw new Error('A different answer has already been applied to this transfer.');
    }
    const pending = pendingPeerAnswers.get(pc);
    if (pending) {
        if (pending.sdp !== answer.sdp) throw new Error('A different answer is already being applied to this transfer.');
        await pending.promise;
        return;
    }
    if (pc.signalingState !== 'have-local-offer') {
        throw new Error(`Cannot apply the answer while the connection is ${pc.signalingState}.`);
    }
    const promise = pc.setRemoteDescription({ type: 'answer', sdp: answer.sdp });
    pendingPeerAnswers.set(pc, { sdp: answer.sdp, promise });
    try { await promise; }
    finally { pendingPeerAnswers.delete(pc); }
}

export async function readPeerDraft(): Promise<PeerInvite> {
    return decodePeerInvite(await getSavedMessagesDraft());
}

export async function writePeerDraft(invite: PeerInvite): Promise<void> {
    const current = await getSavedMessagesDraft();
    const plain = current.split(/\r?\n/).filter((line) => !line.startsWith(PREFIX)).join('\n');
    const text = [plain, encodePeerInvite(invite)].filter(Boolean).join('\n');
    await saveSavedMessagesDraft(text);
}

/** Remove only this transfer's temporary signaling line, retaining draft text. */
export async function clearPeerDraft(id: string): Promise<void> {
    const current = await getSavedMessagesDraft();
    const kept = current.split(/\r?\n/).filter((line) => {
        if (!line.startsWith(PREFIX)) return true;
        try {
            const bytes = Uint8Array.from(atob(line.slice(PREFIX.length)), (char) => char.charCodeAt(0));
            const invite = JSON.parse(new TextDecoder().decode(bytes)) as PeerInvite;
            return invite.id !== id;
        }
        catch { return true; }
    });
    await saveSavedMessagesDraft(kept.join('\n'));
}

function waitForICE(pc: RTCPeerConnection): Promise<void> {
    if (pc.iceGatheringState === 'complete') return Promise.resolve();
    return new Promise((resolve) => {
        // Keep any candidates gathered so far if STUN is unreachable. Host
        // candidates may still connect peers on the same LAN; a timeout here
        // should not be misreported as failure to create the offer itself.
        const timeout = window.setTimeout(() => done(), 8_000);
        function done(): void {
            window.clearTimeout(timeout);
            pc.removeEventListener('icegatheringstatechange', changed);
            resolve();
        }
        function changed(): void {
            if (pc.iceGatheringState === 'complete') done();
        }
        pc.addEventListener('icegatheringstatechange', changed);
    });
}

function newPeerConnection(iceServers: RTCIceServer[] = [{ urls: 'stun:stun.l.google.com:19302' }]): RTCPeerConnection {
    if (typeof RTCPeerConnection === 'undefined') {
        throw new Error('WebRTC is unavailable in this app WebView. Update Android System WebView or use a supported desktop WebView.');
    }
    return new RTCPeerConnection({ iceServers });
}

function isICEGatheringTimeout(error: unknown): boolean {
    const detail = error instanceof Error ? error.message : String(error);
    return /ice.*gather|gather.*ice/i.test(detail) && /timed?\s*out|timeout/i.test(detail);
}

function newTransferID(): string {
    const bytes = new Uint8Array(16);
    if (!globalThis.crypto?.getRandomValues) throw new Error('Secure random number generation is unavailable in this WebView.');
    globalThis.crypto.getRandomValues(bytes);
    bytes[6] = (bytes[6] & 0x0f) | 0x40;
    bytes[8] = (bytes[8] & 0x3f) | 0x80;
    const hex = Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('');
    return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}
