import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";

import { getWallet, type WalletSummary } from "@/services/api/wallet";
import { useUserStore } from "@/stores/use-user-store";

// 余额不是页面心跳数据。工作区内多个位置共享同一个 Query，只在首次进入、显式
// wallet:updated 事件或用户主动刷新时访问服务端，避免常驻布局产生永久轮询。
const WALLET_STALE_TIME_MS = 5 * 60_000;
const walletBalanceQueryKey = (userId: string) => ["wallet-balance", userId] as const;

export type WalletSnapshot = {
    availableMicrocredits: number;
    policy: WalletSummary["policy"];
    walletMode: "internal" | "external";
    rechargeUrl?: string;
};

export function useWalletBalance(userId?: string, enabled = true) {
    const publicMode = useUserStore((state) => state.features.walletMode);
    const publicRechargeUrl = useUserStore((state) => state.features.rechargeUrl);
    const activeUserId = enabled ? userId || "" : "";
    const queryClient = useQueryClient();
    const queryKey = [...walletBalanceQueryKey(activeUserId), publicMode || "internal"];
    const query = useQuery({
        queryKey,
        enabled: Boolean(activeUserId),
        queryFn: async (): Promise<WalletSnapshot> => {
            const wallet = await getWallet(1, 1);
            if (wallet.account.userId !== activeUserId) throw new Error("积分账户与当前用户不一致");
            return { availableMicrocredits: wallet.account.availableMicrocredits, policy: wallet.policy, walletMode: wallet.walletMode || publicMode || "internal", rechargeUrl: wallet.rechargeUrl };
        },
        staleTime: WALLET_STALE_TIME_MS,
        refetchOnWindowFocus: publicMode === "external" ? "always" : false,
    });

    useEffect(() => {
        if (!activeUserId) return;
        const handleUpdated = () => void queryClient.invalidateQueries({ queryKey: walletBalanceQueryKey(activeUserId) });
        window.addEventListener("wallet:updated", handleUpdated);
        return () => {
            window.removeEventListener("wallet:updated", handleUpdated);
        };
    }, [activeUserId, queryClient]);

    useEffect(() => {
        if (query.error) console.warn("积分余额刷新失败", query.error);
    }, [query.error]);

    return {
        availableMicrocredits: query.isError ? null : query.data?.availableMicrocredits ?? null,
        policy: query.isError ? null : query.data?.policy ?? null,
        walletMode: query.data?.walletMode ?? publicMode ?? "internal",
        rechargeUrl: query.data?.rechargeUrl ?? publicRechargeUrl,
        error: query.error,
        refreshing: query.isFetching,
        refresh: query.refetch,
    };
}
