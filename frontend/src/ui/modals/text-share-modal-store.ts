import { createModalController } from './modal-store';

export interface TextSharePayload {
    draft: string;
    loading: boolean;
    loaded: boolean;
}

export const textShareModal = createModalController<TextSharePayload>();
