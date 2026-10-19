import { Alert, Button } from "antd";
import { TicketCheck } from "lucide-react";
import { lazy, Suspense, useState } from "react";

import { AdminPageFrame } from "../components/admin-shell";
import { useUserStore } from "@/stores/use-user-store";

const RedemptionCodesPanel = lazy(() => import("../components/redemption-codes-panel"));

export default function RedemptionCodesPage() {
    const external = useUserStore((state) => state.features.walletMode === "external");
    const [createOpen, setCreateOpen] = useState(false);
    const [createBlocked, setCreateBlocked] = useState(false);

    return (
        <AdminPageFrame
            title="兑换码"
            description="批次发放、核销状态与明细追踪"
            actions={
                <Button type="primary" disabled={createBlocked || external} title={external ? "外部模式请在小洞AI发放积分" : createBlocked ? "请先核对上一次结果不确定的生成请求" : undefined} icon={<TicketCheck className="size-4" />} onClick={() => setCreateOpen(true)}>
                    生成批次
                </Button>
            }
        >
            {external ? <Alert type="info" showIcon title="外部模式不支持 Canvas 本地积分兑换" description="现有批次保留供查看，切回内部模式后可按原规则兑换。" /> : null}
            <Suspense fallback={<div className="py-16 text-center text-sm text-foreground/50">正在读取兑换码批次...</div>}>
                <RedemptionCodesPanel createOpen={createOpen && !external} onCreateOpenChange={setCreateOpen} onCreateBlockedChange={setCreateBlocked} />
            </Suspense>
        </AdminPageFrame>
    );
}
