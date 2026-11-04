import { getSavedMessagesDraft, saveSavedMessagesDraft } from '../../api/text-share';
import { humanizeBackendError } from '../errors';
import { notify } from '../notifications';
import { textShareModal } from '../../ui/modals/text-share-modal-store';

let loadGeneration = 0;

export function openTextShareModal(): void {
    loadGeneration++;
    textShareModal.open({ draft: '', loading: true, loaded: false });
    void loadDraft(loadGeneration);
}

export function closeTextShareModal(): void {
    loadGeneration++;
    textShareModal.close();
}

export function retryLoadSavedMessagesDraft(): void {
    const generation = ++loadGeneration;
    textShareModal.setError('');
    textShareModal.setPayload({ draft: '', loading: true, loaded: false });
    void loadDraft(generation);
}

async function loadDraft(generation: number): Promise<void> {
    try {
        const draft = await getSavedMessagesDraft();
        if (generation !== loadGeneration) return;
        textShareModal.setPayload({ draft, loading: false, loaded: true });
    } catch (error) {
        if (generation !== loadGeneration) return;
        textShareModal.setPayload({ draft: '', loading: false, loaded: false });
        textShareModal.setError(humanizeBackendError(error));
    }
}

export async function submitSavedMessagesDraft(text: string): Promise<void> {
    const trimmedLength = Array.from(text).length;
    if (trimmedLength > 4096) {
        textShareModal.setError("Text exceeds Telegram's 4,096-character draft limit.");
        return;
    }
    textShareModal.setBusy(true);
    textShareModal.setError('');
    try {
        await saveSavedMessagesDraft(text);
        closeTextShareModal();
        notify({ level: 'success', title: 'Saved to your Telegram draft' });
    } catch (error) {
        textShareModal.setError(humanizeBackendError(error));
    } finally {
        textShareModal.setBusy(false);
    }
}
