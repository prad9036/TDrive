import {
    GetSavedMessagesDraft as rawGetSavedMessagesDraft,
    SaveSavedMessagesDraft as rawSaveSavedMessagesDraft,
} from '../../bindings/TDrive/internal/app/textshareservice';
import { invokeBackend } from './gateway';

export async function getSavedMessagesDraft(): Promise<string> {
    return invokeBackend(rawGetSavedMessagesDraft);
}

export async function saveSavedMessagesDraft(text: string): Promise<void> {
    await invokeBackend(rawSaveSavedMessagesDraft, text);
}
