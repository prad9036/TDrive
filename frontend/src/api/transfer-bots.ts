import {
    AddTransferBot as rawAddTransferBot,
    ListTransferBots as rawListTransferBots,
    RemoveTransferBot as rawRemoveTransferBot,
} from '../../bindings/TDrive/internal/app/app';
import { invokeBackend } from './gateway';
import { asRecord } from './shared';

export interface TransferBot {
    id: string;
    name: string;
    username: string;
}

function normalize(value: unknown): TransferBot {
    const raw = asRecord(value);
    return {
        id: String(raw.id ?? ''),
        name: String(raw.name ?? ''),
        username: String(raw.username ?? ''),
    };
}

export async function listTransferBots(): Promise<TransferBot[]> {
    const raw = await invokeBackend(rawListTransferBots);
    return Array.isArray(raw) ? raw.map(normalize) : [];
}

export async function addTransferBot(name: string, token: string): Promise<TransferBot> {
    return normalize(await invokeBackend(rawAddTransferBot, name, token));
}

export async function removeTransferBot(id: string): Promise<void> {
    await invokeBackend(rawRemoveTransferBot, id);
}
