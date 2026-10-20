import { getFeatureAvailability } from "@/services/api/auth";
import { useUserStore } from "@/stores/use-user-store";

let refreshSequence = 0;

export async function refreshFeatureAvailability(signal?: AbortSignal, { notifyWallet = true }: { notifyWallet?: boolean } = {}) {
    const sequence = ++refreshSequence;
    const { user, features } = useUserStore.getState();
    const payload = await getFeatureAvailability(signal);
    signal?.throwIfAborted();
    const current = useUserStore.getState();
    // A wallet response, session change or settings save can be newer than this
    // public response. Never replace that state with an earlier request's copy.
    if (sequence === refreshSequence && current.user === user && current.features === features) {
        current.setFeatures(payload.features);
        const next = useUserStore.getState().features;
        if (notifyWallet && typeof window !== "undefined" && (next.walletMode !== features.walletMode || next.creditsEnabled !== features.creditsEnabled || next.rechargeUrl !== features.rechargeUrl)) {
            window.dispatchEvent(new CustomEvent("wallet:updated"));
        }
    }
    return useUserStore.getState().features;
}
