import {
    BeginP2PReceive as rawBeginP2PReceive,
    CancelP2PReceive as rawCancelP2PReceive,
    FinishP2PReceive as rawFinishP2PReceive,
    WriteP2PReceiveChunk as rawWriteP2PReceiveChunk,
} from '../../bindings/TDrive/internal/app/app';
import { invokeBackend } from './gateway';

export async function beginP2PReceive(name: string, size: number): Promise<string> {
    return invokeBackend(rawBeginP2PReceive, name, size);
}

export async function writeP2PReceiveChunk(token: string, chunkBase64: string): Promise<void> {
    await invokeBackend(rawWriteP2PReceiveChunk, token, chunkBase64);
}

export async function finishP2PReceive(token: string): Promise<string> {
    return invokeBackend(rawFinishP2PReceive, token);
}

export async function cancelP2PReceive(token: string): Promise<void> {
    await invokeBackend(rawCancelP2PReceive, token);
}
