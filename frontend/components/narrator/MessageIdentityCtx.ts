import { createContext, useContext } from "react";

export type MessageIdentity = {
	messageId?: string;
	messageSeq?: number;
	role?: string;
};

export const MessageIdentityCtx = createContext<MessageIdentity>({});

export function useMessageIdentity(): MessageIdentity {
	return useContext(MessageIdentityCtx);
}

export function useMessageIdentityAttributes(messageId?: string): Record<string, string> {
	const identity = useMessageIdentity();
	const resolvedMessageId = messageId ?? identity.messageId;
	return {
		...(resolvedMessageId ? { "data-message-id": resolvedMessageId } : {}),
		...(identity.messageSeq != null ? { "data-message-seq": String(identity.messageSeq) } : {}),
		...(identity.role ? { "data-role": identity.role } : {}),
	};
}
